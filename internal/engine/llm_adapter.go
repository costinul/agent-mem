package engine

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	models "agentmem/internal/models"
)

type DecomposeRequest struct {
	SourceKind      models.SourceKind
	Content         string
	ContextHeader   string
	MessageHistory  []string
}

type EvaluateRequest struct {
	NewFacts       []models.ExtractedFact
	RetrievedFacts []models.Fact
}

type Adapter interface {
	Decompose(context.Context, DecomposeRequest) (models.Decomposition, error)
	Evaluate(context.Context, EvaluateRequest) (models.EvaluateResult, error)
	Embed(context.Context, []string) ([][]float64, error)
}

type DefaultAdapter struct{}

func NewDefaultAdapter() *DefaultAdapter {
	return &DefaultAdapter{}
}

func (a *DefaultAdapter) Decompose(_ context.Context, req DecomposeRequest) (models.Decomposition, error) {
	content := strings.TrimSpace(req.Content)
	if content == "" {
		return models.Decomposition{}, nil
	}

	lines := splitSentences(content)
	facts := make([]models.ExtractedFact, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		facts = append(facts, models.ExtractedFact{
			Text: line,
			Kind: inferFactKind(line),
		})
	}

	decomposition := models.Decomposition{Facts: facts}
	if req.SourceKind == models.SOURCE_USER || req.SourceKind == models.SOURCE_AGENT {
		decomposition.Queries = make([]models.ExtractedQuery, 0, len(lines))
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			decomposition.Queries = append(decomposition.Queries, models.ExtractedQuery{Text: line})
		}
	}

	return decomposition, nil
}

func (a *DefaultAdapter) Evaluate(_ context.Context, req EvaluateRequest) (models.EvaluateResult, error) {
	result := models.EvaluateResult{
		FactsToReturn: make([]models.Fact, 0, len(req.NewFacts)+len(req.RetrievedFacts)),
		FactsToStore:  make([]models.Fact, 0, len(req.NewFacts)),
		FactsToUpdate: []models.Fact{},
		FactsToDelete: []string{},
	}

	seenReturn := map[string]struct{}{}
	for _, fact := range req.RetrievedFacts {
		key := normalizeText(fact.Text)
		if key == "" {
			continue
		}
		if _, ok := seenReturn[key]; ok {
			continue
		}
		seenReturn[key] = struct{}{}
		result.FactsToReturn = append(result.FactsToReturn, fact)
	}

	existing := map[string]struct{}{}
	for _, fact := range req.RetrievedFacts {
		key := normalizeText(fact.Text)
		if key != "" {
			existing[key] = struct{}{}
		}
	}
	for _, extracted := range req.NewFacts {
		key := normalizeText(extracted.Text)
		if key == "" {
			continue
		}
		fact := models.Fact{Text: strings.TrimSpace(extracted.Text), Kind: extracted.Kind}
		if _, ok := existing[key]; !ok {
			result.FactsToStore = append(result.FactsToStore, fact)
			existing[key] = struct{}{}
		}
		if _, ok := seenReturn[key]; !ok {
			seenReturn[key] = struct{}{}
			result.FactsToReturn = append(result.FactsToReturn, fact)
		}
	}

	return result, nil
}

func (a *DefaultAdapter) Embed(_ context.Context, texts []string) ([][]float64, error) {
	vectors := make([][]float64, 0, len(texts))
	for _, text := range texts {
		sum := sha256.Sum256([]byte(strings.TrimSpace(strings.ToLower(text))))
		vector := make([]float64, 8)
		for i := 0; i < len(vector); i++ {
			vector[i] = float64(int(sum[i])) / 255.0
		}
		vectors = append(vectors, vector)
	}
	return vectors, nil
}

func ParseDecompositionJSON(raw string) (models.Decomposition, error) {
	type payload struct {
		Facts   []models.ExtractedFact  `json:"facts"`
		Queries []models.ExtractedQuery `json:"queries"`
	}
	var parsed payload
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return models.Decomposition{}, fmt.Errorf("parse decomposition json: %w", err)
	}
	for _, fact := range parsed.Facts {
		if strings.TrimSpace(fact.Text) == "" {
			return models.Decomposition{}, fmt.Errorf("invalid decomposition payload: empty fact text")
		}
	}
	return models.Decomposition{
		Facts:   parsed.Facts,
		Queries: parsed.Queries,
	}, nil
}

func ParseEvaluateJSON(raw string) (models.EvaluateResult, error) {
	var parsed models.EvaluateResult
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return models.EvaluateResult{}, fmt.Errorf("parse evaluate json: %w", err)
	}
	return parsed, nil
}

func splitSentences(content string) []string {
	replacer := strings.NewReplacer("\r\n", "\n", ";", ". ", "!", ". ", "?", ". ")
	normalized := replacer.Replace(content)
	parts := strings.Split(normalized, ".")
	lines := make([]string, 0, len(parts))
	for _, part := range parts {
		segment := strings.TrimSpace(part)
		if segment == "" {
			continue
		}
		lines = append(lines, segment)
	}
	if len(lines) == 0 {
		return []string{strings.TrimSpace(content)}
	}
	return lines
}

func inferFactKind(text string) models.FactKind {
	lower := strings.ToLower(strings.TrimSpace(text))
	if strings.HasPrefix(lower, "always ") ||
		strings.HasPrefix(lower, "never ") ||
		strings.Contains(lower, " must ") ||
		strings.Contains(lower, " should ") {
		return models.FACT_KIND_RULE
	}
	if strings.Contains(lower, "prefer") {
		return models.FACT_KIND_PREFERENCE
	}
	return models.FACT_KIND_KNOWLEDGE
}

func normalizeText(text string) string {
	return strings.ToLower(strings.TrimSpace(text))
}
