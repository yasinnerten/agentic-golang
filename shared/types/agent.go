package types

import "time"

// AgentType is the kind of agent (classification, routing, etc.).
type AgentType string

const (
	AgentTypeClassification     AgentType = "classification_agent"
	AgentTypeRoute              AgentType = "route_agent"
	AgentTypeTaskPlanner        AgentType = "task_planner_agent"
	AgentTypeDocumentClassifier AgentType = "document_classifier_agent"
	AgentTypeTextExtraction     AgentType = "text_extraction_agent"
	AgentTypeEvidenceScoring    AgentType = "evidence_scoring_agent"
	AgentTypeEvidenceMapping    AgentType = "evidence_mapping_agent"
	AgentTypeRules              AgentType = "rules_agent"
	AgentTypeAmbiguityReasoning AgentType = "ambiguity_reasoning_agent"
	AgentTypeReport             AgentType = "report_agent"
	AgentTypeRiskMatrix         AgentType = "risk_matrix_agent"
	AgentTypeConnectorTest      AgentType = "connector_test_agent"
	AgentTypeXAIBias            AgentType = "xai_bias_agent"
	AgentTypeRecalculation      AgentType = "recalculation_agent"
	AgentTypeGarbageCollection  AgentType = "garbage_collection_agent"
	AgentTypeSupervisor         AgentType = "supervisor_agent"
)

// AgentDefinition is the DB row loaded from public.agent_definitions.
type AgentDefinition struct {
	AgentDefinitionID       string    `json:"agent_definition_id"`
	AgentName               string    `json:"agent_name"`
	AgentType               string    `json:"agent_type"`
	DomainID                string    `json:"domain_id"`
	FrameworkID             string    `json:"framework_id"`
	Description             string    `json:"description"`
	InputSchemaJSON         []byte    `json:"input_schema_json"`
	OutputSchemaJSON        []byte    `json:"output_schema_json"`
	DefaultModelPolicyID    *string   `json:"default_model_policy_id"`
	DefaultPromptTemplateID *string   `json:"default_prompt_template_id"`
	CanUseLLM               bool      `json:"can_use_llm"`
	CanUseTools             bool      `json:"can_use_tools"`
	CanSpawnSubagents       bool      `json:"can_spawn_subagents"`
	CanRunParallel          bool      `json:"can_run_parallel"`
	MaxIterations           int       `json:"max_iterations"`
	MaxRuntimeMs            int       `json:"max_runtime_ms"`
	TimeoutMs               int       `json:"timeout_ms"`
	RetryPolicyID           *string   `json:"retry_policy_id"`
	ErrorPolicyID           *string   `json:"error_policy_id"`
	CachePolicyID           *string   `json:"cache_policy_id"`
	MemoryPolicyID          *string   `json:"memory_policy_id"`
	IsActive                bool      `json:"is_active"`
	CreatedAt               time.Time `json:"created_at"`
	UpdatedAt               time.Time `json:"updated_at"`
}

// AgentInstance is a runtime container for an AgentDefinition.
type AgentInstance struct {
	AgentInstanceID   string     `json:"agent_instance_id"`
	AgentDefinitionID string     `json:"agent_definition_id"`
	SessionID         string     `json:"session_id"`
	InstanceName      string     `json:"instance_name"`
	Status            string     `json:"status"`
	CurrentTaskID     string     `json:"current_task_id"`
	CurrentNodeID     string     `json:"current_node_id"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
	ClosedAt          *time.Time `json:"closed_at,omitempty"`
}
