package engine

import (
	"context"
	"fmt"
	"strings"

	models "agentmem/internal/models"
)

func (e *MemoryEngine) persistAndDecomposeSources(ctx context.Context, eventID, sessionID string, inputs []models.InputItem) ([]models.Source, []models.Decomposition, error) {
	storedSources := make([]models.Source, 0, len(inputs))
	contextHeader := buildEventContextHeader(inputs)
	decompositions := make([]models.Decomposition, 0, len(inputs))

	var msgHistory []string
	if sessionID != "" {
		recent, err := e.repo.ListConversationSourcesBySessionID(ctx, sessionID, 10)
		if err != nil {
			return nil, nil, fmt.Errorf("load message history: %w", err)
		}
		for _, src := range recent {
			if src.Content != nil {
				msgHistory = append(msgHistory, fmt.Sprintf("[%s] %s", src.Kind, *src.Content))
			}
		}
	}

	for _, item := range inputs {
		content := strings.TrimSpace(item.Content)
		var contentPtr *string
		if content != "" {
			contentPtr = &content
		}
		source := models.Source{
			EventID:     eventID,
			Kind:        item.Kind,
			Content:     contentPtr,
			ContentType: defaultContentType(item.ContentType),
		}
		inserted, err := e.repo.InsertSource(ctx, source)
		if err != nil {
			return nil, nil, fmt.Errorf("insert source: %w", err)
		}
		storedSources = append(storedSources, *inserted)

		req := DecomposeRequest{
			SourceKind:    item.Kind,
			Content:       item.Content,
			ContextHeader: contextHeader,
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

func buildEventContextHeader(inputs []models.InputItem) string {
	parts := make([]string, 0, len(inputs))
	for _, input := range inputs {
		if input.Kind != models.SOURCE_USER && input.Kind != models.SOURCE_AGENT {
			continue
		}
		parts = append(parts, strings.TrimSpace(input.Content))
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func flattenExtractedFacts(decompositions []models.Decomposition) []models.ExtractedFact {
	facts := make([]models.ExtractedFact, 0)
	for _, decomposition := range decompositions {
		facts = append(facts, decomposition.Facts...)
	}
	return facts
}

func selectSourceIDForExtractedFact(sources []models.Source, idx int) string {
	if len(sources) == 0 {
		return ""
	}
	if idx < len(sources) {
		return sources[idx].ID
	}
	return sources[len(sources)-1].ID
}

func defaultContentType(contentType string) string {
	trimmed := strings.TrimSpace(contentType)
	if trimmed == "" {
		return "text/plain"
	}
	return trimmed
}

func ptrString(value string) *string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}
