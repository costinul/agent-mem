package engine

import (
	"context"
	"fmt"
	"log"
	"slices"
	"sort"
	"strings"
	"time"

	models "agentmem/internal/models"
	"agentmem/internal/repository/memoryrepo"
)

// rrfK is the reciprocal-rank-fusion smoothing constant. 60 is the canonical default
// (Cormack, Clarke, Buettcher 2009): small enough to keep per-list rank-1 dominant,
// large enough that rank-K and rank-K+1 don't differ wildly.
const rrfK = 60

// Per-channel weights applied to each ranked list's RRF contribution. Dense is the
// most semantically faithful signal for natural-language queries, so it carries full
// weight. Lexical (tsvector) is a corrective signal — strong when the query and the
// stored fact share rare tokens, silent otherwise — and gets half weight. Entity
// overlap is treated as a boost rather than a primary channel: when a query names
// two co-occurring people, every interaction fact between them scores 1.0 in the
// entity SQL and would otherwise flood the candidate pool with off-topic mentions
// at the expense of unilateral facts that may carry the actual answer.
const (
	weightDense   = 1.0
	weightLexical = 0.5
	weightEntity  = 0.2
)

// rankedList is a single channel's ranked output plus the weight RRF should apply
// to its contribution. Weight 0 is treated as 1.0 for backward compatibility.
type rankedList struct {
	IDs    []string
	Weight float64
}

// channelRanks holds, for one ranked list, the rank (0-based) at which each fact ID
// appears. -1 means the fact was not in the list.
type channelRanks map[string]int

// hybridRetrievalResult carries everything Recall needs from the hybrid retrieve step:
// the fact pool, the fused score per fact, and the per-channel ranks for debug output.
type hybridRetrievalResult struct {
	Facts       []models.Fact
	FusedScores map[string]float64
	DenseRank   channelRanks
	LexicalRank channelRanks
	EntityRank  channelRanks
	DenseScores map[string]float64 // best dense score across all phrases × scopes; used as a tiebreaker.
}

// retrieveFactsHybrid runs three retrieval channels in parallel — dense (embedding),
// lexical (text search), entity (entity overlap) — and fuses them via Reciprocal Rank
// Fusion. Each phrase generates its own dense + lexical ranked list (after merging
// thread/agent/account scopes by max score). Entities run as a single combined list.
// All ranked lists are fed into RRF and the global top-`limit` facts are returned.
//
// This path is wired only into Recall. RecallLight, RecallZero, and the ingest
// retrieve continue to use the dense-only retrieveFactsWithLimit so the change is
// A/B-comparable on the eval.
func (e *MemoryEngine) retrieveFactsHybrid(
	ctx context.Context,
	accountID, agentID, threadID string,
	phrases []string,
	embeddings [][]float64,
	entities []string,
	limit int,
	includeSuperseded bool,
	maxSourceEventDate *time.Time,
) (*hybridRetrievalResult, error) {
	if limit <= 0 {
		limit = 10
	}
	aid := ptrString(agentID)
	tid := ptrString(threadID)

	// Cap each per-phrase per-channel list at perChannelLimit before fusion. RRF only
	// cares about ranks, so capping at ~2× the final limit keeps the fusion stable
	// while bounding DB work.
	perChannelLimit := limit * 2
	if perChannelLimit < 50 {
		perChannelLimit = 50
	}

	// Collected ranked lists: one entry per channel call (dense per phrase,
	// lexical per phrase, entity once), each carrying its RRF weight.
	var ranked []rankedList

	// Per-channel rank registries (best rank seen across all lists in that channel).
	denseRank := channelRanks{}
	lexicalRank := channelRanks{}
	entityRank := channelRanks{}

	// Cache of facts seen so we can return full models.Fact at the end. Higher-score
	// observation wins (so we keep the dense-scored copy with embedding when present).
	factCache := map[string]models.Fact{}
	denseScores := map[string]float64{}
	rememberFact := func(f models.Fact) {
		if existing, ok := factCache[f.ID]; ok {
			// Prefer the variant carrying an embedding so cosine tiebreakers still work.
			if len(existing.Embedding) > 0 || len(f.Embedding) == 0 {
				return
			}
		}
		factCache[f.ID] = f
	}
	updateChannelRank := func(reg channelRanks, id string, rank int) {
		if existing, ok := reg[id]; !ok || rank < existing {
			reg[id] = rank
		}
	}
	collectIDs := func(rs []memoryrepo.FactWithScore) []string {
		ids := make([]string, 0, len(rs))
		for _, fs := range rs {
			ids = append(ids, fs.ID)
			rememberFact(fs.Fact)
		}
		return ids
	}

	// Dense + lexical: per phrase, scope-merged ranked list.
	for i, phrase := range phrases {
		var emb []float64
		if i < len(embeddings) {
			emb = embeddings[i]
		}

		if len(emb) > 0 {
			denseList, err := e.searchDenseAcrossScopes(ctx, accountID, aid, tid, emb, perChannelLimit, includeSuperseded, maxSourceEventDate)
			if err != nil {
				return nil, fmt.Errorf("dense channel for phrase %q: %w", phrase, err)
			}
			for _, fs := range denseList {
				if fs.Score > denseScores[fs.ID] {
					denseScores[fs.ID] = fs.Score
				}
			}
			ids := collectIDs(denseList)
			for r, id := range ids {
				updateChannelRank(denseRank, id, r)
			}
			ranked = append(ranked, rankedList{IDs: ids, Weight: weightDense})
		}

		if phrase != "" {
			lexList, err := e.searchLexicalAcrossScopes(ctx, accountID, aid, tid, toLexicalORQuery(phrase), perChannelLimit, includeSuperseded, maxSourceEventDate)
			if err != nil {
				return nil, fmt.Errorf("lexical channel for phrase %q: %w", phrase, err)
			}
			ids := collectIDs(lexList)
			for r, id := range ids {
				updateChannelRank(lexicalRank, id, r)
			}
			ranked = append(ranked, rankedList{IDs: ids, Weight: weightLexical})
		}
	}

	// Entity channel: one combined list across scopes, queried once with all entities.
	if len(entities) > 0 {
		entList, err := e.searchEntitiesAcrossScopes(ctx, accountID, aid, tid, entities, perChannelLimit, includeSuperseded, maxSourceEventDate)
		if err != nil {
			return nil, fmt.Errorf("entity channel: %w", err)
		}
		ids := collectIDs(entList)
		for r, id := range ids {
			updateChannelRank(entityRank, id, r)
		}
		ranked = append(ranked, rankedList{IDs: ids, Weight: weightEntity})
	}

	if len(ranked) == 0 {
		return &hybridRetrievalResult{
			DenseRank:   denseRank,
			LexicalRank: lexicalRank,
			EntityRank:  entityRank,
			FusedScores: map[string]float64{},
			DenseScores: denseScores,
		}, nil
	}

	fused := rrfFuse(ranked, rrfK)

	type rankedFact struct {
		fact  models.Fact
		score float64
	}
	all := make([]rankedFact, 0, len(fused))
	for id, score := range fused {
		f, ok := factCache[id]
		if !ok {
			continue
		}
		all = append(all, rankedFact{fact: f, score: score})
	}
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].score != all[j].score {
			return all[i].score > all[j].score
		}
		// Primary tiebreaker: best dense score so cosine-strong matches outrank ties.
		di, dj := denseScores[all[i].fact.ID], denseScores[all[j].fact.ID]
		if di != dj {
			return di > dj
		}
		// Final tiebreaker: fact ID. Without this, the upstream map iteration
		// (rrfFuse / rankedSlice) randomizes the order of ties between runs and
		// the LLM selector reads that random order as a relevance signal.
		return all[i].fact.ID < all[j].fact.ID
	})

	for i, r := range all {
		if r.fact.ID == "e556c7c0-c42b-45da-a790-58f943cdfe0b" {
			fmt.Printf("fact found at position %d", i)
		}
	}

	if len(all) > limit {
		all = all[:limit]
	}

	facts := make([]models.Fact, len(all))
	for i, rf := range all {
		facts[i] = rf.fact
	}

	if hasFactID(facts, "e556c7c0-c42b-45da-a790-58f943cdfe0b") {
		fmt.Println("has the fact ")
	} else {
		fmt.Println("doesn't have fact")
	}

	log.Printf("recall hybrid lists=%d pool=%d top=%d (dense=%d lexical=%d entity=%d)",
		len(ranked), len(all), len(facts), len(denseRank), len(lexicalRank), len(entityRank))

	return &hybridRetrievalResult{
		Facts:       facts,
		FusedScores: fused,
		DenseRank:   denseRank,
		LexicalRank: lexicalRank,
		EntityRank:  entityRank,
		DenseScores: denseScores,
	}, nil
}

// searchDenseAcrossScopes runs the dense embedding search across (thread, agent,
// account) scopes and returns a single rank-ordered list (max score wins per fact).
func (e *MemoryEngine) searchDenseAcrossScopes(
	ctx context.Context,
	accountID string, agentID, threadID *string,
	embedding []float64,
	limit int,
	includeSuperseded bool,
	maxSourceEventDate *time.Time,
) ([]memoryrepo.FactWithScore, error) {
	merged := map[string]memoryrepo.FactWithScore{}
	params := memoryrepo.SearchByEmbeddingParams{
		AccountID:          accountID,
		Embedding:          embedding,
		Limit:              limit,
		IncludeSuperseded:  includeSuperseded,
		MaxSourceEventDate: maxSourceEventDate,
	}
	for _, scope := range scopeChain(agentID, threadID) {
		params.AgentID = scope.AgentID
		params.ThreadID = scope.ThreadID
		rs, err := e.repo.SearchFactsByEmbeddingWithScores(ctx, params)
		if err != nil {
			return nil, err
		}
		for _, r := range rs {
			if r.Fact.ID == "" {
				continue
			}
			if existing, ok := merged[r.Fact.ID]; !ok || r.Score > existing.Score {
				merged[r.Fact.ID] = r
			}
		}
	}
	return rankedSlice(merged), nil
}

// searchLexicalAcrossScopes runs SearchFactsByText across scopes and returns a
// single rank-ordered list (max score wins per fact).
func (e *MemoryEngine) searchLexicalAcrossScopes(
	ctx context.Context,
	accountID string, agentID, threadID *string,
	query string,
	limit int,
	includeSuperseded bool,
	maxSourceEventDate *time.Time,
) ([]memoryrepo.FactWithScore, error) {
	merged := map[string]memoryrepo.FactWithScore{}
	for _, scope := range scopeChain(agentID, threadID) {
		rs, err := e.repo.SearchFactsByText(ctx, memoryrepo.SearchByTextParams{
			AccountID:          accountID,
			AgentID:            scope.AgentID,
			ThreadID:           scope.ThreadID,
			Query:              query,
			Limit:              limit,
			IncludeSuperseded:  includeSuperseded,
			MaxSourceEventDate: maxSourceEventDate,
		})
		if err != nil {
			return nil, err
		}
		for _, r := range rs {
			if existing, ok := merged[r.Fact.ID]; !ok || r.Score > existing.Score {
				merged[r.Fact.ID] = r
			}
		}
	}
	return rankedSlice(merged), nil
}

// searchEntitiesAcrossScopes runs SearchFactsByEntities across scopes and returns
// a single rank-ordered list (max score wins per fact).
func (e *MemoryEngine) searchEntitiesAcrossScopes(
	ctx context.Context,
	accountID string, agentID, threadID *string,
	entities []string,
	limit int,
	includeSuperseded bool,
	maxSourceEventDate *time.Time,
) ([]memoryrepo.FactWithScore, error) {
	merged := map[string]memoryrepo.FactWithScore{}
	for _, scope := range scopeChain(agentID, threadID) {
		rs, err := e.repo.SearchFactsByEntities(ctx, memoryrepo.SearchByEntitiesParams{
			AccountID:          accountID,
			AgentID:            scope.AgentID,
			ThreadID:           scope.ThreadID,
			Entities:           entities,
			Limit:              limit,
			IncludeSuperseded:  includeSuperseded,
			MaxSourceEventDate: maxSourceEventDate,
		})
		if err != nil {
			return nil, err
		}
		for _, r := range rs {
			if existing, ok := merged[r.Fact.ID]; !ok || r.Score > existing.Score {
				merged[r.Fact.ID] = r
			}
		}
	}
	return rankedSlice(merged), nil
}

type scopeFilter struct {
	AgentID  *string
	ThreadID *string
}

// scopeChain returns the (thread, agent, account) scope filter triple. A nil pointer
// in either field means "match all". This mirrors the merge order used by the
// original retrieveFactsWithLimit.
func scopeChain(agentID, threadID *string) []scopeFilter {
	return []scopeFilter{
		{AgentID: agentID, ThreadID: threadID},
		{AgentID: agentID, ThreadID: nil},
		{AgentID: nil, ThreadID: nil},
	}
}

func rankedSlice(merged map[string]memoryrepo.FactWithScore) []memoryrepo.FactWithScore {
	out := make([]memoryrepo.FactWithScore, 0, len(merged))
	for _, fs := range merged {
		out = append(out, fs)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Fact.ID < out[j].Fact.ID
	})
	return out
}

// rrfFuse runs weighted Reciprocal Rank Fusion over a set of ranked ID lists.
// Score for fact f is Σ over lists L of  weight_L * 1 / (k + rank_L(f) + 1).
// Facts not present in a list contribute 0 from that list. A weight of 0 is
// promoted to 1.0 so callers that don't care about per-channel weighting still get
// classic RRF behavior.
func rrfFuse(lists []rankedList, k int) map[string]float64 {
	out := map[string]float64{}
	for _, list := range lists {
		w := list.Weight
		if w == 0 {
			w = 1.0
		}
		for i, id := range list.IDs {
			out[id] += w / float64(k+i+1)
		}
	}
	return out
}

// toLexicalORQuery rewrites a natural-language phrase into a websearch_to_tsquery
// expression that uses OR between tokens instead of the implicit AND.
//
// websearch_to_tsquery defaults to AND-joining every token. For conversational
// recall queries like "Melanie read a book suggested by Caroline", AND semantics
// almost guarantee zero matches: the answer fact ("Caroline loved the book
// 'Becoming Nicole'") doesn't contain "Melanie", "read", or "suggested", so it
// fails the AND test even though it shares the most informative tokens (Caroline,
// book). Switching to OR turns the lexical channel into a soft signal — every
// matched fact contributes, ranked by ts_rank_cd which already weights rare tokens
// higher. The lower channel weight in RRF compensates for the noisier matches.
func toLexicalORQuery(phrase string) string {
	fields := strings.Fields(phrase)
	if len(fields) <= 1 {
		return phrase
	}
	return strings.Join(fields, " OR ")
}

// rrfPlaceSiblings returns candidates re-ordered such that:
//   - Facts with a fused RRF score sort by that score (desc), ties broken by best
//     dense-cosine score (so semantically-strong matches win ties).
//   - Sibling facts injected by expandBySource (no RRF score) are placed at the end,
//     ordered by their max cosine against the query embeddings.
//
// This replaces cosineRerank for the recall path: a lexical-only or entity-only match
// must not be demoted just because its dense similarity is low.
func rrfPlaceSiblings(candidates []models.Fact, fused map[string]float64, denseScores map[string]float64, queryEmbeddings [][]float64) []models.Fact {
	if len(candidates) == 0 {
		return candidates
	}
	type scored struct {
		fact      models.Fact
		fused     float64
		hasFused  bool
		cosine    float64
		denseBest float64
	}
	ss := make([]scored, len(candidates))
	for i, f := range candidates {
		s := scored{fact: f}
		if v, ok := fused[f.ID]; ok {
			s.fused = v
			s.hasFused = true
		}
		s.denseBest = denseScores[f.ID]
		s.cosine = maxCosine(f.Embedding, queryEmbeddings)
		ss[i] = s
	}
	sort.SliceStable(ss, func(i, j int) bool {
		if ss[i].hasFused != ss[j].hasFused {
			return ss[i].hasFused
		}
		if ss[i].hasFused {
			if ss[i].fused != ss[j].fused {
				return ss[i].fused > ss[j].fused
			}
			if ss[i].denseBest != ss[j].denseBest {
				return ss[i].denseBest > ss[j].denseBest
			}
			return ss[i].fact.ID < ss[j].fact.ID
		}
		if ss[i].cosine != ss[j].cosine {
			return ss[i].cosine > ss[j].cosine
		}
		return ss[i].fact.ID < ss[j].fact.ID
	})
	out := make([]models.Fact, len(ss))
	for i, s := range ss {
		out[i] = s.fact
	}
	return out
}

func hasFactID(facts []models.Fact, id string) bool {
	return slices.ContainsFunc(facts, func(f models.Fact) bool {
		return f.ID == id
	})
}
