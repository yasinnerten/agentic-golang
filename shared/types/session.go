package types

import "time"

// SessionType categorizes an agent session.
type SessionType string

const (
	SessionTypeClassification SessionType = "classification_session"
	SessionTypeEvidence       SessionType = "evidence_session"
	SessionTypeRAG            SessionType = "rag_session"
	SessionTypeReport         SessionType = "report_session"
	SessionTypeConnectorTest  SessionType = "connector_test_session"
	SessionTypeRiskMatrix     SessionType = "risk_matrix_session"
	SessionTypeXAIBias        SessionType = "xai_bias_session"
	SessionTypeAdminDebug     SessionType = "admin_debug_session"
	SessionTypeSubagent       SessionType = "subagent_session"
)

// AgentSession is the runtime session record.
type AgentSession struct {
	SessionID         string     `json:"session_id"`
	ParentSessionID   string     `json:"parent_session_id"`
	DomainID          string     `json:"domain_id"`
	FrameworkID       string     `json:"framework_id"`
	TenantID          string     `json:"tenant_id"`
	SessionType       string     `json:"session_type"`
	Status            string     `json:"status"`
	StartedByUserID   string     `json:"started_by_user_id"`
	AssignedAgentID   string     `json:"assigned_agent_id"`
	CurrentWorkflowID string     `json:"current_workflow_id"`
	CurrentNodeID     string     `json:"current_node_id"`
	CompressedStateID string     `json:"compressed_state_id"`
	RawContextPolicy  string     `json:"raw_context_policy"`
	MaxRuntimeMs      int        `json:"max_runtime_ms"`
	TimeoutMs         int        `json:"timeout_ms"`
	MaxAgentRuns      int        `json:"max_agent_runs"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
	LastActivityAt    time.Time  `json:"last_activity_at"`
	ClosedAt          *time.Time `json:"closed_at,omitempty"`
}

// CompressedState is a minimal assessment snapshot.
type CompressedState struct {
	FrameworkID     string   `json:"framework_id"`
	Route           string   `json:"route"`
	ActiveModules   []string `json:"active_modules"`
	MissingEvidence []string `json:"missing_evidence"`
	OpenQuestions   []string `json:"open_questions"`
	RiskLevel       string   `json:"risk_level"`
	Confidence      float64  `json:"confidence"`
}
