# Engine

Notation: **[LLM]** = LLM call, **[EMB]** = embedding call, **[VEC]** = vector search call, **[DB]** = plain DB write/read.

## Ingestion (`Add`)

1. Validate input.
2. Enforce monotonic `event_date` per thread (reject backdated events). **[DB]**
3. Insert `Event` row. **[DB]**
4. For each input: insert `Source` **[DB]**, load last 10 thread messages **[DB]**, then:
   - conversational + unchunked → `DecomposeWithQueries` (facts + search queries in one call) **[LLM]**
   - conversational + chunked → per-chunk `Decompose` **[LLM × N]** + one `DecomposeQueries` **[LLM]**
   - non-conversational → per-chunk `Decompose` only **[LLM × N]**
5. Embed all extracted fact texts + query texts. **[EMB]**
6. For each phrase embedding, vector-search facts at thread, agent, account scope (3 calls/phrase) and merge per-phrase top-K into a global top-10. **[VEC × 3 × phrases]**
7. `Evaluate`: LLM decides per new fact → `store` / `update` / `evolve`. **[LLM]**
8. Apply result:
   - store new facts → embed survivors **[EMB]**, insert rows **[DB]**
   - update existing → embed new texts **[EMB]**, update rows **[DB]**
   - evolve → embed successors **[EMB]**, supersede + insert **[DB]**

Chunking is token-aware (cl100k_base) with configurable `ChunkMaxTokens` (default 4000) and `ChunkOverlapTokens` (default 400). Paragraphs are preferred as split boundaries; a hard token cut is used only for single oversized paragraphs.

## Recall — Standard (`Recall`)

1. `DecomposeRecall`: split free-text query into search phrases. **[LLM]**
2. Embed phrases. **[EMB]**
3. Per-phrase vector search across thread/agent/account, merge top-100 candidates. **[VEC × 3 × phrases]**
4. Sibling expansion: load facts sharing a `source_id` with seeds, round-robin into the candidate set up to a 35-budget. **[DB]**
5. Cosine rerank candidates against query embeddings (in-memory, no call).
6. Text-normalized dedup (in-memory).
7. Date rerank: demote facts whose `referenced_at > event_date` (in-memory).
8. `SelectFacts`: LLM picks the relevant subset from the reranked candidates. **[LLM]**
9. Build response (loads sources/threads as needed). **[DB]**

## Recall — Light (`RecallLight`)

Cheaper, language-agnostic recall: no query decomposition, no per-phrase multi-scope fan-out, and the final selection runs on a small LLM (Gemini Flash Lite by default).

1. Embed the raw query as a single vector. **[EMB]**
2. Vector search across thread/agent/account, top-K=200. **[VEC × 3]**
3. Sibling expansion: round-robin facts sharing a `source_id` with seeds, budget 35. **[DB]**
4. Cosine rerank against the query embedding (in-memory).
5. Text-normalized dedup (in-memory).
6. Date rerank: demote facts whose `referenced_at > event_date` (in-memory).
7. `SelectFactsLight`: small-LLM pick over the full reranked candidate set, reusing the `select_facts` prompt. **[LLM]**
8. Build response. **[DB]**

## Recall — Zero (`RecallZero`)

LLM-free recall: same retrieval/post-processing chain as `RecallLight`, but the final LLM-based selection is dropped — the deterministic candidate list is truncated to the top N (`recallZeroDefaultLimit` = 30, overridable via `Limit`) and returned as-is.

1. Embed the raw query as a single vector. **[EMB]**
2. Vector search across thread/agent/account, top-K=200. **[VEC × 3]**
3. Sibling expansion: round-robin facts sharing a `source_id` with seeds, budget 35. **[DB]**
4. Cosine rerank against the query embedding (in-memory).
5. Text-normalized dedup (in-memory).
6. Date rerank: demote facts whose `referenced_at > event_date` (in-memory).
7. Truncate to top N (default 30, or `input.Limit` when set).
8. Build response. **[DB]**

## Differences: Standard vs Light vs Zero recall

- **Query decomposition**: standard calls `DecomposeRecall` (**[LLM]**) to produce multiple search phrases; light/zero skip it and use the raw query as a single embedding.
- **Embeddings**: standard embeds every phrase; light/zero embed once.
- **Vector search**: standard fans out `3 × phrases` searches and merges; light/zero do a single 3-scope search at K=200.
- **Final selection**: standard uses the primary `SelectFacts` model; light uses `SelectFactsLight` (Gemini Flash Lite) over the same prompt; zero performs no LLM call and returns the top N candidates directly.
- **Post-retrieval processing** (`expandBySource`, `cosineRerank`, `dedupByText`, `dateRerank`) is identical across all three.
