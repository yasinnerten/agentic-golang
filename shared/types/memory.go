package types

import "time"

// MemoryScope controls how memory is queried.
type MemoryScope struct {
	SessionID  string
	TenantID   string
	MemoryType string
	Limit      int
}

// MemoryItem is a single record from agent_memory_items.
type MemoryItem struct {
	MemoryItemID    string     `json:"memory_item_id"`
	SessionID       string     `json:"session_id"`
	AgentInstanceID string     `json:"agent_instance_id"`
	TenantID        string     `json:"tenant_id"`
	MemoryType      string     `json:"memory_type"`
	Key             string     `json:"key"`
	ValueJSON       []byte     `json:"value_json"`
	SourceType      string     `json:"source_type"`
	SourceID        string     `json:"source_id"`
	Confidence      float64    `json:"confidence"`
	ExpiresAt       *time.Time `json:"expires_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
}
