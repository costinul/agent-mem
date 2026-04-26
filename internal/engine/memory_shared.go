package engine

import (
	"context"
	"fmt"
	"strings"
	"time"

	models "agentmem/internal/models"
)

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

		req := DecomposeRequest{
			SourceKind:    item.Kind,
			Author:        item.Author,
			Content:       item.Content,
			ContextHeader: contextHeader,
			EventDate:     eventDate.Format("Monday, 2 January 2006, 15:04 UTC"),
		}
		if item.Kind == models.SOURCE_USER || item.Kind == models.SOURCE_AGENT {
			req.MessageHistory = msgHistory
		}

		decomposition, err := e.ai.Decompose(ctx, req)
		if err != nil {
			return nil, nil, fmt.Errorf("decompose source %s: %w", inserted.ID, err)
		}

		decompositions = append(decompositions, decomposition)
	}

	return storedSources, decompositions, nil
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
