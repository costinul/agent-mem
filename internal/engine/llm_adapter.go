package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
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

// SelectFactsRequest is the input for the fact selection step. The same shape is
// reused for both rounds of two-step recall: round 1 leaves AlreadySelected nil,
// round 2 fills it with the round-1 fact texts so the selector knows what's
// already committed and only picks complementary facts from the new candidates.
type SelectFactsRequest struct {
	Query      string
	EventDate  string   // ISO "YYYY-MM-DD" when the question is asked; used by the LLM to resolve relative-time phrases.
	Phrases    []string // decomposed search angles produced by the recall planner; empty when decomposition was skipped (e.g. RecallLight).
	Candidates []models.Fact
	// AlreadySelected is non-nil only on round-2 calls. It carries the TEXTS of
	// the facts the previous selector pass already picked so the model can avoid
	// re-recommending them and focus on candidates that complement the current set.
	AlreadySelected []string
}

// SelectFactsResult carries the selector's choice plus an optional gap signal.
//
// NeedMore=true means the selector judged its candidates insufficient to write a
// complete answer; Missing is a short noun phrase (1-5 words) describing what
// kind of fact would close the gap. The two-step recall loop uses this to decide
// whether to run a second selector pass over additional candidates. On round-2
// calls the loop currently only LOGS NeedMore (it does not trigger a round 3) —
// the signal is collected so we can decide later whether a third pass is worth
// the cost.
type SelectFactsResult struct {
	Facts    []models.Fact
	NeedMore bool
	Missing  string
}

// LLMModels holds per-operation model IDs for the LLM adapter.
type LLMModels struct {
	Decompose        string
	Evaluate         string
	SelectFacts      string
	SelectFactsLight string // cheaper model for RecallLight; reuses select_facts prompt
	DecomposeQueries string
	DecomposeRecall  string
}

// LLMAdapter wraps the bwai client and exposes the three LLM operations the engine needs.
type LLMAdapter struct {
	client         *bwaiclient.BWAIClient
	models         LLMModels
	embeddingModel string
	trackerReg     *trackerRegistry
}

func NewLLMAdapter(client *bwaiclient.BWAIClient, models LLMModels, embeddingModel string, reg *trackerRegistry) *LLMAdapter {
	return &LLMAdapter{
		client:         client,
		models:         models,
		embeddingModel: embeddingModel,
		trackerReg:     reg,
	}
}

// bind generates a refID and registers it against the in-flight CallTracker so
// that the bwai usage logger can route token counts back to the right tracker.
// The returned cleanup function must be deferred immediately after call.
func (a *LLMAdapter) bind(ctx context.Context) (uuid.UUID, func()) {
	refID := uuid.New()
	if t := getTracker(ctx); t != nil && a.trackerReg != nil {
		a.trackerReg.bind(refID, t)
	}
	return refID, func() {
		if a.trackerReg != nil {
			a.trackerReg.unbind(refID)
		}
	}
}

func observeLLM(ctx context.Context, op string, start time.Time) {
	elapsed := time.Since(start)
	log.Printf("llm call op=%s duration=%dms", op, elapsed.Milliseconds())
	if t := getTracker(ctx); t != nil {
		t.addLLM(elapsed)
	}
}

func observeEmbed(ctx context.Context, start time.Time) {
	elapsed := time.Since(start)
	log.Printf("embed call duration=%dms", elapsed.Milliseconds())
	if t := getTracker(ctx); t != nil {
		t.addEmbed(elapsed)
	}
}

// Embed generates vector embeddings for the given texts.
func (a *LLMAdapter) Embed(ctx context.Context, texts []string) ([][]float64, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	defer observeEmbed(ctx, time.Now())
	refID, done := a.bind(ctx)
	defer done()
	raw, err := a.client.GetEmbeddings(ctx, refID, a.embeddingModel, texts)
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
	defer observeLLM(ctx, "decompose", time.Now())

	refID, done := a.bind(ctx)
	defer done()
	out := &decompositionOutput{}
	err := a.client.ExecuteAs(ctx, refID, "decompose", a.models.Decompose, &bwai.PromptData{
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
		Facts:    facts,
		Queries:  out.Queries,
		Entities: normalizeEntities(out.Entities),
	}, nil
}

// DecomposeWithQueries extracts facts AND search queries from a conversational source
// in a single LLM call. Used for unchunked conversational sources to save one round-trip.
// The prompt is decompose (extended to produce both outputs); the output
// schema includes both facts and queries.
func (a *LLMAdapter) DecomposeWithQueries(ctx context.Context, req DecomposeRequest) (models.Decomposition, error) {
	defer observeLLM(ctx, "decompose_with_queries", time.Now())
	refID, done := a.bind(ctx)
	defer done()
	out := &decompositionOutput{}
	err := a.client.ExecuteAs(ctx, refID, "decompose", a.models.Decompose, &bwai.PromptData{
		Data: req,
	}, out)
	if err != nil {
		return models.Decomposition{}, fmt.Errorf("decompose conversational with queries: %w", err)
	}

	facts := make([]models.ExtractedFact, len(out.Facts))
	for i, f := range out.Facts {
		facts[i] = f.toModel()
	}
	return models.Decomposition{
		Facts:    facts,
		Queries:  out.Queries,
		Entities: normalizeEntities(out.Entities),
	}, nil
}

// DecomposeQueries plans the search phrases used during ingest to find related stored
// memory. Single-purpose call so the model can focus on query phrasing.
func (a *LLMAdapter) DecomposeQueries(ctx context.Context, req DecomposeRequest) ([]models.ExtractedQuery, error) {
	defer observeLLM(ctx, "decompose_queries", time.Now())
	refID, done := a.bind(ctx)
	defer done()
	out := &queriesOnlyOutput{}
	err := a.client.ExecuteAs(ctx, refID, "decompose_queries", a.models.DecomposeQueries, &bwai.PromptData{
		Data: req,
	}, out)
	if err != nil {
		return nil, fmt.Errorf("decompose queries: %w", err)
	}
	return out.Queries, nil
}

// DecomposeRecall breaks a recall query into atomic search phrases via the LLM.
func (a *LLMAdapter) DecomposeRecall(ctx context.Context, req DecomposeRecallRequest) (models.Decomposition, error) {
	defer observeLLM(ctx, "decompose_recall", time.Now())
	refID, done := a.bind(ctx)
	defer done()
	out := &decompositionOutput{}
	err := a.client.ExecuteAs(ctx, refID, "decompose_recall", a.models.DecomposeRecall, &bwai.PromptData{
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
		Facts:    facts,
		Queries:  out.Queries,
		Entities: normalizeEntities(out.Entities),
	}, nil
}

// Evaluate determines what to do with new and retrieved facts using the LLM.
func (a *LLMAdapter) Evaluate(ctx context.Context, req EvaluateRequest) (models.EvaluateResult, error) {
	if len(req.NewFacts) == 0 && len(req.RetrievedFacts) == 0 {
		return models.EvaluateResult{}, nil
	}
	defer observeLLM(ctx, "evaluate", time.Now())
	refID, done := a.bind(ctx)
	defer done()
	out := &evaluateOutput{}
	err := a.client.ExecuteAs(ctx, refID, "evaluate", a.models.Evaluate, &bwai.PromptData{
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
		f := models.Fact{Text: ef.Text, Kind: ef.Kind, Entities: normalizeEntities(ef.Entities)}
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
			f.Entities = normalizeEntities(u.Entities)
			if u.ReferencedAt != "" {
				if t, err := time.Parse("2006-01-02", u.ReferencedAt); err == nil {
					f.ReferencedAt = &t
				}
			}
			factsToUpdate = append(factsToUpdate, f)
		}
	}

	factsToEvolve := make([]models.FactEvolution, len(out.FactsToEvolve))
	for i, ev := range out.FactsToEvolve {
		factsToEvolve[i] = models.FactEvolution{
			OldFactID:       ev.OldFactID,
			NewText:         ev.NewText,
			NewKind:         ev.NewKind,
			NewEntities:     normalizeEntities(ev.NewEntities),
			NewReferencedAt: ev.NewReferencedAt,
		}
	}

	return models.EvaluateResult{
		FactsToReturn: factsToReturn,
		FactsToStore:  factsToStore,
		FactsToUpdate: factsToUpdate,
		FactsToEvolve: factsToEvolve,
	}, nil
}

// SelectFacts runs the strong selector model on the given candidates and returns
// its picks plus the NeedMore signal. Used as round 1 of two-step recall and as
// the only call in single-step recall.
func (a *LLMAdapter) SelectFacts(ctx context.Context, req SelectFactsRequest) (SelectFactsResult, error) {
	return a.selectFactsCore(ctx, "select_facts", a.models.SelectFacts, req)
}

// SelectFactsGap runs the round-2 selector pass on a fresh batch of candidates
// using the cheap light model. It uses the SAME prompt as SelectFacts; the only
// differences are the model and that req.AlreadySelected is populated so the
// model treats the round-1 picks as committed and only picks complementary
// facts. The returned NeedMore is currently only logged by the caller — it is
// collected so we can later judge whether a round 3 is worth implementing.
func (a *LLMAdapter) SelectFactsGap(ctx context.Context, req SelectFactsRequest) (SelectFactsResult, error) {
	return a.selectFactsCore(ctx, "select_facts_gap", a.models.SelectFactsLight, req)
}

// selectFactsCore is the shared implementation behind every selector call. The op
// label is used only for tracking/observability — the prompt itself is always
// "select_facts", so round 1 and round 2 share rules and examples and can never
// silently diverge.
func (a *LLMAdapter) selectFactsCore(ctx context.Context, op, modelID string, req SelectFactsRequest) (SelectFactsResult, error) {
	if len(req.Candidates) == 0 {
		return SelectFactsResult{}, nil
	}
	defer observeLLM(ctx, op, time.Now())
	refID, done := a.bind(ctx)
	defer done()

	type candidateView struct {
		ID           string
		Kind         models.FactKind
		Text         string
		EventDate    string // ISO "YYYY-MM-DD" of the source message; "" when unset
		ReferencedAt string // ISO "YYYY-MM-DD" of the described event; "" when unset
		SupersededBy string
	}
	type templateData struct {
		Query           string
		EventDate       string // ISO "YYYY-MM-DD" when the question is asked; "" when unset
		Phrases         []string
		AlreadySelected []string // empty/nil for round-1 calls
		Candidates      []candidateView
	}
	td := templateData{
		Query:           req.Query,
		EventDate:       req.EventDate,
		Phrases:         req.Phrases,
		AlreadySelected: req.AlreadySelected,
	}
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
	if err := a.client.ExecuteAs(ctx, refID, "select_facts", modelID, &bwai.PromptData{Data: td}, out); err != nil {
		return SelectFactsResult{}, fmt.Errorf("%s: %w", op, err)
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
	return SelectFactsResult{
		Facts:    selected,
		NeedMore: out.NeedMore,
		Missing:  strings.TrimSpace(out.Missing),
	}, nil
}

// SelectFactsLight runs the same select_facts prompt on the cheaper light model.
func (a *LLMAdapter) SelectFactsLight(ctx context.Context, req SelectFactsRequest) ([]models.Fact, error) {
	if len(req.Candidates) == 0 {
		return nil, nil
	}
	defer observeLLM(ctx, "select_facts_light", time.Now())
	refID, done := a.bind(ctx)
	defer done()

	type candidateView struct {
		ID           string
		Kind         models.FactKind
		Text         string
		EventDate    string
		ReferencedAt string
		SupersededBy string
	}
	type templateData struct {
		Query      string
		EventDate  string
		Phrases    []string
		Candidates []candidateView
	}
	td := templateData{Query: req.Query, EventDate: req.EventDate, Phrases: req.Phrases}
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
	err := a.client.ExecuteAs(ctx, refID, "select_facts", a.models.SelectFactsLight, &bwai.PromptData{
		Data: td,
	}, out)
	if err != nil {
		return nil, fmt.Errorf("select facts light: %w", err)
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
	Entities     []string        `json:"entities"`
	ReferencedAt string          `json:"referenced_at"`
}

func (f extractedFactLLM) toModel() models.ExtractedFact {
	ef := models.ExtractedFact{Text: f.Text, Kind: f.Kind, Entities: normalizeEntities(f.Entities)}
	if f.ReferencedAt != "" {
		if t, err := time.Parse("2006-01-02", f.ReferencedAt); err == nil {
			ef.ReferencedAt = &t
		}
	}
	return ef
}

// normalizeEntities lowercases, trims, and deduplicates entity strings while preserving
// first-seen order. Empty strings are dropped. Returns nil when the input has no usable
// entries so that the JSON omitempty tag elides the field.
func normalizeEntities(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, raw := range in {
		s := strings.ToLower(strings.TrimSpace(raw))
		if s == "" {
			continue
		}
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// factUpdateLLM is the evaluate step's wire shape for an existing fact update.
type factUpdateLLM struct {
	ID           string   `json:"id"`
	Text         string   `json:"text"`
	Entities     []string `json:"entities"`
	ReferencedAt string   `json:"referenced_at"` // ISO 8601 "YYYY-MM-DD" or ""; plain string for schema compliance.
}

// decompositionOutput is the legacy combined shape, retained only for decompose_recall
// which currently emits both fields (facts is always empty in practice).
type decompositionOutput struct {
	Facts    []extractedFactLLM      `json:"facts"`
	Queries  []models.ExtractedQuery `json:"queries"`
	Entities []string                `json:"entities"`
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

// factEvolutionLLM is the evaluate step's wire shape for a fact evolution.
// Plain strings rather than pointers/optionals so that Azure's strict schema (every
// property must be in `required`) is satisfied.
type factEvolutionLLM struct {
	OldFactID       string          `json:"old_fact_id"`
	NewText         string          `json:"new_text"`
	NewKind         models.FactKind `json:"new_kind" jsonschema:"enum=KNOWLEDGE,enum=RULE,enum=PREFERENCE"`
	NewEntities     []string        `json:"new_entities"`
	NewReferencedAt string          `json:"new_referenced_at"`
}

type evaluateOutput struct {
	FactsToReturn []string           `json:"facts_to_return"`
	FactsToStore  []extractedFactLLM `json:"facts_to_store"`
	FactsToUpdate []factUpdateLLM    `json:"facts_to_update"`
	FactsToEvolve []factEvolutionLLM `json:"facts_to_evolve"`
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
	FactIDs  []string `json:"fact_ids"`
	NeedMore bool     `json:"need_more"`
	// Missing is a short noun phrase (1-5 words) naming what the answerer would
	// still need to write a complete answer. Set only when NeedMore is true.
	Missing string `json:"missing,omitempty"`
}

func (o *selectFactsOutput) SchemaDescription() string {
	return "IDs of the candidate facts that are relevant to the query (most-relevant first), plus need_more=true with a short missing-piece noun phrase if the selected facts are insufficient to answer."
}

func (o *selectFactsOutput) Validate() error {
	return nil
}

func (o *selectFactsOutput) Unmarshal(data []byte) error {
	return json.Unmarshal(data, o)
}

