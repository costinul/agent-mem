package models

import "time"

type SourceKind string

const (
	SourceSystem   SourceKind = "SYSTEM"
	SourceUser     SourceKind = "USER"
	SourceAgent    SourceKind = "AGENT"
	SourceTool     SourceKind = "TOOL"
	SourceDocument SourceKind = "DOCUMENT"
	SourceCode     SourceKind = "CODE"
)

type FactKind string

const (
	FactKindKnowledge  FactKind = "Knowledge"
	FactKindRule       FactKind = "Rule"
	FactKindPreference FactKind = "Preference"
)

type Metadata map[string]interface{}

type Input struct {
	Source      SourceKind `json:"source"`
	Content     string     `json:"content"`
	ContentType string     `json:"content_type,omitempty"`
	Metadata    Metadata   `json:"metadata,omitempty"`
}

type Fact struct {
	ID        string     `json:"id"`
	EventID   string     `json:"event_id"`
	Text      string     `json:"text"`
	Kind      FactKind   `json:"kind"`
	Source    SourceKind `json:"source"`
	CreatedAt time.Time  `json:"created_at"`
	Metadata  Metadata   `json:"metadata,omitempty"`
}

type Query struct {
	Text string `json:"text"`
}

type Decomposition struct {
	Facts   []Fact  `json:"facts"`
	Queries []Query `json:"queries,omitempty"`
}

type FactLink struct {
	FactID       string     `json:"fact_id"`
	Source       SourceKind `json:"source"`
	RelativePath string     `json:"relative_path,omitempty"`
}

type ProcessMemoryRequest struct {
	SessionID      *string `json:"session_id,omitempty"`
	EventID        string  `json:"event_id"`
	IncludeSources bool    `json:"include_sources"`
	MessageHistory int     `json:"message_history,omitempty"`
	Inputs         []Input `json:"inputs"`
}

type ProcessMemoryResponse struct {
	FactsToReturn []Fact   `json:"facts_to_return"`
	FactsToStore  []Fact   `json:"facts_to_store,omitempty"`
	FactsToUpdate []Fact   `json:"facts_to_update,omitempty"`
	FactIDsDelete []string `json:"fact_ids_delete,omitempty"`
}
