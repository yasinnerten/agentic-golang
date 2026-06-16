package workflow

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/yasinnerten/agentic-golang/services/rulesengine"
	"github.com/yasinnerten/agentic-golang/shared/types"
)

// Builder constructs WorkflowGraph objects from DB rows.
type Builder struct {
	db *sql.DB
}

// NewBuilder creates a workflow graph builder.
func NewBuilder(db *sql.DB) *Builder {
	return &Builder{db: db}
}

// LoadGraph loads a complete workflow graph by workflow ID.
func (b *Builder) LoadGraph(ctx context.Context, workflowID string) (*types.WorkflowGraph, error) {
	var def types.WorkflowDefinition
	err := b.db.QueryRowContext(ctx,
		`SELECT workflow_id, domain_id, framework_id, workflow_name, description, entry_node_id, is_active, created_at, updated_at
		FROM public.workflow_definitions WHERE workflow_id = $1`, workflowID).Scan(
		&def.WorkflowID, &def.DomainID, &def.FrameworkID, &def.WorkflowName, &def.Description,
		&def.EntryNodeID, &def.IsActive, &def.CreatedAt, &def.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("load definition: %w", err)
	}

	// Load nodes
	nodeRows, err := b.db.QueryContext(ctx,
		`SELECT workflow_node_id, workflow_id, node_name, node_type, COALESCE(agent_definition_id,''),
		input_contract_json, output_contract_json, rules_first, llm_allowed, tool_allowed,
		COALESCE(retry_policy_id,''), COALESCE(error_policy_id,''), COALESCE(cache_policy_id,''), is_idempotent, timeout_ms, max_runtime_ms, created_at
		FROM public.workflow_nodes WHERE workflow_id = $1`, workflowID)
	if err != nil {
		return nil, fmt.Errorf("load nodes: %w", err)
	}
	defer nodeRows.Close()

	nodes := make(map[string]types.WorkflowNode)
	for nodeRows.Next() {
		var n types.WorkflowNode
		if err := nodeRows.Scan(&n.WorkflowNodeID, &n.WorkflowID, &n.NodeName, &n.NodeType, &n.AgentDefinitionID,
			&n.InputContractJSON, &n.OutputContractJSON, &n.RulesFirst, &n.LLMAllowed, &n.ToolAllowed,
			&n.RetryPolicyID, &n.ErrorPolicyID, &n.CachePolicyID, &n.IsIdempotent, &n.TimeoutMs, &n.MaxRuntimeMs, &n.CreatedAt); err != nil {
			return nil, err
		}
		nodes[n.WorkflowNodeID] = n
	}
	if err := nodeRows.Err(); err != nil {
		return nil, err
	}

	// Load edges
	edgeRows, err := b.db.QueryContext(ctx,
		`SELECT workflow_edge_id, workflow_id, from_node_id, to_node_id, condition_expression, priority, created_at
		FROM public.workflow_edges WHERE workflow_id = $1 ORDER BY priority DESC`, workflowID)
	if err != nil {
		return nil, fmt.Errorf("load edges: %w", err)
	}
	defer edgeRows.Close()

	var edges []types.WorkflowEdge
	for edgeRows.Next() {
		var e types.WorkflowEdge
		if err := edgeRows.Scan(&e.WorkflowEdgeID, &e.WorkflowID, &e.FromNodeID, &e.ToNodeID, &e.ConditionExpression, &e.Priority, &e.CreatedAt); err != nil {
			return nil, err
		}
		edges = append(edges, e)
	}
	if err := edgeRows.Err(); err != nil {
		return nil, err
	}

	return &types.WorkflowGraph{
		Definition: def,
		Nodes:      nodes,
		Edges:      edges,
	}, nil
}

// SelectNextEdge returns the highest-priority outgoing edge whose CEL condition
// evaluates true against env (the accumulated loop state). Candidates are
// pre-sorted by priority DESC. An empty condition is treated as always-true. A
// condition that fails to compile/evaluate (e.g. it references a variable not
// present in env) is treated as not-matching and skipped, so an unrelated edge
// can never be taken by accident. Returns nil when nothing matches (the loop
// then treats the node as terminal).
func SelectNextEdge(eng *rulesengine.Engine, g *types.WorkflowGraph, nodeID string, env map[string]any) *types.WorkflowEdge {
	candidates := outgoing(g, nodeID)
	for i := range candidates {
		ok, err := eng.EvaluateBoolWithVars(candidates[i].ConditionExpression, env)
		if err != nil || !ok {
			continue
		}
		return &candidates[i]
	}
	return nil
}

func outgoing(g *types.WorkflowGraph, nodeID string) []types.WorkflowEdge {
	var out []types.WorkflowEdge
	for _, e := range g.Edges {
		if e.FromNodeID == nodeID {
			out = append(out, e)
		}
	}
	return out
}
