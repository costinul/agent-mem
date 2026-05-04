# Engine

Notation: **[LLM]** = LLM call, **[EMB]** = embedding call, **[VEC]** = vector search call, **[DB]** = plain DB write/read.

## Contextual — Ingestion (`ProcessContextual`)

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

## Contextual — Recall (`Recall`)

Recall is shared between contextual and factual. Same code path.

1. `DecomposeRecall`: split free-text query into search phrases. **[LLM]**
2. Embed phrases. **[EMB]**
3. Per-phrase vector search across thread/agent/account, merge top-100 candidates. **[VEC × 3 × phrases]**
4. Sibling expansion: load facts sharing a `source_id` with seeds, round-robin into the candidate set up to a 35-budget. **[DB]**
5. Cosine rerank candidates against query embeddings (in-memory, no call).
6. Text-normalized dedup (in-memory).
7. Date rerank: demote facts whose `referenced_at > event_date` (in-memory).
8. `SelectFacts`: LLM picks the relevant subset from the reranked candidates. **[LLM]**
9. Build response (loads sources/threads as needed). **[DB]**

## Factual — Ingestion (`AddFactual`)

1. Validate input.
2. Insert `Event` row. **[DB]**
3. For each input: insert `Source` **[DB]**, then `Decompose` per chunk (no message history, no queries when non-conversational). **[LLM × N]**
4. Embed extracted fact texts. **[EMB]**
5. Per-phrase vector search, thread/agent/account, top-10. **[VEC × 3 × phrases]**
6. `Evaluate` LLM → store/update/evolve. **[LLM]**
7. Apply result (same store/update/evolve path as contextual, each with **[EMB]** + **[DB]**).

## Factual — Recall

Same `Recall` path as contextual (see above).

## Differences: Contextual vs Factual ingestion

- **Event-date ordering**: contextual enforces monotonic `event_date` per thread; factual does not.
- **Message history**: contextual loads the last 10 thread messages and feeds them to the decomposer as prior context; factual does not.
- **Decomposer outputs**: for conversational unchunked inputs, contextual uses the combined `DecomposeWithQueries` (facts + queries in one call) and otherwise plans queries explicitly via `DecomposeQueries`. Factual relies on plain `Decompose` (facts only) — no separate query-planning call, so search embeddings come only from the extracted fact texts.
- **Everything else is identical**: `Source`/`Event` insertion, embedding, three-scope vector retrieval, `Evaluate`, and the store/update/evolve apply step are the exact same code.
- **Recall**: shared, no difference.
