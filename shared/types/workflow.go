package types

import "time"

// WorkflowDefinition is the header for a workflow graph.
type WorkflowDefinition struct {
	WorkflowID   string    `json:"workflow_id"`
	DomainID     string    `json:"domain_id"`
	FrameworkID  string    `json:"framework_id"`
	WorkflowName string    `json:"workflow_name"`
	Description  string    `json:"description"`
	EntryNodeID  string    `json:"entry_node_id"`
	IsActive     bool      `json:"is_active"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// WorkflowNode is a single step in a workflow.
type WorkflowNode struct {
	WorkflowNodeID     string    `json:"workflow_node_id"`
	WorkflowID         string    `json:"workflow_id"`
	NodeName           string    `json:"node_name"`
	NodeType           string    `json:"node_type"`
	AgentDefinitionID  string    `json:"agent_definition_id"`
	InputContractJSON  []byte    `json:"input_contract_json"`
	OutputContractJSON []byte    `json:"output_contract_json"`
	RulesFirst         bool      `json:"rules_first"`
	LLMAllowed         bool      `json:"llm_allowed"`
	ToolAllowed        bool      `json:"tool_allowed"`
	RetryPolicyID      string    `json:"retry_policy_id"`
	ErrorPolicyID      string    `json:"error_policy_id"`
	CachePolicyID      string    `json:"cache_policy_id"`
	IsIdempotent       bool      `json:"is_idempotent"`
	TimeoutMs          int       `json:"timeout_ms"`
	MaxRuntimeMs       int       `json:"max_runtime_ms"`
	CreatedAt          time.Time `json:"created_at"`
}

// WorkflowEdge links two workflow nodes with a CEL condition.
type WorkflowEdge struct {
	WorkflowEdgeID      string    `json:"workflow_edge_id"`
	WorkflowID          string    `json:"workflow_id"`
	FromNodeID          string    `json:"from_node_id"`
	ToNodeID            string    `json:"to_node_id"`
	ConditionExpression string    `json:"condition_expression"`
	Priority            int       `json:"priority"`
	CreatedAt           time.Time `json:"created_at"`
}

// WorkflowGraph is the runtime graph built from DB rows.
type WorkflowGraph struct {
	Definition WorkflowDefinition
	Nodes      map[string]WorkflowNode
	Edges      []WorkflowEdge
}
