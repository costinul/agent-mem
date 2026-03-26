package memory

import "time"

// =====================
// API — Input
// =====================

// MemoryInput is the request body for the contextual smart pipeline.
type MemoryInput struct {
	AccountID      string      `json:"account_id"`
	AgentID        string      `json:"agent_id"`
	SessionID      string      `json:"session_id"`
	IncludeSources bool        `json:"include_sources"` // When true, return original source content with facts.
	MessageHistory int         `json:"message_history"` // Number of recent raw messages to return. 0 = facts only.
	Inputs         []InputItem `json:"inputs"`
}

// InputItem is a single piece of content within one API call.
// Text inputs set Content. File inputs set Content as base64 and ContentType accordingly.
type InputItem struct {
	Kind        SourceKind `json:"kind"`
	Content     string     `json:"content"`      // Text as string, files as base64.
	ContentType string     `json:"content_type"` // MIME type: text/plain, application/pdf, image/png, etc.
}

// =====================
// API — Output
// =====================

// MemoryOutput is the response from the contextual smart pipeline.
type MemoryOutput struct {
	Facts    []ReturnedFact        `json:"facts"`    // Relevant facts for the agent.
	Messages []ConversationMessage `json:"messages"` // Recent USER/AGENT messages from sources. Empty if MessageHistory is 0.
}

// ReturnedFact is a fact returned to the agent.
type ReturnedFact struct {
	ID             string     `json:"id"`
	Text           string     `json:"text"`
	Kind           FactKind   `json:"kind"`
	SourceKind     SourceKind `json:"source_kind"`
	OriginalSource *string    `json:"original_source"` // Populated only if IncludeSources is true.
}

// ConversationMessage is a conversation message projected from sources.
type ConversationMessage struct {
	SourceID  string     `json:"source_id"`
	EventID   string     `json:"event_id"`
	SessionID string     `json:"session_id"`
	Kind      SourceKind `json:"kind"`
	Content   string     `json:"content"`
	CreatedAt time.Time  `json:"created_at"`
}

// =====================
// API — Fact Management Requests
// =====================

type FactGetRequest struct {
	FactID         string `json:"fact_id"`
	IncludeSources bool   `json:"include_sources"`
}

type FactUpdateRequest struct {
	FactID string     `json:"fact_id"`
	Text   string     `json:"text"`   // New fact text. Embedding will be regenerated.
	Source SourceKind `json:"source"` // Must be equal or higher trust than the target fact's source.
}

type FactDeleteRequest struct {
	FactIDs []string   `json:"fact_ids"`
	Source  SourceKind `json:"source"` // Must be equal or higher trust than the target fact's source.
}

// FactualInput is the request body for the factual interface.
// SessionID is optional; when omitted, facts are agent/account scoped.
type FactualInput struct {
	AccountID string      `json:"account_id"`
	AgentID   string      `json:"agent_id"`
	SessionID string      `json:"session_id"`
	Inputs    []InputItem `json:"inputs"`
}

type FactUpdateBody struct {
	Text   string     `json:"text"`
	Source SourceKind `json:"source"`
}

type FactDeleteBody struct {
	FactIDs []string   `json:"fact_ids"`
	Source  SourceKind `json:"source"`
}
