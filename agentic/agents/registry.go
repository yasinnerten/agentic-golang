package agents

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/yasinnerten/agentic-golang/shared/types"
)

// Registry loads agent definitions from the DB and creates instances.
type Registry struct {
	db *sql.DB
}

// NewRegistry creates an agent registry backed by the public schema.
func NewRegistry(db *sql.DB) *Registry {
	return &Registry{db: db}
}

// ListDefinitions returns active agent definitions, optionally filtered by domain/framework.
func (r *Registry) ListDefinitions(ctx context.Context, domainID, frameworkID string) ([]types.AgentDefinition, error) {
	query := `SELECT agent_definition_id, agent_name, agent_type, domain_id, framework_id,
		description, input_schema_json, output_schema_json, default_model_policy_id,
		default_prompt_template_id, can_use_llm, can_use_tools, can_spawn_subagents,
		can_run_parallel, max_iterations, max_runtime_ms, timeout_ms,
		retry_policy_id, error_policy_id, cache_policy_id, memory_policy_id,
		is_active, created_at, updated_at
		FROM public.agent_definitions WHERE is_active = true`
	args := []any{}
	if domainID != "" {
		query += " AND domain_id = $1"
		args = append(args, domainID)
	}
	if frameworkID != "" {
		query += fmt.Sprintf(" AND framework_id = $%d", len(args)+1)
		args = append(args, frameworkID)
	}
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query definitions: %w", err)
	}
	defer rows.Close()

	var defs []types.AgentDefinition
	for rows.Next() {
		var d types.AgentDefinition
		if err := rows.Scan(&d.AgentDefinitionID, &d.AgentName, &d.AgentType, &d.DomainID, &d.FrameworkID,
			&d.Description, &d.InputSchemaJSON, &d.OutputSchemaJSON, &d.DefaultModelPolicyID,
			&d.DefaultPromptTemplateID, &d.CanUseLLM, &d.CanUseTools, &d.CanSpawnSubagents,
			&d.CanRunParallel, &d.MaxIterations, &d.MaxRuntimeMs, &d.TimeoutMs,
			&d.RetryPolicyID, &d.ErrorPolicyID, &d.CachePolicyID, &d.MemoryPolicyID,
			&d.IsActive, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, err
		}
		defs = append(defs, d)
	}
	return defs, rows.Err()
}

// GetDefinition loads a single agent definition by ID.
func (r *Registry) GetDefinition(ctx context.Context, id string) (*types.AgentDefinition, error) {
	query := `SELECT agent_definition_id, agent_name, agent_type, domain_id, framework_id,
		description, input_schema_json, output_schema_json, default_model_policy_id,
		default_prompt_template_id, can_use_llm, can_use_tools, can_spawn_subagents,
		can_run_parallel, max_iterations, max_runtime_ms, timeout_ms,
		retry_policy_id, error_policy_id, cache_policy_id, memory_policy_id,
		is_active, created_at, updated_at
		FROM public.agent_definitions WHERE agent_definition_id = $1`
	var d types.AgentDefinition
	err := r.db.QueryRowContext(ctx, query, id).Scan(
		&d.AgentDefinitionID, &d.AgentName, &d.AgentType, &d.DomainID, &d.FrameworkID,
		&d.Description, &d.InputSchemaJSON, &d.OutputSchemaJSON, &d.DefaultModelPolicyID,
		&d.DefaultPromptTemplateID, &d.CanUseLLM, &d.CanUseTools, &d.CanSpawnSubagents,
		&d.CanRunParallel, &d.MaxIterations, &d.MaxRuntimeMs, &d.TimeoutMs,
		&d.RetryPolicyID, &d.ErrorPolicyID, &d.CachePolicyID, &d.MemoryPolicyID,
		&d.IsActive, &d.CreatedAt, &d.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("get definition: %w", err)
	}
	return &d, nil
}

// CreateInstance inserts a new agent runtime instance.
func (r *Registry) CreateInstance(ctx context.Context, defID, sessionID, name string) (*types.AgentInstance, error) {
	id := "inst_" + name + "_" + fmt.Sprintf("%d", time.Now().UnixNano())
	query := `INSERT INTO public.agent_instances (agent_instance_id, agent_definition_id, session_id, instance_name, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, 'pending', NOW(), NOW())
		RETURNING agent_instance_id, agent_definition_id, session_id, instance_name, status, current_task_id, current_node_id, created_at, updated_at`
	var i types.AgentInstance
	err := r.db.QueryRowContext(ctx, query, id, defID, sessionID, name).Scan(
		&i.AgentInstanceID, &i.AgentDefinitionID, &i.SessionID, &i.InstanceName,
		&i.Status, &i.CurrentTaskID, &i.CurrentNodeID, &i.CreatedAt, &i.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("create instance: %w", err)
	}
	return &i, nil
}
