package engine

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"

	models "agentmem/internal/models"

	"github.com/tiktoken-go/tokenizer"
)

// provenanceSuffixRe matches any trailing parenthetical that carries provenance
// metadata (added either by the deterministic mapFactForOutput suffix or by the
// decompose-conversational prompt). Used by normalizeFactText to compare facts
// by their semantic body, not by their provenance tail.
var provenanceSuffixRe = regexp.MustCompile(`(?i)\s*\([^)]*(?:as mentioned on|originally said|as of)[^)]*\)\.?\s*$`)

// normalizeFactText returns a canonical form of a fact's text suitable for
// equality-based duplicate detection. It strips trailing provenance suffixes
// (which can be added by the renderer or by extraction prompts), lowercases,
// collapses interior whitespace, and trims trailing terminal punctuation.
//
// Two facts whose normalized text is identical describe the same statement and
// are safe to dedupe: identical strings carry identical information.
func normalizeFactText(s string) string {
	s = strings.TrimSpace(s)
	for {
		next := provenanceSuffixRe.ReplaceAllString(s, "")
		if next == s {
			break
		}
		s = next
	}
	s = strings.ToLower(s)
	s = strings.Join(strings.Fields(s), " ")
	s = strings.TrimRight(s, " .;,!?")
	return s
}

// cl100kEncoder is a shared cl100k_base tokenizer used to count tokens for chunking.
// It is safe for concurrent use.
var cl100kEncoder tokenizer.Codec

func init() {
	enc, err := tokenizer.Get(tokenizer.Cl100kBase)
	if err != nil {
		panic(fmt.Sprintf("failed to initialize cl100k_base tokenizer: %v", err))
	}
	cl100kEncoder = enc
}

// persistAndDecomposeSources saves each input as a Source record, then calls the LLM
// to decompose it into extracted facts and search queries. Optionally loads recent
// conversation history from the thread to provide context to the decomposer.
// Each item's EventDate defaults to now() when not provided by the caller.
func (e *MemoryEngine) persistAndDecomposeSources(ctx context.Context, eventID, threadID string, inputs []models.InputItem, withMessageHistory bool) ([]models.Source, []models.Decomposition, error) {
	storedSources := make([]models.Source, 0, len(inputs))
	contextHeader := buildEventContextHeader(inputs)
	decompositions := make([]models.Decomposition, 0, len(inputs))

	var msgHistory []string
	if withMessageHistory && threadID != "" {
		recent, err := e.repo.ListConversationSourcesByThreadID(ctx, threadID, 10)
		if err != nil {
			return nil, nil, fmt.Errorf("load message history: %w", err)
		}
		for _, src := range recent {
			if src.Content != nil {
				if src.Author != nil {
					msgHistory = append(msgHistory, fmt.Sprintf("%s: %s", *src.Author, *src.Content))
				} else {
					msgHistory = append(msgHistory, fmt.Sprintf("[%s] %s", src.Kind, *src.Content))
				}
			}
		}
	}

	for _, item := range inputs {
		eventDate := time.Now().UTC()
		if item.EventDate != nil {
			eventDate = item.EventDate.UTC()
		}

		content := strings.TrimSpace(item.Content)
		var contentPtr *string
		if content != "" {
			contentPtr = &content
		}
		source := models.Source{
			EventID:     eventID,
			Kind:        item.Kind,
			Author:      item.Author,
			Content:     contentPtr,
			ContentType: defaultContentType(item.ContentType),
			EventDate:   eventDate,
		}
		inserted, err := e.repo.InsertSource(ctx, source)
		if err != nil {
			return nil, nil, fmt.Errorf("insert source: %w", err)
		}
		storedSources = append(storedSources, *inserted)

		isConversational := item.Kind == models.SOURCE_USER || item.Kind == models.SOURCE_AGENT
		formattedEventDate := eventDate.Format("Monday, 2 January 2006, 15:04 UTC")

		chunks := chunkContent(item.Content, e.ingestion.ChunkMaxTokens, e.ingestion.ChunkOverlapTokens)
		if len(chunks) > 1 {
			log.Printf("decompose chunked source=%s kind=%s chunks=%d", inserted.ID, item.Kind, len(chunks))
		}

		var decomposition models.Decomposition

		// Unchunked conversational: single combined call produces facts + queries.
		// Chunked conversational: per-chunk fact extraction + one separate query call (old path).
		// Non-conversational: per-chunk fact extraction only, no queries.
		if isConversational && len(chunks) == 1 && strings.TrimSpace(item.Content) != "" {
			req := DecomposeRequest{
				SourceKind:     item.Kind,
				Author:         item.Author,
				Content:        chunks[0],
				ContextHeader:  contextHeader,
				EventDate:      formattedEventDate,
				MessageHistory: msgHistory,
			}
			combined, err := e.ai.DecomposeWithQueries(ctx, req)
			if err != nil {
				return nil, nil, fmt.Errorf("decompose source %s: %w", inserted.ID, err)
			}
			decomposition = combined
		} else {
			var allFacts []models.ExtractedFact
			for _, chunk := range chunks {
				req := DecomposeRequest{
					SourceKind:    item.Kind,
					Author:        item.Author,
					Content:       chunk,
					ContextHeader: contextHeader,
					EventDate:     formattedEventDate,
				}
				if isConversational {
					req.MessageHistory = msgHistory
				}

				partial, err := e.ai.Decompose(ctx, req)
				if err != nil {
					return nil, nil, fmt.Errorf("decompose source %s: %w", inserted.ID, err)
				}
				allFacts = append(allFacts, partial.Facts...)
			}
			decomposition.Facts = allFacts

			// Separate query planning for chunked conversational sources.
			if isConversational && strings.TrimSpace(item.Content) != "" {
				qReq := DecomposeRequest{
					SourceKind:     item.Kind,
					Author:         item.Author,
					Content:        item.Content,
					ContextHeader:  contextHeader,
					EventDate:      formattedEventDate,
					MessageHistory: msgHistory,
				}
				queries, err := e.ai.DecomposeQueries(ctx, qReq)
				if err != nil {
					return nil, nil, fmt.Errorf("decompose queries source %s: %w", inserted.ID, err)
				}
				decomposition.Queries = queries
			}
		}

		decompositions = append(decompositions, decomposition)
	}

	return storedSources, decompositions, nil
}

// chunkContent splits content into token-aware chunks no larger than maxTokens,
// prepending overlapTokens of the previous chunk to each subsequent chunk.
// Paragraph boundaries are preferred; line breaks are used when paragraphs are
// too large; hard token cuts are used as a last resort.
func chunkContent(content string, maxTokens, overlapTokens int) []string {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return []string{trimmed}
	}
	if maxTokens <= 0 {
		return []string{trimmed}
	}
	if countTokens(trimmed) <= maxTokens {
		return []string{trimmed}
	}

	paragraphs := splitNonEmpty(trimmed, "\n\n")
	if len(paragraphs) <= 1 {
		paragraphs = splitNonEmpty(trimmed, "\n")
	}

	var rawChunks []string
	if len(paragraphs) > 1 {
		rawChunks = packParagraphs(paragraphs, maxTokens)
	} else {
		rawChunks = hardCutByTokens(trimmed, maxTokens)
	}

	if overlapTokens <= 0 || len(rawChunks) <= 1 {
		return rawChunks
	}
	return applyOverlap(rawChunks, overlapTokens)
}

// packParagraphs greedily packs paragraphs into chunks of at most maxTokens tokens.
func packParagraphs(paragraphs []string, maxTokens int) []string {
	var chunks []string
	var current strings.Builder
	currentTokens := 0

	flush := func() {
		if t := strings.TrimSpace(current.String()); t != "" {
			chunks = append(chunks, t)
		}
		current.Reset()
		currentTokens = 0
	}

	for _, para := range paragraphs {
		paraTokens := countTokens(para)
		if paraTokens > maxTokens {
			// paragraph is too large on its own — flush current and hard-cut
			flush()
			chunks = append(chunks, hardCutByTokens(para, maxTokens)...)
			continue
		}
		sep := ""
		if current.Len() > 0 {
			sep = "\n\n"
		}
		if currentTokens+paraTokens+countTokens(sep) > maxTokens {
			flush()
		}
		if current.Len() > 0 {
			current.WriteString("\n\n")
			currentTokens += countTokens("\n\n")
		}
		current.WriteString(para)
		currentTokens += paraTokens
	}
	flush()
	return chunks
}

// hardCutByTokens cuts a string at token boundaries, preferring a preceding
// space within the second half of the window.
func hardCutByTokens(s string, maxTokens int) []string {
	var chunks []string
	for {
		if countTokens(s) <= maxTokens {
			if t := strings.TrimSpace(s); t != "" {
				chunks = append(chunks, t)
			}
			break
		}
		// Binary-search for the largest prefix that fits in maxTokens.
		runes := []rune(s)
		lo, hi := 1, len(runes)
		for lo < hi {
			mid := (lo + hi + 1) / 2
			if countTokens(string(runes[:mid])) <= maxTokens {
				lo = mid
			} else {
				hi = mid - 1
			}
		}
		cut := lo
		// Prefer breaking at whitespace within the second half of the window.
		for i := lo; i > lo/2; i-- {
			if strings.ContainsRune(" \t\n\r", runes[i-1]) {
				cut = i
				break
			}
		}
		piece := strings.TrimSpace(string(runes[:cut]))
		if piece != "" {
			chunks = append(chunks, piece)
		}
		s = strings.TrimSpace(string(runes[cut:]))
	}
	return chunks
}

// applyOverlap prepends the last overlapTokens tokens from the previous chunk
// to the start of each subsequent chunk.
func applyOverlap(chunks []string, overlapTokens int) []string {
	result := make([]string, len(chunks))
	result[0] = chunks[0]
	for i := 1; i < len(chunks); i++ {
		suffix := lastNTokensAsText(chunks[i-1], overlapTokens)
		if suffix != "" {
			result[i] = suffix + "\n\n" + chunks[i]
		} else {
			result[i] = chunks[i]
		}
	}
	return result
}

// lastNTokensAsText returns the last n tokens of s decoded back to a string.
func lastNTokensAsText(s string, n int) string {
	ids, _, err := cl100kEncoder.Encode(s)
	if err != nil || len(ids) == 0 {
		return ""
	}
	if n >= len(ids) {
		return s
	}
	tail := ids[len(ids)-n:]
	text, err := cl100kEncoder.Decode(tail)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(text)
}

// countTokens returns the cl100k_base token count for s.
func countTokens(s string) int {
	ids, _, err := cl100kEncoder.Encode(s)
	if err != nil {
		return len([]rune(s)) / 4
	}
	return len(ids)
}

// splitNonEmpty splits s by sep and returns the trimmed, non-empty pieces.
func splitNonEmpty(s, sep string) []string {
	raw := strings.Split(s, sep)
	out := make([]string, 0, len(raw))
	for _, r := range raw {
		if t := strings.TrimSpace(r); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// buildEventContextHeader concatenates the content of USER and AGENT inputs
// into a single string passed to the LLM as context for the decomposition step.
// Lines are prefixed with the author name when available, falling back to the role tag.
func buildEventContextHeader(inputs []models.InputItem) string {
	parts := make([]string, 0, len(inputs))
	for _, input := range inputs {
		if input.Kind != models.SOURCE_USER && input.Kind != models.SOURCE_AGENT {
			continue
		}
		if input.Author != nil {
			parts = append(parts, fmt.Sprintf("%s: %s", *input.Author, strings.TrimSpace(input.Content)))
		} else {
			parts = append(parts, strings.TrimSpace(input.Content))
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

// flattenExtractedFacts collects all ExtractedFact entries from a slice of decompositions into a single flat slice.
func flattenExtractedFacts(decompositions []models.Decomposition) []models.ExtractedFact {
	facts := make([]models.ExtractedFact, 0)
	for _, decomposition := range decompositions {
		facts = append(facts, decomposition.Facts...)
	}
	return facts
}

// selectSourceIDForExtractedFact maps an extracted-fact index back to its originating source ID.
// Falls back to the last source when the index exceeds the sources slice.
func selectSourceIDForExtractedFact(sources []models.Source, idx int) string {
	if len(sources) == 0 {
		return ""
	}
	if idx < len(sources) {
		return sources[idx].ID
	}
	return sources[len(sources)-1].ID
}

// defaultContentType returns the given content type or "text/plain" when it is empty.
func defaultContentType(contentType string) string {
	trimmed := strings.TrimSpace(contentType)
	if trimmed == "" {
		return "text/plain"
	}
	return trimmed
}

// ptrString trims s and returns a pointer to it, or nil when the trimmed value is empty.
func ptrString(value string) *string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}
