package engine

import (
	"context"
	"fmt"
	"strings"
	"time"

	"agentmem/internal/errs"
	models "agentmem/internal/models"
	"agentmem/internal/repository/memoryrepo"
)

func (e *MemoryEngine) ListFactsForAccount(ctx context.Context, accountID string, params memoryrepo.ListFactsParams) (models.FactListOutput, error) {
	params.AccountID = accountID
	facts, total, err := e.repo.ListFactsFiltered(ctx, params)
	if err != nil {
		return models.FactListOutput{}, fmt.Errorf("list facts: %w", err)
	}

	returned := make([]models.ReturnedFact, 0, len(facts))
	for _, fact := range facts {
		mapped, err := e.mapFactForOutput(ctx, fact, false)
		if err != nil {
			return models.FactListOutput{}, err
		}
		returned = append(returned, mapped)
	}

	return models.FactListOutput{
		Facts:  returned,
		Total:  total,
		Limit:  params.Limit,
		Offset: params.Offset,
	}, nil
}

func (e *MemoryEngine) GetFact(ctx context.Context, factID string, includeSources bool) (models.ReturnedFact, error) {
	return e.GetFactForAccount(ctx, "", factID, includeSources)
}

func (e *MemoryEngine) GetFactForAccount(ctx context.Context, accountID, factID string, includeSources bool) (models.ReturnedFact, error) {
	fact, err := e.repo.GetFactByID(ctx, factID)
	if err != nil {
		return models.ReturnedFact{}, fmt.Errorf("get fact: %w", err)
	}
	if fact == nil {
		return models.ReturnedFact{}, errs.NewNotFound("fact not found")
	}
	if strings.TrimSpace(accountID) != "" && fact.AccountID != strings.TrimSpace(accountID) {
		return models.ReturnedFact{}, errs.NewNotFound("fact not found")
	}
	return e.mapFactForOutput(ctx, *fact, includeSources)
}

func (e *MemoryEngine) UpdateFact(ctx context.Context, factID string, text string, source models.SourceKind) (models.ReturnedFact, error) {
	return e.UpdateFactForAccount(ctx, "", factID, text, source)
}

func (e *MemoryEngine) UpdateFactForAccount(ctx context.Context, accountID, factID string, text string, source models.SourceKind) (models.ReturnedFact, error) {
	if strings.TrimSpace(text) == "" {
		return models.ReturnedFact{}, errs.NewValidation("text is required")
	}
	fact, err := e.repo.GetFactByID(ctx, factID)
	if err != nil {
		return models.ReturnedFact{}, fmt.Errorf("get fact: %w", err)
	}
	if fact == nil {
		return models.ReturnedFact{}, errs.NewNotFound("fact not found")
	}
	if strings.TrimSpace(accountID) != "" && fact.AccountID != strings.TrimSpace(accountID) {
		return models.ReturnedFact{}, errs.NewNotFound("fact not found")
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

func (e *MemoryEngine) DeleteFactForAccount(ctx context.Context, accountID, factID string) error {
	fact, err := e.repo.GetFactByID(ctx, factID)
	if err != nil {
		return fmt.Errorf("get fact: %w", err)
	}
	if fact == nil {
		return errs.NewNotFound("fact not found")
	}
	if strings.TrimSpace(accountID) != "" && fact.AccountID != strings.TrimSpace(accountID) {
		return errs.NewNotFound("fact not found")
	}
	return e.repo.DeleteFact(ctx, factID)
}

// buildRecallOutput maps a list of selected facts to ReturnedFact, deduplicating by ID (or text when ID is absent).
// Pass a non-nil dbg to attach debug information to the response (only when the API key has debug=true).
func (e *MemoryEngine) buildRecallOutput(ctx context.Context, input models.RecallInput, facts []models.Fact, dbg *models.RecallDebug) (models.RecallOutput, error) {
	output := models.RecallOutput{
		Facts: make([]models.ReturnedFact, 0, len(facts)),
		Debug: dbg,
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
			return models.RecallOutput{}, err
		}
		output.Facts = append(output.Facts, mapped)
	}

	return output, nil
}

// mapFactForOutput converts a stored Fact to the API-facing ReturnedFact,
// optionally loading the original source content.
func (e *MemoryEngine) mapFactForOutput(ctx context.Context, fact models.Fact, includeSource bool) (models.ReturnedFact, error) {
	source, err := e.repo.GetSourceByID(ctx, fact.SourceID)
	if err != nil {
		return models.ReturnedFact{}, fmt.Errorf("load source for fact %s: %w", fact.ID, err)
	}
	text := fact.Text
	if fact.ReferencedAt != nil && !factTextContainsDate(text, *fact.ReferencedAt) {
		text = text + " (on " + fact.ReferencedAt.Format("2006-01-02") + ")"
	}
	returned := models.ReturnedFact{
		ID:   fact.ID,
		Text: text,
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

// factTextContainsDate returns true if the fact text already contains the ISO date string
// (YYYY-MM-DD) or the 4-digit year of the given time, so we don't double-annotate.
func factTextContainsDate(text string, t time.Time) bool {
	iso := t.Format("2006-01-02")
	if strings.Contains(text, iso) {
		return true
	}
	year := t.Format("2006")
	return strings.Contains(text, year)
}

// ensureSourceCanMutateFact enforces the trust hierarchy: the calling source kind must have
// equal or higher trust than the source that originally created the fact, and system facts are immutable.
func (e *MemoryEngine) ensureSourceCanMutateFact(ctx context.Context, source models.SourceKind, fact models.Fact) error {
	targetSource, err := e.repo.GetSourceByID(ctx, fact.SourceID)
	if err != nil {
		return fmt.Errorf("load fact source: %w", err)
	}
	if targetSource == nil {
		return errs.NewNotFound("fact source not found")
	}
	if targetSource.Kind == models.SOURCE_SYSTEM {
		return errs.NewValidation("system facts are immutable")
	}

	callerTrust, ok := models.SourceTrustHierarchy[source]
	if !ok {
		return errs.NewValidation("invalid source kind: %s", source)
	}
	targetTrust := models.SourceTrustHierarchy[targetSource.Kind]
	if callerTrust < targetTrust {
		return errs.NewValidation("source %s is not allowed to mutate fact from %s", source, targetSource.Kind)
	}
	return nil
}
