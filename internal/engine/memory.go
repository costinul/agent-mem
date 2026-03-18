package engine

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	models "agentmem/internal/models"
	"agentmem/internal/repository/memoryrepo"

	"github.com/costinul/bwai/bwaiclient"
)

type MemoryEngine struct {
	repo memoryrepo.Repository
	ai   *LLMAdapter
}

func NewMemoryEngine(client *bwaiclient.BWAIClient, repo memoryrepo.Repository, schemaModel, embeddingModel string) *MemoryEngine {
	return &MemoryEngine{
		repo: repo,
		ai:   NewLLMAdapter(client, schemaModel, embeddingModel),
	}
}

func (e *MemoryEngine) ProcessContextual(ctx context.Context, input models.MemoryInput) (models.MemoryOutput, error) {
	if err := validateContextualInput(input); err != nil {
		return models.MemoryOutput{}, err
	}

	log.Printf("contextual pipeline start account=%s agent=%s session=%s inputs=%d", input.AccountID, input.AgentID, input.SessionID, len(input.Inputs))
	sessionID := input.SessionID
	event, err := e.repo.InsertEvent(ctx, models.Event{
		AccountID: input.AccountID,
		AgentID:   input.AgentID,
		SessionID: &sessionID,
	})
	if err != nil {
		return models.MemoryOutput{}, fmt.Errorf("insert event: %w", err)
	}

	storedSources, decompositions, err := e.persistAndDecomposeSources(ctx, event.ID, input.SessionID, input.Inputs)
	if err != nil {
		return models.MemoryOutput{}, err
	}

	queryEmbeddings, err := e.buildSearchEmbeddings(ctx, decompositions)
	if err != nil {
		return models.MemoryOutput{}, err
	}

	retrieved, err := e.retrieveFacts(ctx, input.AccountID, input.AgentID, input.SessionID, queryEmbeddings)
	if err != nil {
		return models.MemoryOutput{}, err
	}

	evalInput := flattenExtractedFacts(decompositions)
	evalResult, err := e.ai.Evaluate(ctx, EvaluateRequest{
		NewFacts:       evalInput,
		RetrievedFacts: retrieved,
	})
	if err != nil {
		return models.MemoryOutput{}, fmt.Errorf("evaluate facts: %w", err)
	}

	storedFacts, err := e.applyEvaluateResult(ctx, input, storedSources, evalInput, evalResult)
	if err != nil {
		return models.MemoryOutput{}, err
	}

	output, err := e.buildOutput(ctx, input, append(evalResult.FactsToReturn, storedFacts...))
	if err != nil {
		return models.MemoryOutput{}, err
	}
	log.Printf("contextual pipeline completed event=%s returned_facts=%d", event.ID, len(output.Facts))
	return output, nil
}

func (e *MemoryEngine) AddFactual(ctx context.Context, input models.FactualInput) (models.MemoryOutput, error) {
	if strings.TrimSpace(input.AccountID) == "" || strings.TrimSpace(input.AgentID) == "" {
		return models.MemoryOutput{}, errors.New("account_id and agent_id are required")
	}
	if len(input.Inputs) == 0 {
		return models.MemoryOutput{}, errors.New("inputs are required")
	}
	for idx, item := range input.Inputs {
		if strings.TrimSpace(item.Content) == "" {
			return models.MemoryOutput{}, fmt.Errorf("inputs[%d].content is required", idx)
		}
		if _, ok := models.SourceTrustHierarchy[item.Kind]; !ok {
			return models.MemoryOutput{}, fmt.Errorf("inputs[%d].kind is invalid", idx)
		}
	}

	var sessionID *string
	if strings.TrimSpace(input.SessionID) != "" {
		s := strings.TrimSpace(input.SessionID)
		sessionID = &s
	}
	event, err := e.repo.InsertEvent(ctx, models.Event{
		AccountID: input.AccountID,
		AgentID:   input.AgentID,
		SessionID: sessionID,
	})
	if err != nil {
		return models.MemoryOutput{}, fmt.Errorf("insert event: %w", err)
	}

	storedSources, decompositions, err := e.persistAndDecomposeSources(ctx, event.ID, strings.TrimSpace(input.SessionID), input.Inputs)
	if err != nil {
		return models.MemoryOutput{}, err
	}

	extracted := flattenExtractedFacts(decompositions)
	texts := make([]string, 0, len(extracted))
	for _, fact := range extracted {
		texts = append(texts, fact.Text)
	}
	embeddings, err := e.ai.Embed(ctx, texts)
	if err != nil {
		return models.MemoryOutput{}, fmt.Errorf("embed factual facts: %w", err)
	}

	stored := make([]models.Fact, 0, len(extracted))
	for idx, extractedFact := range extracted {
		sourceID := selectSourceIDForExtractedFact(storedSources, idx)
		fact := models.Fact{
			AccountID: input.AccountID,
			AgentID:   ptrString(input.AgentID),
			SessionID: sessionID,
			SourceID:  sourceID,
			Kind:      extractedFact.Kind,
			Text:      extractedFact.Text,
			Embedding: embeddings[idx],
		}
		inserted, err := e.repo.InsertFact(ctx, fact)
		if err != nil {
			return models.MemoryOutput{}, fmt.Errorf("insert factual fact: %w", err)
		}
		stored = append(stored, *inserted)
	}

	return e.buildOutput(ctx, models.MemoryInput{
		IncludeSources: true,
	}, stored)
}

func (e *MemoryEngine) GetFact(ctx context.Context, factID string, includeSources bool) (models.ReturnedFact, error) {
	fact, err := e.repo.GetFactByID(ctx, factID)
	if err != nil {
		return models.ReturnedFact{}, fmt.Errorf("get fact: %w", err)
	}
	if fact == nil {
		return models.ReturnedFact{}, errors.New("fact not found")
	}
	return e.mapFactForOutput(ctx, *fact, includeSources)
}

func (e *MemoryEngine) UpdateFact(ctx context.Context, factID string, text string, source models.SourceKind) (models.ReturnedFact, error) {
	if strings.TrimSpace(text) == "" {
		return models.ReturnedFact{}, errors.New("text is required")
	}
	fact, err := e.repo.GetFactByID(ctx, factID)
	if err != nil {
		return models.ReturnedFact{}, fmt.Errorf("get fact: %w", err)
	}
	if fact == nil {
		return models.ReturnedFact{}, errors.New("fact not found")
	}

	if err := e.ensureSourceCanMutateFact(ctx, source, *fact); err != nil {
		return models.ReturnedFact{}, err
	}
	embedding, err := e.ai.Embed(ctx, []string{text})
	if err != nil {
		return models.ReturnedFact{}, fmt.Errorf("embed updated fact: %w", err)
	}
	fact.Text = strings.TrimSpace(text)
	fact.Embedding = embedding[0]
	if err := e.repo.UpdateFact(ctx, *fact); err != nil {
		return models.ReturnedFact{}, fmt.Errorf("update fact: %w", err)
	}
	return e.mapFactForOutput(ctx, *fact, false)
}

func (e *MemoryEngine) DeleteFacts(ctx context.Context, factIDs []string, source models.SourceKind) error {
	if len(factIDs) == 0 {
		return errors.New("fact_ids are required")
	}
	for _, factID := range factIDs {
		fact, err := e.repo.GetFactByID(ctx, factID)
		if err != nil {
			return fmt.Errorf("get fact %s: %w", factID, err)
		}
		if fact == nil {
			continue
		}
		if err := e.ensureSourceCanMutateFact(ctx, source, *fact); err != nil {
			return err
		}
	}
	return e.repo.DeleteFacts(ctx, factIDs)
}

func (e *MemoryEngine) persistAndDecomposeSources(ctx context.Context, eventID, sessionID string, inputs []models.InputItem) ([]models.Source, []models.Decomposition, error) {
	storedSources := make([]models.Source, 0, len(inputs))
	contextHeader := buildEventContextHeader(inputs)
	decompositions := make([]models.Decomposition, 0, len(inputs))

	// Load recent conversation history once for USER/AGENT sources.
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
			Metadata:    item.Metadata,
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

func (e *MemoryEngine) buildSearchEmbeddings(ctx context.Context, decompositions []models.Decomposition) ([][]float64, error) {
	queries := make([]string, 0)
	for _, decomposition := range decompositions {
		for _, fact := range decomposition.Facts {
			queries = append(queries, fact.Text)
		}
		for _, query := range decomposition.Queries {
			queries = append(queries, query.Text)
		}
	}
	if len(queries) == 0 {
		return nil, nil
	}
	embeddings, err := e.ai.Embed(ctx, queries)
	if err != nil {
		return nil, fmt.Errorf("embed search queries: %w", err)
	}
	return embeddings, nil
}

func (e *MemoryEngine) retrieveFacts(ctx context.Context, accountID, agentID, sessionID string, embeddings [][]float64) ([]models.Fact, error) {
	aid := ptrString(agentID)
	sid := ptrString(sessionID)
	seen := map[string]struct{}{}
	facts := make([]models.Fact, 0)

	for _, emb := range embeddings {
		// Session scope
		sessionFacts, err := e.repo.SearchFactsByEmbedding(ctx, memoryrepo.SearchByEmbeddingParams{
			AccountID: accountID,
			AgentID:   aid,
			SessionID: sid,
			Embedding: emb,
			Limit:     10,
		})
		if err != nil {
			return nil, fmt.Errorf("search session facts: %w", err)
		}
		// Agent scope
		agentFacts, err := e.repo.SearchFactsByEmbedding(ctx, memoryrepo.SearchByEmbeddingParams{
			AccountID: accountID,
			AgentID:   aid,
			Embedding: emb,
			Limit:     10,
		})
		if err != nil {
			return nil, fmt.Errorf("search agent facts: %w", err)
		}
		// Account scope
		accountFacts, err := e.repo.SearchFactsByEmbedding(ctx, memoryrepo.SearchByEmbeddingParams{
			AccountID: accountID,
			Embedding: emb,
			Limit:     10,
		})
		if err != nil {
			return nil, fmt.Errorf("search account facts: %w", err)
		}
		facts = appendUniqueFacts(facts, seen, sessionFacts...)
		facts = appendUniqueFacts(facts, seen, agentFacts...)
		facts = appendUniqueFacts(facts, seen, accountFacts...)
	}
	return facts, nil
}

func (e *MemoryEngine) applyEvaluateResult(
	ctx context.Context,
	input models.MemoryInput,
	storedSources []models.Source,
	newFacts []models.ExtractedFact,
	result models.EvaluateResult,
) ([]models.Fact, error) {
	newTexts := make([]string, 0, len(result.FactsToStore))
	for _, fact := range result.FactsToStore {
		newTexts = append(newTexts, fact.Text)
	}
	embeddings, err := e.ai.Embed(ctx, newTexts)
	if err != nil {
		return nil, fmt.Errorf("embed new facts: %w", err)
	}

	stored := make([]models.Fact, 0, len(result.FactsToStore))
	for idx, fact := range result.FactsToStore {
		sourceID := selectSourceIDForExtractedFact(storedSources, idx)
		newFact := models.Fact{
			AccountID: input.AccountID,
			AgentID:   ptrString(input.AgentID),
			SessionID: ptrString(input.SessionID),
			SourceID:  sourceID,
			Kind:      fact.Kind,
			Text:      fact.Text,
		}
		if idx < len(embeddings) {
			newFact.Embedding = embeddings[idx]
		}
		inserted, err := e.repo.InsertFact(ctx, newFact)
		if err != nil {
			return nil, fmt.Errorf("insert contextual fact: %w", err)
		}
		stored = append(stored, *inserted)
	}

	for _, fact := range result.FactsToUpdate {
		if err := e.repo.UpdateFact(ctx, fact); err != nil {
			return nil, fmt.Errorf("update contextual fact: %w", err)
		}
	}
	if err := e.repo.DeleteFacts(ctx, result.FactsToDelete); err != nil {
		return nil, fmt.Errorf("delete contextual facts: %w", err)
	}
	_ = newFacts
	return stored, nil
}

func (e *MemoryEngine) buildOutput(ctx context.Context, input models.MemoryInput, facts []models.Fact) (models.MemoryOutput, error) {
	output := models.MemoryOutput{
		Facts: make([]models.ReturnedFact, 0, len(facts)),
	}
	seen := map[string]struct{}{}
	for _, fact := range facts {
		key := fact.ID
		if key == "" {
			key = strings.ToLower(strings.TrimSpace(fmt.Sprintf("%s|%s", fact.Kind, fact.Text)))
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		mapped, err := e.mapFactForOutput(ctx, fact, input.IncludeSources)
		if err != nil {
			return models.MemoryOutput{}, err
		}
		output.Facts = append(output.Facts, mapped)
	}

	if input.MessageHistory > 0 && strings.TrimSpace(input.SessionID) != "" {
		sources, err := e.repo.ListConversationSourcesBySessionID(ctx, input.SessionID, input.MessageHistory)
		if err != nil {
			return models.MemoryOutput{}, fmt.Errorf("list conversation sources: %w", err)
		}
		output.Messages = make([]models.ConversationMessage, 0, len(sources))
		for _, source := range sources {
			content := ""
			if source.Content != nil {
				content = *source.Content
			}
			output.Messages = append(output.Messages, models.ConversationMessage{
				SourceID:  source.ID,
				EventID:   source.EventID,
				SessionID: input.SessionID,
				Kind:      source.Kind,
				Content:   content,
				CreatedAt: source.CreatedAt,
			})
		}
	}
	return output, nil
}

func (e *MemoryEngine) mapFactForOutput(ctx context.Context, fact models.Fact, includeSource bool) (models.ReturnedFact, error) {
	source, err := e.repo.GetSourceByID(ctx, fact.SourceID)
	if err != nil {
		return models.ReturnedFact{}, fmt.Errorf("load source for fact %s: %w", fact.ID, err)
	}
	returned := models.ReturnedFact{
		ID:   fact.ID,
		Text: fact.Text,
		Kind: fact.Kind,
	}
	if source != nil {
		returned.SourceKind = source.Kind
		if includeSource && source.Content != nil {
			content := *source.Content
			returned.OriginalSource = &content
		}
	}
	return returned, nil
}

func (e *MemoryEngine) ensureSourceCanMutateFact(ctx context.Context, source models.SourceKind, fact models.Fact) error {
	targetSource, err := e.repo.GetSourceByID(ctx, fact.SourceID)
	if err != nil {
		return fmt.Errorf("load fact source: %w", err)
	}
	if targetSource == nil {
		return errors.New("fact source not found")
	}
	if targetSource.Kind == models.SOURCE_SYSTEM {
		return errors.New("system facts are immutable")
	}

	callerTrust, ok := models.SourceTrustHierarchy[source]
	if !ok {
		return fmt.Errorf("invalid source kind: %s", source)
	}
	targetTrust := models.SourceTrustHierarchy[targetSource.Kind]
	if callerTrust < targetTrust {
		return fmt.Errorf("source %s is not allowed to mutate fact from %s", source, targetSource.Kind)
	}
	return nil
}

func validateContextualInput(input models.MemoryInput) error {
	if strings.TrimSpace(input.AccountID) == "" {
		return errors.New("account_id is required")
	}
	if strings.TrimSpace(input.AgentID) == "" {
		return errors.New("agent_id is required")
	}
	if strings.TrimSpace(input.SessionID) == "" {
		return errors.New("session_id is required")
	}
	if len(input.Inputs) == 0 {
		return errors.New("inputs are required")
	}
	for idx, item := range input.Inputs {
		if strings.TrimSpace(item.Content) == "" {
			return fmt.Errorf("inputs[%d].content is required", idx)
		}
		if _, ok := models.SourceTrustHierarchy[item.Kind]; !ok {
			return fmt.Errorf("inputs[%d].kind is invalid", idx)
		}
	}
	return nil
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

func appendUniqueFacts(target []models.Fact, seen map[string]struct{}, facts ...models.Fact) []models.Fact {
	for _, fact := range facts {
		if fact.ID != "" {
			if _, ok := seen[fact.ID]; ok {
				continue
			}
			seen[fact.ID] = struct{}{}
		}
		target = append(target, fact)
	}
	return target
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
