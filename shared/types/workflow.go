package types

import (
	"encoding/json"
	"fmt"
	"time"
)

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
	WorkflowNodeID     string `json:"workflow_node_id"`
	WorkflowID         string `json:"workflow_id"`
	NodeName           string `json:"node_name"`
	NodeType           string `json:"node_type"`
	AgentDefinitionID  string `json:"agent_definition_id"`
	InputContractJSON  []byte `json:"input_contract_json"`
	OutputContractJSON []byte `json:"output_contract_json"`
	// ConfigJSON holds node-type-specific settings, e.g. {"model": "..."} for an
	// agent node, {"workflow_id": "..."} for a subagent node, or the branch list
	// of a parallel node.
	ConfigJSON    []byte    `json:"config_json"`
	RulesFirst    bool      `json:"rules_first"`
	LLMAllowed    bool      `json:"llm_allowed"`
	ToolAllowed   bool      `json:"tool_allowed"`
	RetryPolicyID string    `json:"retry_policy_id"`
	ErrorPolicyID string    `json:"error_policy_id"`
	CachePolicyID string    `json:"cache_policy_id"`
	IsIdempotent  bool      `json:"is_idempotent"`
	TimeoutMs     int       `json:"timeout_ms"`
	MaxRuntimeMs  int       `json:"max_runtime_ms"`
	CreatedAt     time.Time `json:"created_at"`
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

// NodeConfig is the decoded form of WorkflowNode.ConfigJSON. One struct covers
// every node type; each type reads only the fields it needs.
type NodeConfig struct {
	// Model selection (agent nodes). Overrides the agent's model policy.
	Model          string   `json:"model,omitempty"`
	FallbackModels []string `json:"fallback_models,omitempty"`
	Temperature    *float64 `json:"temperature,omitempty"`
	MaxTokens      int      `json:"max_tokens,omitempty"`

	// Tool use (agent nodes). Tools restricts the node to the named tools;
	// empty means every tool the executor knows. MaxToolRounds bounds the
	// model -> tool -> model cycle.
	Tools         []string `json:"tools,omitempty"`
	MaxToolRounds int      `json:"max_tool_rounds,omitempty"`

	// Subagent nodes: the child workflow to run, and the key the child's
	// result is stored under in the parent's state (default: the node ID).
	WorkflowID string `json:"workflow_id,omitempty"`
	OutputKey  string `json:"output_key,omitempty"`

	// Parallel nodes: branches run concurrently, at most MaxConcurrency at a
	// time (0 = all at once). With FailFast the first branch error cancels
	// the rest.
	Branches       []BranchConfig `json:"branches,omitempty"`
	MaxConcurrency int            `json:"max_concurrency,omitempty"`
	FailFast       bool           `json:"fail_fast,omitempty"`
}

// BranchConfig is one branch of a parallel node. A branch runs either a single
// agent (AgentDefinitionID, optionally with its own Model) or a whole child
// workflow (WorkflowID) as a subagent.
type BranchConfig struct {
	Key               string `json:"key"`
	AgentDefinitionID string `json:"agent_definition_id,omitempty"`
	WorkflowID        string `json:"workflow_id,omitempty"`
	Model             string `json:"model,omitempty"`
	// Config is passed through as the branch node's own config_json, so a
	// branch can set tools, fallbacks and so on.
	Config map[string]any `json:"config,omitempty"`
}

// ParseNodeConfig decodes a node's config_json. Empty input yields a zero config.
func ParseNodeConfig(raw []byte) (NodeConfig, error) {
	var c NodeConfig
	if len(raw) == 0 {
		return c, nil
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, fmt.Errorf("parse node config: %w", err)
	}
	return c, nil
}
