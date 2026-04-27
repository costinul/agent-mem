package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

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
	// EventDate is a human-readable date/time string (e.g. "Monday, 8 May 2023, 10:30 UTC") derived from
	// the caller-supplied event_date. Used to resolve relative temporal expressions in facts.
	EventDate string
}

// DecomposeRecallRequest is the input for decomposing a recall query into search phrases.
type DecomposeRecallRequest struct {
	Content   string
	EventDate string // ISO "YYYY-MM-DD" used to resolve relative-time phrases in the query.
}

// EvaluateRequest is the input for the evaluate step.
type EvaluateRequest struct {
	NewFacts       []models.ExtractedFact
	RetrievedFacts []models.Fact
}

// SelectFactsRequest is the input for the fact selection step.
type SelectFactsRequest struct {
	Query      string
	EventDate  string // ISO "YYYY-MM-DD" when the question is asked; used by the LLM to resolve relative-time phrases.
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

// Decompose extracts facts from a source using the LLM. Queries are produced by
// DecomposeQueries in a separate, single-purpose call so that fact extraction does
// not have to share attention with query planning.
func (a *LLMAdapter) Decompose(ctx context.Context, req DecomposeRequest) (models.Decomposition, error) {
	promptName := "decompose_content"
	if req.SourceKind == models.SOURCE_USER || req.SourceKind == models.SOURCE_AGENT {
		promptName = "decompose_conversational"
	}

	out := &factsOnlyOutput{}
	err := a.client.ExecuteAs(ctx, uuid.New(), promptName, a.schemaModel, &bwai.PromptData{
		Data: req,
	}, out)
	if err != nil {
		return models.Decomposition{}, fmt.Errorf("decompose %s source: %w", req.SourceKind, err)
	}

	facts := make([]models.ExtractedFact, len(out.Facts))
	for i, f := range out.Facts {
		facts[i] = f.toModel()
	}
	return models.Decomposition{Facts: facts}, nil
}

// DecomposeQueries plans the search phrases used during ingest to find related stored
// memory. Single-purpose call so the model can focus on query phrasing.
func (a *LLMAdapter) DecomposeQueries(ctx context.Context, req DecomposeRequest) ([]models.ExtractedQuery, error) {
	out := &queriesOnlyOutput{}
	err := a.client.ExecuteAs(ctx, uuid.New(), "decompose_queries", a.schemaModel, &bwai.PromptData{
		Data: req,
	}, out)
	if err != nil {
		return nil, fmt.Errorf("decompose queries: %w", err)
	}
	return out.Queries, nil
}

// DecomposeRecall breaks a recall query into atomic search phrases via the LLM.
func (a *LLMAdapter) DecomposeRecall(ctx context.Context, req DecomposeRecallRequest) (models.Decomposition, error) {
	out := &decompositionOutput{}
	err := a.client.ExecuteAs(ctx, uuid.New(), "decompose_recall", a.schemaModel, &bwai.PromptData{
		Data: req,
	}, out)
	if err != nil {
		return models.Decomposition{}, fmt.Errorf("decompose recall query: %w", err)
	}

	facts := make([]models.ExtractedFact, len(out.Facts))
	for i, f := range out.Facts {
		facts[i] = f.toModel()
	}
	return models.Decomposition{
		Facts:   facts,
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

	// LLM echoes referenced_at on each new fact — map it directly, no text-key lookup needed.
	factsToStore := make([]models.Fact, 0, len(out.FactsToStore))
	for _, ef := range out.FactsToStore {
		f := models.Fact{Text: ef.Text, Kind: ef.Kind}
		if ef.ReferencedAt != "" {
			if t, err := time.Parse("2006-01-02", ef.ReferencedAt); err == nil {
				f.ReferencedAt = &t
			}
		}
		factsToStore = append(factsToStore, f)
	}

	factsToUpdate := make([]models.Fact, 0, len(out.FactsToUpdate))
	for _, u := range out.FactsToUpdate {
		if f, ok := retrievedByID[u.ID]; ok {
			f.Text = u.Text
			if u.ReferencedAt != "" {
				if t, err := time.Parse("2006-01-02", u.ReferencedAt); err == nil {
					f.ReferencedAt = &t
				}
			}
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

	// Build a template-friendly representation so the prompt can access pre-formatted dates.
	type candidateView struct {
		ID           string
		Kind         models.FactKind
		Text         string
		EventDate    string // ISO "YYYY-MM-DD" of the source message; "" when unset
		ReferencedAt string // ISO "YYYY-MM-DD" of the described event; "" when unset
		SupersededBy string
	}
	type templateData struct {
		Query     string
		EventDate string // ISO "YYYY-MM-DD" when the question is asked; "" when unset
		Candidates []candidateView
	}
	td := templateData{Query: req.Query, EventDate: req.EventDate}
	for _, f := range req.Candidates {
		cv := candidateView{ID: f.ID, Kind: f.Kind, Text: f.Text}
		if f.EventDate != nil {
			cv.EventDate = f.EventDate.Format("2006-01-02")
		}
		if f.ReferencedAt != nil {
			cv.ReferencedAt = f.ReferencedAt.Format("2006-01-02")
		}
		if f.SupersededBy != nil {
			cv.SupersededBy = *f.SupersededBy
		}
		td.Candidates = append(td.Candidates, cv)
	}

	out := &selectFactsOutput{}
	err := a.client.ExecuteAs(ctx, uuid.New(), "select_facts", a.schemaModel, &bwai.PromptData{
		Data: td,
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

// extractedFactLLM is the LLM wire shape for a single extracted fact.
// referenced_at is a plain string (ISO 8601 "YYYY-MM-DD" or "") rather than a
// pointer so that Azure's strict schema requirement (all properties must be in
// required) is satisfied.
type extractedFactLLM struct {
	Text         string          `json:"text"`
	Kind         models.FactKind `json:"kind" jsonschema:"enum=KNOWLEDGE,enum=RULE,enum=PREFERENCE"`
	ReferencedAt string          `json:"referenced_at"`
}

func (f extractedFactLLM) toModel() models.ExtractedFact {
	ef := models.ExtractedFact{Text: f.Text, Kind: f.Kind}
	if f.ReferencedAt != "" {
		if t, err := time.Parse("2006-01-02", f.ReferencedAt); err == nil {
			ef.ReferencedAt = &t
		}
	}
	return ef
}

// factUpdateLLM is the evaluate step's wire shape for an existing fact update.
type factUpdateLLM struct {
	ID           string `json:"id"`
	Text         string `json:"text"`
	ReferencedAt string `json:"referenced_at"` // ISO 8601 "YYYY-MM-DD" or ""; plain string for schema compliance.
}

// decompositionOutput is the legacy combined shape, retained only for decompose_recall
// which currently emits both fields (facts is always empty in practice).
type decompositionOutput struct {
	Facts   []extractedFactLLM      `json:"facts"`
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

// factsOnlyOutput is the wire shape for the facts-only ingest decompose call.
type factsOnlyOutput struct {
	Facts []extractedFactLLM `json:"facts"`
}

func (o *factsOnlyOutput) SchemaDescription() string {
	return "Atomic, self-contained facts extracted from the source content."
}

func (o *factsOnlyOutput) Validate() error {
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

func (o *factsOnlyOutput) Unmarshal(data []byte) error {
	return json.Unmarshal(data, o)
}

// queriesOnlyOutput is the wire shape for the queries-only ingest decompose call.
type queriesOnlyOutput struct {
	Queries []models.ExtractedQuery `json:"queries"`
}

func (o *queriesOnlyOutput) SchemaDescription() string {
	return "Concise search phrases used to find related existing memory for this source."
}

func (o *queriesOnlyOutput) Validate() error {
	return nil
}

func (o *queriesOnlyOutput) Unmarshal(data []byte) error {
	return json.Unmarshal(data, o)
}

// ──────────────────────────────────────────────

type evaluateOutput struct {
	FactsToReturn []string               `json:"facts_to_return"`
	FactsToStore  []extractedFactLLM     `json:"facts_to_store"`
	FactsToUpdate []factUpdateLLM        `json:"facts_to_update"`
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
