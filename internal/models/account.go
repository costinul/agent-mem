package memory

import "time"

type Account struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Agent struct {
	ID        string    `json:"id"`
	AccountID string    `json:"account_id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Thread struct {
	ID        string    `json:"id"`
	AccountID string    `json:"account_id"`
	AgentID   string    `json:"agent_id"`
	CreatedAt time.Time `json:"created_at"`
}

type APIKey struct {
	ID        string     `json:"id"`
	AccountID string     `json:"account_id"`
	Prefix    string     `json:"prefix"`
	KeyHash   string     `json:"key_hash"`
	Label     *string    `json:"label"`
	ExpiresAt *time.Time `json:"expires_at"`
	Valid     bool       `json:"valid"`
	Debug     bool       `json:"debug"`
	CreatedAt time.Time  `json:"created_at"`
}
