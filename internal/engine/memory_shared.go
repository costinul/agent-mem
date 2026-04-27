package engine

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"
	"unicode"

	models "agentmem/internal/models"
)

// maxDecomposeChunkChars is the upper bound (in runes) on the content size sent to
// a single decompose call. Long messages are split into chunks below this size so
// the LLM keeps full attention on each piece and drops fewer details.
const maxDecomposeChunkChars = 500

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

		chunks := chunkContent(item.Content, maxDecomposeChunkChars)
		if len(chunks) > 1 {
			log.Printf("decompose chunked source=%s kind=%s chunks=%d", inserted.ID, item.Kind, len(chunks))
		}

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

		decomposition := models.Decomposition{Facts: allFacts}

		// Queries planning runs once per source, on the full content, only for conversational sources.
		// Content sources never produce queries.
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

		decompositions = append(decompositions, decomposition)
	}

	return storedSources, decompositions, nil
}

// chunkContent splits content into pieces no larger than maxChars runes, preferring
// structural delimiters that exist in any language: paragraph break ("\n\n"), then
// line break ("\n"), then any Unicode whitespace, finally a hard rune-boundary cut.
// No regex, no language-specific punctuation, no proper-noun detection.
func chunkContent(content string, maxChars int) []string {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return []string{trimmed}
	}
	if maxChars <= 0 {
		return []string{trimmed}
	}
	if runeLen(trimmed) <= maxChars {
		return []string{trimmed}
	}

	if parts := splitNonEmpty(trimmed, "\n\n"); len(parts) > 1 {
		return chunkPieces(parts, maxChars)
	}
	if parts := splitNonEmpty(trimmed, "\n"); len(parts) > 1 {
		return chunkPieces(parts, maxChars)
	}
	return hardCutByRunes(trimmed, maxChars)
}

func chunkPieces(parts []string, maxChars int) []string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, chunkContent(p, maxChars)...)
	}
	return out
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

// hardCutByRunes cuts a single piece at maxChars rune boundaries, preferring
// the nearest preceding whitespace within the second half of the window.
func hardCutByRunes(s string, maxChars int) []string {
	runes := []rune(s)
	out := make([]string, 0, (len(runes)/maxChars)+1)
	for len(runes) > 0 {
		if len(runes) <= maxChars {
			if t := strings.TrimSpace(string(runes)); t != "" {
				out = append(out, t)
			}
			break
		}
		cut := maxChars
		for i := maxChars; i > maxChars/2; i-- {
			if unicode.IsSpace(runes[i]) {
				cut = i
				break
			}
		}
		piece := strings.TrimSpace(string(runes[:cut]))
		if piece != "" {
			out = append(out, piece)
		}
		runes = runes[cut:]
	}
	return out
}

func runeLen(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
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
