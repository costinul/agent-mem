package engine

import (
	"context"
	"encoding/json"
	"fmt"

	models "agentmem/internal/models"

	"github.com/costinul/bwai"
	"github.com/costinul/bwai/bwaiclient"
	"github.com/google/uuid"
)

// DecomposeRequest is the input for decomposing a single source.
type DecomposeRequest struct {
	SourceKind     models.SourceKind
	Author         *string
	Content        string
	ContextHeader  string
	MessageHistory []string
}

// DecomposeRecallRequest is the input for decomposing a recall query into search phrases.
type DecomposeRecallRequest struct {
	Content string
}

// EvaluateRequest is the input for the evaluate step.
type EvaluateRequest struct {
	NewFacts       []models.ExtractedFact
	RetrievedFacts []models.Fact
}

// SelectFactsRequest is the input for the fact selection step.
type SelectFactsRequest struct {
	Query      string
	Candidates []models.Fact
}

// LLMAdapter wraps the bwai client and exposes the three LLM operations the engine needs.
type LLMAdapter struct {
	client         *bwaiclient.BWAIClient
	schemaModel    string
	embeddingModel string
}

func NewLLMAdapter(client *bwaiclient.BWAIClient, schemaModel, embeddingModel string) *LLMAdapter {
	return &LLMAdapter{
		client:         client,
		schemaModel:    schemaModel,
		embeddingModel: embeddingModel,
	}
}

// Embed generates vector embeddings for the given texts.
func (a *LLMAdapter) Embed(ctx context.Context, texts []string) ([][]float64, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	raw, err := a.client.GetEmbeddings(ctx, uuid.New(), a.embeddingModel, texts)
	if err != nil {
		return nil, fmt.Errorf("get embeddings: %w", err)
	}
	result := make([][]float64, len(raw))
	for i, vec := range raw {
		f64 := make([]float64, len(vec))
		for j, v := range vec {
			f64[j] = float64(v)
		}
		result[i] = f64
	}
	return result, nil
}

// Decompose extracts facts (and queries for USER/AGENT sources) from a source using the LLM.
func (a *LLMAdapter) Decompose(ctx context.Context, req DecomposeRequest) (models.Decomposition, error) {
	promptName := "decompose_content"
	if req.SourceKind == models.SOURCE_USER || req.SourceKind == models.SOURCE_AGENT {
		promptName = "decompose_conversational"
	}

	out := &decompositionOutput{}
	err := a.client.ExecuteAs(ctx, uuid.New(), promptName, a.schemaModel, &bwai.PromptData{
		Data: req,
	}, out)
	if err != nil {
		return models.Decomposition{}, fmt.Errorf("decompose %s source: %w", req.SourceKind, err)
	}

	return models.Decomposition{
		Facts:   out.Facts,
		Queries: out.Queries,
	}, nil
}

// DecomposeRecall breaks a recall query into atomic search phrases via the LLM.
func (a *LLMAdapter) DecomposeRecall(ctx context.Context, query string) (models.Decomposition, error) {
	out := &decompositionOutput{}
	err := a.client.ExecuteAs(ctx, uuid.New(), "decompose_recall", a.schemaModel, &bwai.PromptData{
		Data: DecomposeRecallRequest{Content: query},
	}, out)
	if err != nil {
		return models.Decomposition{}, fmt.Errorf("decompose recall query: %w", err)
	}

	return models.Decomposition{
		Facts:   out.Facts,
		Queries: out.Queries,
	}, nil
}

// Evaluate determines what to do with new and retrieved facts using the LLM.
func (a *LLMAdapter) Evaluate(ctx context.Context, req EvaluateRequest) (models.EvaluateResult, error) {
	if len(req.NewFacts) == 0 && len(req.RetrievedFacts) == 0 {
		return models.EvaluateResult{}, nil
	}

	out := &evaluateOutput{}
	err := a.client.ExecuteAs(ctx, uuid.New(), "evaluate", a.schemaModel, &bwai.PromptData{
		Data: req,
	}, out)
	if err != nil {
		return models.EvaluateResult{}, fmt.Errorf("evaluate facts: %w", err)
	}

	retrievedByID := make(map[string]models.Fact, len(req.RetrievedFacts))
	for _, f := range req.RetrievedFacts {
		retrievedByID[f.ID] = f
	}

	factsToReturn := make([]models.Fact, 0, len(out.FactsToReturn))
	for _, id := range out.FactsToReturn {
		if f, ok := retrievedByID[id]; ok {
			factsToReturn = append(factsToReturn, f)
		}
	}

	factsToStore := make([]models.Fact, 0, len(out.FactsToStore))
	for _, ef := range out.FactsToStore {
		factsToStore = append(factsToStore, models.Fact{Text: ef.Text, Kind: ef.Kind})
	}

	factsToUpdate := make([]models.Fact, 0, len(out.FactsToUpdate))
	for _, u := range out.FactsToUpdate {
		if f, ok := retrievedByID[u.ID]; ok {
			f.Text = u.Text
			factsToUpdate = append(factsToUpdate, f)
		}
	}

	return models.EvaluateResult{
		FactsToReturn: factsToReturn,
		FactsToStore:  factsToStore,
		FactsToUpdate: factsToUpdate,
		FactsToEvolve: out.FactsToEvolve,
	}, nil
}

// SelectFacts uses the LLM to filter and rank candidate facts by relevance to the query.
// Returned facts preserve the order produced by the model.
func (a *LLMAdapter) SelectFacts(ctx context.Context, req SelectFactsRequest) ([]models.Fact, error) {
	if len(req.Candidates) == 0 {
		return nil, nil
	}

	out := &selectFactsOutput{}
	err := a.client.ExecuteAs(ctx, uuid.New(), "select_facts", a.schemaModel, &bwai.PromptData{
		Data: req,
	}, out)
	if err != nil {
		return nil, fmt.Errorf("select facts: %w", err)
	}

	byID := make(map[string]models.Fact, len(req.Candidates))
	for _, f := range req.Candidates {
		byID[f.ID] = f
	}

	selected := make([]models.Fact, 0, len(out.FactIDs))
	seen := make(map[string]struct{}, len(out.FactIDs))
	for _, id := range out.FactIDs {
		if _, dup := seen[id]; dup {
			continue
		}
		if f, ok := byID[id]; ok {
			selected = append(selected, f)
			seen[id] = struct{}{}
		}
	}
	return selected, nil
}

// ──────────────────────────────────────────────
// Structured output types
// ──────────────────────────────────────────────

type decompositionOutput struct {
	Facts   []models.ExtractedFact  `json:"facts"`
	Queries []models.ExtractedQuery `json:"queries"`
}

func (o *decompositionOutput) SchemaDescription() string {
	return "Extracted facts and search queries from the source content."
}

func (o *decompositionOutput) Validate() error {
	for i, f := range o.Facts {
		if f.Text == "" {
			return fmt.Errorf("facts[%d].text is empty", i)
		}
		if f.Kind != models.FACT_KIND_KNOWLEDGE && f.Kind != models.FACT_KIND_RULE && f.Kind != models.FACT_KIND_PREFERENCE {
			return fmt.Errorf("facts[%d].kind %q is invalid", i, f.Kind)
		}
	}
	return nil
}

func (o *decompositionOutput) Unmarshal(data []byte) error {
	return json.Unmarshal(data, o)
}

// ──────────────────────────────────────────────

type factUpdate struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

type evaluateOutput struct {
	FactsToReturn []string               `json:"facts_to_return"`
	FactsToStore  []models.ExtractedFact `json:"facts_to_store"`
	FactsToUpdate []factUpdate           `json:"facts_to_update"`
	FactsToEvolve []models.FactEvolution `json:"facts_to_evolve"`
}

func (o *evaluateOutput) SchemaDescription() string {
	return "Evaluation result: which existing facts to return, which new facts to store, which existing facts to update, and which to evolve into new versions."
}

func (o *evaluateOutput) Validate() error {
	for i, f := range o.FactsToStore {
		if f.Text == "" {
			return fmt.Errorf("facts_to_store[%d].text is empty", i)
		}
	}
	return nil
}

func (o *evaluateOutput) Unmarshal(data []byte) error {
	return json.Unmarshal(data, o)
}

// ──────────────────────────────────────────────

type selectFactsOutput struct {
	FactIDs []string `json:"fact_ids"`
}

func (o *selectFactsOutput) SchemaDescription() string {
	return "IDs of the candidate facts that are relevant to the query, ordered most-relevant first."
}

func (o *selectFactsOutput) Validate() error {
	return nil
}

func (o *selectFactsOutput) Unmarshal(data []byte) error {
	return json.Unmarshal(data, o)
}
