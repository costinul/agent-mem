# Pricing Estimation

Working doc for cost-of-goods, handler-enforced limits, and tier sizing.

All cost figures are **estimates** and depend on:
- Gemini Flash & Flash Lite token prices (verify on the provider's pricing page before publishing)
- Embedding model cost
- Postgres / pgvector hosting cost
- Average prompt-template overhead per call

Numbers below use **actual Gemini pricing as of 2026-05-12**:
- Gemini 3.0 Flash: **$0.50 / 1M input, $3.00 / 1M output**
- Gemini 3.0 Flash Lite: **$0.25 / 1M input, $1.50 / 1M output**
- Embedding (text-embedding-3-small or gemini-embedding-001): **~$0.02 / 1M tokens**

If provider prices shift, scale linearly.

---

## 1. What "N" actually is

Earlier I wrote "1 decompose + N evaluate". That was wrong. The real per-operation LLM call shape is:

### Add — LLM call count

Per call to `POST /memory` with batch of `I` inputs (a "batch" = e.g. `{user_msg, agent_reply}` → `I=2`):

For each input `i`:
- Let `chunks_i = ceil(content_tokens_i / 3600)` (4000 max minus 400 overlap)
- Conversational + 1 chunk: **1 `DecomposeWithQueries` call**
- Conversational + multiple chunks: **`chunks_i` `Decompose` calls + 1 `DecomposeQueries`**
- Non-conversational: **`chunks_i` `Decompose` calls** (no queries)

Then once for the whole batch:
- **1 `Evaluate` call** — takes ALL extracted facts + retrieved facts together, returns store/update/evolve decisions for all of them in a single response.

Total LLM calls = `Σ chunks_i + (conversational ? I : 0) + 1`

Embed calls = **2 HTTP calls** total (one for fact texts + query texts, one for surviving fact texts after evaluation). Billed by token count of all items combined.

Vector searches = `3 × phrases_total` where `phrases_total = Σ queries_i`.

### Recall Standard — LLM call count

Per call to `POST /memory/recall`:
- **1 `DecomposeRecall`** call (~2–4 phrases out, plus entity list)
- **1 `Embed`** call (batched, all phrases)
- **`3 × phrases`** vector searches
- **1 `SelectFacts`** call (1 strong-model call over top-K candidates)
- Optional: 1 `SelectFactsGap` call on the light model if two-step gap-fill is enabled

= **2–3 LLM calls** per recall.

### Recall Zero — LLM call count

- **1 `Embed`** call (single phrase = the raw query)
- **3** vector searches
- **0** LLM calls

= **0 LLM calls**. Cost is dominated by pgvector compute + DB egress.

---

## 2. Per-call cost — Add tiered by content size

Assumptions per chunk:
- Decompose prompt template + few-shot: **~2,000 tokens** baseline input
- Output per Decompose: **~400 tokens** (3–5 facts + reasoning)

Assumptions for Evaluate:
- Prompt template: **~1,500 tokens** baseline
- Plus all new facts: ~50 tokens each
- Plus retrieved facts: ~50 tokens each × 30 retrieved (the ingest pipeline retrieves top-30)
- Output: ~50 tokens per decision

| Size class | Content tokens | Chunks | Decompose calls | Evaluate (in/out) | LLM cost | Embed | Total ~ |
|---|---|---|---|---|---|---|---|
| **Tiny** (single message) | 100 | 1 | 1 × (2,100 in / 300 out) | 3,000 / 400 | $0.0047 | $0.00001 | **$0.0047** |
| **Small** (1 turn, both msgs) | 400 | 1+1 | 2 × (2,200 in / 400 out) | 3,500 / 600 | $0.0081 | $0.00002 | **$0.0081** |
| **Medium** (long message) | 2,000 | 1 | 1 × (4,000 in / 600 out) | 4,000 / 800 | $0.0082 | $0.00003 | **$0.0082** |
| **Large** (chunked doc) | 8,000 | 2 | 2 × (4,500 in / 700 out) + 1 query call | 4,500 / 1,200 | $0.0180 | $0.00004 | **$0.0180** |
| **XL** (long doc) | 20,000 | 5 | 5 × (4,500 in / 700 out) | 6,000 / 2,500 | $0.0323 | $0.00006 | **$0.0324** |
| **XXL** (book chapter) | 100,000 | 25 | 25 × (4,500 in / 700 out) | 15,000 / 6,000 | $0.134 | $0.00020 | **$0.134** |

Notes:
- Vector search cost (DB compute) is negligible per call (<$0.0001) but not zero — count it in DB amortization, not per-call.
- DB inserts: negligible compute, real storage growth (see §4).
- **Evaluate scales sub-linearly** in content size but linearly in extracted fact count. At XXL, Evaluate may approach Flash's output budget — practical bottleneck for huge ingests.

**Rule of thumb: ~$0.008 per "typical conversational Add" (one turn).**

---

## 3. Per-call cost — Recall tiered by candidate set size

The dominant variable for Standard Recall cost is **how many candidates are sent to `SelectFacts`**.

Assumptions:
- `DecomposeRecall`: ~1,500 in / ~200 out → $0.001
- `SelectFacts` template baseline: ~1,500 tokens
- Per candidate sent: ~60 tokens (text + metadata + index)

| Candidates → SelectFacts | SelectFacts (in/out) | Decompose | Total LLM cost | Total |
|---|---|---|---|---|
| 30 | 3,300 / 400 | $0.00135 | $0.0042 | **$0.0042** |
| 50 (default `recallCandidateK`) | 4,500 / 500 | $0.00135 | $0.0051 | **$0.0051** |
| 100 | 7,500 / 700 | $0.00135 | $0.0072 | **$0.0072** |
| 150 (two-step max) | 10,500 / 1,000 | $0.00135 | $0.0096 | **$0.0096** |

**Rule of thumb: ~$0.005 per Standard Recall at default settings.**

### Recall Zero (= the new `/memory/search`)

- 1 embed call (~50 tokens): ~$0.000001
- 3 pgvector searches: ~$0.0001 of DB compute (assuming ~1M facts, HNSW index)
- CPU post-processing: ~0

**Rule of thumb: ~$0.0001 per Search.**

This is ~40× cheaper than Standard Recall. It is the only operation we can afford to give away effectively for free.

---

## 4. Storage cost

| Item | Per fact | At 100k facts | At 1M facts |
|---|---|---|---|
| Fact text (~10 words avg) | ~60–100 B | ~10 MB | ~100 MB |
| Row metadata (UUIDs, timestamps, kind, entities) | ~300 B | ~30 MB | ~300 MB |
| pgvector embedding (768-dim float32) | ~3 KB | ~300 MB | ~3 GB |
| HNSW index overhead (amortized) | ~700 B | ~70 MB | ~700 MB |
| Source row share (amortized, ~4 facts/source) | ~250 B | ~25 MB | ~250 MB |
| Postgres page/TOAST overhead | ~200 B | ~20 MB | ~200 MB |
| **Total disk** | **~4.5 KB** | **~455 MB** | **~4.5 GB** |

The embedding dominates — the 10-word fact text itself is ~1% of the storage. Cutting fact text in half doesn't change the picture.

Managed Postgres storage: ~$0.10–0.15/GB/mo. Compute (instance) is the real fixed cost.

**Per-fact per-month storage cost: ~$0.000001** (basically free; the DB instance fixed cost dominates for low fact counts).

The risk isn't per-fact cost — it's **unbounded accumulation on inactive free accounts**. Mitigation: TTL on free-tier facts (e.g. 30 days), or hard cap on facts per free account.

---

## 5. Handler-enforced limits (new)

Today both `/memory/recall` and `/memory/recall/zero` accept an unbounded `input.Limit` (the engine treats `limit ≤ 0` as "no truncation"). Search (Zero) currently has no documented default; the `engine.md` comment claims 30 but no `recallZeroDefaultLimit` constant actually exists in code. This is a gap.

### Proposed limits

| Endpoint | Default | Min | Hard max (enforced at handler) | Reason |
|---|---|---|---|---|
| `/memory/search` (Zero) | **20** | 1 | **50** | No LLM filter — precision is per-item lower, so caller needs more candidates to compensate. Consider raising to 30 if eval shows answer-in-top-20 hit rate is weak. |
| `/memory/recall` (Standard) | **20** | 1 | **50** | SelectFacts already self-limits, but a hard cap prevents accidental gigantic responses. |
| `Add` `inputs[]` length | (existing) | 1 | **20** | Prevents pathological batches; protects Evaluate prompt size. |
| `Add` `content` per input | — | — | **32k tokens** | ~8 chunks max; bounds worst-case Add cost at ~$0.04. |

Validation lives in the handler (`memory_handlers.go`), not the engine, so it returns 400 early with a clear error before any LLM cost is incurred.

### Why a Search default at all?

Without a default cap, naive callers will receive up to ~235 facts (200 retrieved + 35 sibling expansion). That payload is large (each fact carries text + metadata, ~500 bytes JSON), and downstream agents will burn tokens stuffing them into context.

Default = 20 (not 10) because Search has no LLM filter — per-item precision is lower, so the caller needs more candidates to compensate. Standard's 20 are curated; Search's 20 are raw cosine of similar size to give the agent enough material to ground on.

---

## 6. Free tier

| Quota | Volume / month | Unit cost | Worst case |
|---|---|---|---|
| Adds | 100 | $0.008 | $0.80 |
| Standard Recalls | 200 | $0.005 | $1.00 |
| Searches (Zero) | 2,000 | $0.0001 | $0.20 |
| Storage | Bounded by Adds (~$0.001/mo); facts purged after 30 days of account inactivity | — | ~$0 |
| **Per active user / mo (worst case)** | | | **~$2.00** |

At 30% utilization: ~$0.62/user/mo. 1,000 active users ≈ **$620/mo** worst case, ~$200/mo expected. Survivable while paid revenue ramps.

Most signups churn or convert within 1–2 months — they don't linger as long-term free riders. Combined with the 30-day TTL, this caps storage tail risk.

**Why no signup credit, no $5 trial:**
- Free monthly tier is the industry standard (Supabase, mem0, Zep, Pinecone). Trial credits and paid trials add a pricing-page tier nobody needs.
- Free monthly cap is *cheaper per signup* than a $5 credit ($0.62 expected vs $0.90 expected) because most users don't stick around long enough to consume 12 months.

---

## 7. Paid tiers — Managed (you pay LLM passthrough)

Annual = 2 months free (~16.7% off).

| Tier | Monthly | Annual | Adds | Recalls | Searches |
|---|---|---|---|---|---|
| **Free** | $0 | — | 100 | 200 | 2,000 |
| **Hobby** | $19 | $190 | 1,000 | 500 | 10,000 |
| **Builder** | $59 | $590 | 3,000 | 1,500 | 30,000 |
| **Pro** | $199 | $1,990 | 10,000 | 5,000 | 100,000 |

**No storage cap on Managed tiers.** Add quota already bounds storage growth: 1 Add ≈ 4 facts × 4.5 KB = ~18 KB. Pro at 10,000 Adds/mo accumulates ~180 MB/mo (~$0.02/mo storage cost — rounding error vs $199 revenue). Caps would add tracking/enforcement complexity for cost protection that doesn't exist.

**Free tier exception:** facts auto-delete after 30 days of account inactivity (no Add/Recall/Search). Protects against signup-and-abandon storage accumulation; active free users keep their data indefinitely.

### Margin check at 100% utilization

| Tier | Cost worst-case | Monthly margin | Annual margin |
|---|---|---|---|
| Hobby | $11.50 | $7.50 / **39%** | $52 / **27%** |
| Builder | $34.50 | $24.50 / **42%** | $176 / **30%** |
| Pro | $115 | $84 / **42%** | $610 / **31%** |

At 30% utilization (realistic), every tier sits at 70%+ margin. Annual at 100% util stays above 25% on every tier — the floor.

**One Add = up to 4,000 input tokens** (one chunk). Larger inputs consume multiple Add units. Keeps per-unit cost flat and protects you from variance.

---

## 8. BYOK tiers (user brings Gemini key)

For developers who'd rather pay Google directly. You only charge for storage + orchestration.

| Tier | Monthly | Annual | Fact cap |
|---|---|---|---|
| **BYOK Free** | $0 | — | 5,000 facts (~30 MB), 30d TTL |
| **BYOK Plus** | $9 | $90 | 50,000 facts (~300 MB), no TTL |
| **BYOK Pro** | $29 | $290 | 500,000 facts (~3 GB), no TTL |

Your cost per BYOK user: ~$0.07–$0.50/mo. Margins above 90% on paid BYOK tiers because no LLM passthrough.

---

## 9. Overage pricing (Managed only)

Once quota is exhausted, charge per-call:

| Operation | Cost | Retail | Markup |
|---|---|---|---|
| Add | $0.008 | $0.015 | ~2× |
| Standard Recall | $0.005 | $0.010 | 2× |
| Search (Zero) | $0.0001 | $0.0005 | 5× |
| Storage / GB / mo | $0.10 | $1.00 | 10× |

Default behavior: hard stop at quota. Overage charged only if user explicitly enables it. Protects users from runaway bills.

---

## 10. Sensitivity — what changes the picture

Numbers above are most sensitive to:

1. **Average Add content size.** If your typical user sends 500-token messages (Small class), unit cost stays ~$0.008. If they're ingesting 20k-token docs (XL), it jumps 4×. Worth instrumenting average input size per account before finalizing pricing.

2. **Average candidates passed to SelectFacts.** Currently `recallCandidateK = 100`. Dropping to 50 cuts SelectFacts cost ~30%. Worth A/B-ing on the eval set.

3. **Average retrieved set size in Evaluate.** Currently 30. Each retrieved fact is ~50 tokens × 30 = 1,500 tokens of prompt baseline. If accuracy is preserved at 15, cost drops meaningfully.

4. **Two-step recall enabled (`TwoStepEnabled`).** Adds 1 light-model call on miss → ~$0.0005 extra. Probably safe to enable; doesn't shift tier math.

5. **Active vs total free users.** The $120/mo bootstrap math assumes the 60% BYOK + 20% trial split is roughly stable. If 80% of signups end up on managed-free instead of BYOK, costs jump meaningfully. UX has to default-steer new signups toward BYOK.

---

## 11. Open questions

1. What does the eval data say about quality difference between Standard and Search at the default limit of 20? Without this, we can't tell users "use Search unless you really need Standard."
2. Is the BYOK setup-friction acceptable as-is, or does the "paste your Gemini key" wizard need polish to feel free-tier-grade?
3. Should annual prepay discount stay at "2 months free" (16.7%) or move to 20% for a cleaner pitch? 20% squeezes Builder annual to ~22% margin at 100% util — tight but workable.
4. Hard cap at quota or allow overage by default? Recommendation: hard cap by default, overage opt-in via account setting.
