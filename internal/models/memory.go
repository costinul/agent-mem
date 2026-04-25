package memory

import (
	"time"
)

// =====================
// Enums
// =====================

// SourceKind classifies the origin of data entering the memory engine.
type SourceKind string

const (
	SOURCE_SYSTEM   SourceKind = "SYSTEM"   // Current date, thread info, environment variables. Immutable.
	SOURCE_USER     SourceKind = "USER"     // Input from whoever is initiating the request to the agent.
	SOURCE_AGENT    SourceKind = "AGENT"    // The agent's own generated output.
	SOURCE_TOOL     SourceKind = "TOOL"     // Output from any tool or sub-agent invoked by the agent.
	SOURCE_DOCUMENT SourceKind = "DOCUMENT" // Any non-code reference material. PDFs, text files, spreadsheets.
	SOURCE_CODE     SourceKind = "CODE"     // Source code, configuration files, schemas, technical artifacts.
)

// SourceTrustHierarchy defines the trust level for each source.
// Higher value = higher trust. A source can only alter facts from equal or lower trust level.
var SourceTrustHierarchy = map[SourceKind]int{
	SOURCE_SYSTEM:   100, // Immutable — cannot be altered by any source.
	SOURCE_USER:     80,
	SOURCE_AGENT:    60,
	SOURCE_TOOL:     40,
	SOURCE_DOCUMENT: 20,
	SOURCE_CODE:     10,
}

// FactKind classifies how a fact behaves during retrieval.
type FactKind string

const (
	FACT_KIND_KNOWLEDGE  FactKind = "KNOWLEDGE"  // Factual information. Retrieved only when semantically relevant.
	FACT_KIND_RULE       FactKind = "RULE"       // Always included in retrieval. User-defined rules.
	FACT_KIND_PREFERENCE FactKind = "PREFERENCE" // Soft weight for decisions. Retrieved when relevant to choices.
)

// =====================
// Stored — Events and Sources
// =====================

// Event represents a single API call. Groups all sources submitted together.
type Event struct {
	ID        string    `json:"id"`
	AccountID string    `json:"account_id"`
	AgentID   string    `json:"agent_id"`
	ThreadID  *string   `json:"thread_id"` // Null means global scope.
	CreatedAt time.Time `json:"created_at"`
}

// Source is a stored input within an event.
// Exactly one of Content or BucketPath is populated.
// Text inputs store content inline. File inputs are uploaded to the bucket.
type Source struct {
	ID          string     `json:"id"`
	EventID     string     `json:"event_id"`
	Kind        SourceKind `json:"kind"`
	Author      *string    `json:"author,omitempty"` // Optional: name of the person or entity that produced this input.
	Content     *string    `json:"content"`          // Populated for text inputs.
	ContentType string     `json:"content_type"`
	BucketPath  *string    `json:"bucket_path"` // Populated for file inputs stored in S3-compatible bucket.
	SizeBytes   *int64     `json:"size_bytes"`  // Size of the original payload in bytes.
	CreatedAt   time.Time  `json:"created_at"`
}

// =====================
// Stored — Facts
// =====================

// Fact is an atomic piece of information extracted from a source.
type Fact struct {
	ID           string     `json:"id"`
	AccountID    string     `json:"account_id"`
	AgentID      *string    `json:"agent_id"`
	ThreadID     *string    `json:"thread_id"`    // Null means agent-level or account-level scope.
	SourceID     string     `json:"source_id"`    // The source that produced this fact.
	Kind         FactKind   `json:"kind"`
	Text         string     `json:"text"`
	Embedding    []float64  `json:"embedding"`
	ReferencedAt *time.Time `json:"referenced_at,omitempty"` // Calendar date the fact's content refers to, extracted at decompose time.
	SupersededAt *time.Time `json:"superseded_at"` // Non-nil means this fact has been evolved into a successor.
	SupersededBy *string    `json:"superseded_by"` // ID of the successor fact.
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// =====================
// Operational — Decomposition
// =====================

// Decomposition holds facts and queries extracted from a source by the LLM.
type Decomposition struct {
	Facts     []ExtractedFact  `json:"facts"`
	Queries   []ExtractedQuery `json:"queries"`
	QueryDate *time.Time       `json:"query_date,omitempty"` // Populated by decompose_recall when the query references a specific date.
}

// ExtractedFact is a fact produced during decomposition, before it is stored.
type ExtractedFact struct {
	Text         string     `json:"text"`
	Kind         FactKind   `json:"kind"`
	ReferencedAt *time.Time `json:"referenced_at,omitempty"`
}

// ExtractedQuery is a query produced during decomposition, used for vector search only — never stored.
type ExtractedQuery struct {
	Text string `json:"text"`
}

// =====================
// Operational — Evaluate
// =====================

// FactEvolution describes a fact that should be superseded by a new version.
type FactEvolution struct {
	OldFactID string   `json:"old_fact_id"`
	NewText   string   `json:"new_text"`
	NewKind   FactKind `json:"new_kind"`
}

// EvaluateResult is the output of the evaluate step.
// Trust hierarchy is enforced: a source can only alter facts from equal or lower trust level.
type EvaluateResult struct {
	FactsToReturn []Fact          `json:"facts_to_return"` // Relevant existing + new facts to return to the agent.
	FactsToStore  []Fact          `json:"facts_to_store"`  // New facts to insert.
	FactsToUpdate []Fact          `json:"facts_to_update"` // Existing facts contradicted by higher/equal trust source.
	FactsToEvolve []FactEvolution `json:"facts_to_evolve"` // Facts superseded by new information; old fact is preserved with lineage.
}
