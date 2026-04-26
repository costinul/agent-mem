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
	// Timestamp is a human-readable date/time string (e.g. "8 May 2023") derived from
	// the session wall-clock time, used to resolve relative temporal expressions in facts.
	Timestamp string
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
	QueryDate  *time.Time
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

	facts := make([]models.ExtractedFact, len(out.Facts))
	for i, f := range out.Facts {
		facts[i] = f.toModel()
	}
	return models.Decomposition{
		Facts:   facts,
		Queries: out.Queries,
	}, nil
}

// DecomposeRecall breaks a recall query into atomic search phrases via the LLM.
// It also parses query_date when the LLM extracts a temporal anchor from the query.
func (a *LLMAdapter) DecomposeRecall(ctx context.Context, query string) (models.Decomposition, error) {
	out := &decompositionOutput{}
	err := a.client.ExecuteAs(ctx, uuid.New(), "decompose_recall", a.schemaModel, &bwai.PromptData{
		Data: DecomposeRecallRequest{Content: query},
	}, out)
	if err != nil {
		return models.Decomposition{}, fmt.Errorf("decompose recall query: %w", err)
	}

	facts := make([]models.ExtractedFact, len(out.Facts))
	for i, f := range out.Facts {
		facts[i] = f.toModel()
	}
	d := models.Decomposition{
		Facts:   facts,
		Queries: out.Queries,
	}
	if out.QueryDate != "" {
		if t, err := time.Parse("2006-01-02", out.QueryDate); err == nil {
			d.QueryDate = &t
		}
	}
	return d, nil
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

	// Build a template-friendly representation so the prompt can access pre-formatted dates.
	type candidateView struct {
		ID           string
		Kind         models.FactKind
		Text         string
		ReferencedAt string // ISO "YYYY-MM-DD" or "" when unset
		SupersededBy string
	}
	type templateData struct {
		Query      string
		QueryDate  string // ISO "YYYY-MM-DD" or "" when unset
		Candidates []candidateView
	}
	td := templateData{Query: req.Query}
	if req.QueryDate != nil {
		td.QueryDate = req.QueryDate.Format("2006-01-02")
	}
	for _, f := range req.Candidates {
		cv := candidateView{ID: f.ID, Kind: f.Kind, Text: f.Text}
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

// VerifyExtraction checks extracted facts against the original source content and
// restores any proper nouns, titles, or descriptive attributes that were lost during
// extraction. Returns the same number of facts in the same order; if the LLM output
// is malformed the original facts are returned unchanged.
func (a *LLMAdapter) VerifyExtraction(ctx context.Context, source string, facts []models.ExtractedFact) ([]models.ExtractedFact, error) {
	if len(facts) == 0 {
		return facts, nil
	}

	type factView struct {
		Text string `json:"text"`
		Kind string `json:"kind"`
	}
	type templateData struct {
		Source string
		Facts  []factView
	}
	td := templateData{Source: source}
	for _, f := range facts {
		td.Facts = append(td.Facts, factView{Text: f.Text, Kind: string(f.Kind)})
	}

	out := &decompositionOutput{}
	err := a.client.ExecuteAs(ctx, uuid.New(), "verify_extraction", a.schemaModel, &bwai.PromptData{
		Data: td,
	}, out)
	if err != nil {
		return facts, fmt.Errorf("verify extraction: %w", err)
	}

	if len(out.Facts) != len(facts) {
		// Wrong count — fall back to originals rather than misaligning.
		return facts, nil
	}

	result := make([]models.ExtractedFact, len(facts))
	for i, llmFact := range out.Facts {
		orig := facts[i]
		verified := llmFact.toModel()
		// Keep the original referenced_at when the LLM didn't echo one.
		if verified.ReferencedAt == nil && orig.ReferencedAt != nil {
			verified.ReferencedAt = orig.ReferencedAt
		}
		// Keep original kind if LLM produced an empty one.
		if verified.Kind == "" {
			verified.Kind = orig.Kind
		}
		result[i] = verified
	}
	return result, nil
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
	Kind         models.FactKind `json:"kind"`
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

// extractedFactStoreLLM is the evaluate step's wire shape: no referenced_at since
// the evaluate prompt doesn't generate dates.
type extractedFactStoreLLM struct {
	Text string          `json:"text"`
	Kind models.FactKind `json:"kind"`
}

type decompositionOutput struct {
	Facts     []extractedFactLLM      `json:"facts"`
	Queries   []models.ExtractedQuery `json:"queries"`
	QueryDate string                  `json:"query_date"` // ISO 8601 "YYYY-MM-DD" or ""; plain string for Azure schema compliance.
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
	FactsToReturn []string                `json:"facts_to_return"`
	FactsToStore  []extractedFactStoreLLM `json:"facts_to_store"`
	FactsToUpdate []factUpdate            `json:"facts_to_update"`
	FactsToEvolve []models.FactEvolution  `json:"facts_to_evolve"`
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
