package engine

import (
	"context"
	"errors"

	models "agentmem/internal/models"

	"github.com/costinul/bwai/bwaiclient"
)

type DecomposeRequest struct {
	SourceKind     models.SourceKind
	Content        string
	ContextHeader  string
	MessageHistory []string
}

type EvaluateRequest struct {
	NewFacts       []models.ExtractedFact
	RetrievedFacts []models.Fact
}

type LLMAdapter struct {
	client *bwaiclient.BWAIClient
}

func NewLLMAdapter(client *bwaiclient.BWAIClient) *LLMAdapter {
	return &LLMAdapter{client: client}
}

func (a *LLMAdapter) Decompose(_ context.Context, _ DecomposeRequest) (models.Decomposition, error) {
	return models.Decomposition{}, errors.New("not implemented")
}

func (a *LLMAdapter) Evaluate(_ context.Context, _ EvaluateRequest) (models.EvaluateResult, error) {
	return models.EvaluateResult{}, errors.New("not implemented")
}

func (a *LLMAdapter) Embed(_ context.Context, _ []string) ([][]float64, error) {
	return nil, errors.New("not implemented")
}
