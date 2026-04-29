package memory

import "time"

// =====================
// API — Input
// =====================

// MemoryInput is the request body for the contextual smart pipeline.
type MemoryInput struct {
	AccountID string      `json:"-" swaggerignore:"true"`
	AgentID   string      `json:"-" swaggerignore:"true"`
	ThreadID  string      `json:"thread_id"`
	Inputs    []InputItem `json:"inputs"`
}

// InputItem is a single piece of content within one API call.
// Text inputs set Content. File inputs set Content as base64 and ContentType accordingly.
type InputItem struct {
	Kind        SourceKind `json:"kind"`
	Author      *string    `json:"author,omitempty"`     // Optional: name of the person or entity that produced this turn (e.g. "Alex").
	Content     string     `json:"content"`              // Text as string, files as base64.
	ContentType string     `json:"content_type"`         // MIME type: text/plain, application/pdf, image/png, etc.
	EventDate   *time.Time `json:"event_date,omitempty"` // When the message was authored. Used to anchor relative dates during fact extraction. Defaults to now().
}

// =====================
// API — Output
// =====================

// DurationStats holds cumulative timing information for a single memory operation.
type DurationStats struct {
	DBMs    int64 `json:"db_ms"`
	DBCalls int   `json:"db_calls"`
	AIMs    int64 `json:"ai_ms"`
	AICalls int   `json:"ai_calls"`
}

// WriteOutput is the acknowledgement returned by contextual/factual write pipelines.
// Writes are fire-and-forget: the caller delegates fact extraction/storage to the
// memory manager and does not receive the resulting facts. Use the recall or
// fact-listing endpoints to inspect what is stored.
type WriteOutput struct {
	Duration DurationStats `json:"duration"`
}

// RecallOutput is the response from the recall (read-only retrieval) endpoint.
type RecallOutput struct {
	Facts    []ReturnedFact `json:"facts"`
	Duration DurationStats  `json:"duration"`
	Debug    *RecallDebug   `json:"debug,omitempty"`
}

// RecallDebug holds verbose diagnostic information emitted only when the API key has debug=true.
type RecallDebug struct {
	Query            string           `json:"query"`
	Phrases          []string         `json:"phrases"`
	EventDate        string           `json:"event_date,omitempty"`
	RetrievedCount   int              `json:"retrieved_count"`
	ExpandedCount    int              `json:"expanded_count"`
	InWindowCount    int              `json:"in_window_count"`
	OutOfWindowCount int              `json:"out_of_window_count"`
	DateWindowDays   int              `json:"date_window_days"`
	Candidates       []DebugCandidate `json:"candidates"`
	SelectedIDs      []string         `json:"selected_ids"`
	Errors           []string         `json:"errors,omitempty"`
}

// DebugCandidate is a single candidate fact entry in RecallDebug.
type DebugCandidate struct {
	ID           string     `json:"id"`
	Text         string     `json:"text"`
	SourceID     string     `json:"source_id"`
	Kind         FactKind   `json:"kind"`
	EventDate    string     `json:"event_date,omitempty"`
	ReferencedAt *time.Time `json:"referenced_at,omitempty"`
	InWindow     bool       `json:"in_window"`
	Selected     bool       `json:"selected"`
}

// ThreadMessagesOutput is the response from the thread messages endpoint.
type ThreadMessagesOutput struct {
	Messages []ConversationMessage `json:"messages"`
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
	ThreadID  string     `json:"thread_id"`
	Kind      SourceKind `json:"kind"`
	Author    *string    `json:"author,omitempty"` // Set when the original input carried an author.
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

// FactualInput is the request body for the factual interface.
type FactualInput struct {
	AccountID string      `json:"-" swaggerignore:"true"`
	AgentID   string      `json:"-" swaggerignore:"true"`
	ThreadID  string      `json:"thread_id"`
	Inputs    []InputItem `json:"inputs"`
}

type FactUpdateBody struct {
	Text   string     `json:"text"`
	Source SourceKind `json:"source"`
}

// FactListOutput is the response for listing/browsing facts.
type FactListOutput struct {
	Facts  []ReturnedFact `json:"facts"`
	Total  int            `json:"total"`
	Limit  int            `json:"limit"`
	Offset int            `json:"offset"`
}

// RecallInput is the request body for read-only memory retrieval.
type RecallInput struct {
	AccountID      string     `json:"-" swaggerignore:"true"`
	AgentID        string     `json:"agent_id"`
	ThreadID       string     `json:"thread_id"`
	Query          string     `json:"query"`
	EventDate      *time.Time `json:"event_date,omitempty"` // When the question is being asked. Used to resolve relative-time phrases in the query. Defaults to now().
	Limit          int        `json:"limit"`
	IncludeSources bool       `json:"include_sources"`
	Debug          bool       `json:"-" swaggerignore:"true"`
}

type ThreadCreateBody struct {
	AgentID string `json:"agent_id"`
}
