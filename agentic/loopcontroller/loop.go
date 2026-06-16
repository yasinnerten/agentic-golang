package loopcontroller

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/yasinnerten/agentic-golang/agentic/memory"
	"github.com/yasinnerten/agentic-golang/agentic/sessions"
	"github.com/yasinnerten/agentic-golang/agentic/workflow"
	"github.com/yasinnerten/agentic-golang/services/rulesengine"
	"github.com/yasinnerten/agentic-golang/shared/types"
)

// Default loop execution limits, applied when a LoopConfig field is zero.
const (
	DefaultMaxIterations = 100
	DefaultMaxRuntimeMs  = 300000
	DefaultTimeoutMs     = 30000
)

// Controller executes workflow node loops with guards.
type Controller struct {
	db           *sql.DB
	sessions     *sessions.Manager
	memoryStore  *memory.Store
	graphBuilder *workflow.Builder
	broadcaster  EventBroadcaster
	obsRecorder  ObservabilityRecorder
	executors    []NodeExecutor
	rules        *rulesengine.Engine
}

func NewController(db *sql.DB, sm *sessions.Manager, ms *memory.Store, wb *workflow.Builder) *Controller {
	return &Controller{
		db:           db,
		sessions:     sm,
		memoryStore:  ms,
		graphBuilder: wb,
	}
}

func (c *Controller) SetBroadcaster(b EventBroadcaster) {
	c.broadcaster = b
}

// SetObservabilityRecorder wires the observability service so that every node
// execution is recorded for metrics, hallucination tracking, and usage dashboards.
func (c *Controller) SetObservabilityRecorder(r ObservabilityRecorder) {
	c.obsRecorder = r
}

// ObservabilityRecorder records node-level execution metrics (tokens, latency,
// model used) into the observability_events table. Implemented by
// observability.Service — kept as an interface here to avoid an import cycle.
type ObservabilityRecorder interface {
	RecordNodeExecution(ctx context.Context, sessionID, tenantID, loopRunID, nodeID, agentType, model string, latencyMs int)
}

// SetRules wires the CEL engine used for workflow edge condition evaluation.
func (c *Controller) SetRules(eng *rulesengine.Engine) {
	c.rules = eng
}

// StartWorkflow attaches a workflow to a session, seeds the initial loop input
// into session memory, and runs the loop. This is the entry point callers use
// to actually execute a workflow (a bare session has no workflow attached).
func (c *Controller) StartWorkflow(ctx context.Context, sessionID, workflowID string, input map[string]any, cfg LoopConfig) error {
	session, err := c.sessions.Get(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("load session: %w", err)
	}
	if workflowID != "" {
		if err := c.sessions.AttachWorkflow(ctx, sessionID, workflowID); err != nil {
			return fmt.Errorf("attach workflow: %w", err)
		}
	}
	if len(input) > 0 {
		if err := c.memoryStore.Put(ctx, session.TenantID, sessionID, "", memory.MemorySession,
			"loop_input", input, "loop_start", "", 1.0, 0); err != nil {
			return fmt.Errorf("seed loop input: %w", err)
		}
	}
	return c.Run(ctx, sessionID, cfg)
}

func (c *Controller) emit(tenantID, sessionID, eventType string, payload map[string]any) {
	if c.broadcaster != nil {
		c.broadcaster.BroadcastPipelineEvent(tenantID, sessionID, eventType, payload)
	}
}

// NodeExecutor is implemented by domain plugins to execute workflow nodes.
type NodeExecutor interface {
	CanHandle(nodeType string) bool
	Execute(ctx context.Context, node types.WorkflowNode, input map[string]any, sessionID string) (output map[string]any, err error)
}

// RegisterExecutor adds a node executor to this controller. Executors are
// matched against a node's type via CanHandle in registration order.
func (c *Controller) RegisterExecutor(e NodeExecutor) {
	c.executors = append(c.executors, e)
}

// LoopConfig controls loop execution limits.
type LoopConfig struct {
	MaxIterations int
	MaxRuntimeMs  int
	TimeoutMs     int
}

// Run starts (or resumes) the loop for a session.
func (c *Controller) Run(ctx context.Context, sessionID string, cfg LoopConfig) error {
	if c.rules == nil {
		c.rules = rulesengine.NewEngine()
	}
	if cfg.MaxIterations == 0 {
		cfg.MaxIterations = DefaultMaxIterations
	}
	if cfg.MaxRuntimeMs == 0 {
		cfg.MaxRuntimeMs = DefaultMaxRuntimeMs
	}
	if cfg.TimeoutMs == 0 {
		cfg.TimeoutMs = DefaultTimeoutMs
	}

	// Load the session first — its tenant scopes every loop/try-run row written
	// below (both tables require a non-null tenant_id).
	session, err := c.sessions.Get(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("load session: %w", err)
	}

	// Create loop run record
	loopRunID := uuid.New().String()
	_, err = c.db.ExecContext(ctx,
		`INSERT INTO public.agentic_loop_runs (loop_run_id, session_id, tenant_id, status, max_iterations, max_runtime_ms, timeout_ms, started_at)
		VALUES ($1, $2, $3, 'running', $4, $5, $6, NOW())`,
		loopRunID, sessionID, session.TenantID, cfg.MaxIterations, cfg.MaxRuntimeMs, cfg.TimeoutMs)
	if err != nil {
		return fmt.Errorf("create loop run: %w", err)
	}

	c.emit(session.TenantID, sessionID, "loop_started", map[string]any{
		"loop_run_id": loopRunID, "max_iterations": cfg.MaxIterations, "max_runtime_ms": cfg.MaxRuntimeMs,
	})

	// Load workflow graph if session has a current workflow
	var graph *types.WorkflowGraph
	if session.CurrentWorkflowID != "" {
		graph, err = c.graphBuilder.LoadGraph(ctx, session.CurrentWorkflowID)
		if err != nil {
			markStopped(c.db, ctx, loopRunID, "failed", "load graph: "+err.Error())
			return err
		}
	}

	// env is the accumulated loop state: the seeded input plus every node's
	// output, merged flat. It feeds both node input and edge condition routing
	// (e.g. classify writes "classification", which an edge out of the router
	// node then evaluates). "error" is seeded empty so error-guard edge
	// conditions compile and stay false on the happy path.
	env := map[string]any{"error": ""}
	if seeded, err := c.memoryStore.Get(ctx, sessionID, memory.MemorySession, "loop_input"); err == nil {
		if m, ok := seeded.(map[string]any); ok {
			for k, v := range m {
				env[k] = v
			}
		}
	}

	iteration := 0
	start := time.Now()
	maxDuration := time.Duration(cfg.MaxRuntimeMs) * time.Millisecond

	for {
		// Cooperative pause: if the session was paused out-of-band (POST
		// /sessions/{id}/pause), stop the loop cleanly between iterations without
		// writing decisions. Resume sets status back to 'running' and re-invokes
		// Run, which continues from the persisted current node.
		if cur, err := c.sessions.Get(ctx, sessionID); err == nil && cur.Status == "paused" {
			c.emit(session.TenantID, sessionID, "loop_paused", map[string]any{"iterations": iteration})
			markStopped(c.db, ctx, loopRunID, "paused", "paused by user")
			return nil
		}

		if iteration >= cfg.MaxIterations {
			c.emit(session.TenantID, sessionID, "loop_completed", map[string]any{"reason": "max_iterations", "iterations": iteration})
			markStopped(c.db, ctx, loopRunID, "completed", "max iterations reached")
			c.writePerTaskDecisions(ctx, session.TenantID, sessionID, env)
			return c.sessions.UpdateStatus(ctx, sessionID, "completed")
		}
		if time.Since(start) > maxDuration {
			c.emit(session.TenantID, sessionID, "loop_completed", map[string]any{"reason": "timeout", "iterations": iteration})
			markStopped(c.db, ctx, loopRunID, "timed_out", "max runtime exceeded")
			c.writePerTaskDecisions(ctx, session.TenantID, sessionID, env)
			return c.sessions.UpdateStatus(ctx, sessionID, "timed_out")
		}

		// Determine current node
		currentNodeID := session.CurrentNodeID
		if currentNodeID == "" && graph != nil {
			currentNodeID = graph.Definition.EntryNodeID
		}
		if graph == nil || currentNodeID == "" {
			markStopped(c.db, ctx, loopRunID, "completed", "no current node or graph")
			c.writePerTaskDecisions(ctx, session.TenantID, sessionID, env)
			return c.sessions.UpdateStatus(ctx, sessionID, "completed")
		}

		node, ok := graph.Nodes[currentNodeID]
		if !ok {
			markStopped(c.db, ctx, loopRunID, "failed", "node not found: "+currentNodeID)
			return fmt.Errorf("node not found: %s", currentNodeID)
		}

		log.Printf("[loop] session=%s iteration=%d node=%s type=%s", sessionID, iteration, node.WorkflowNodeID, node.NodeType)
		c.emit(session.TenantID, sessionID, "node_executing", map[string]any{
			"node_id": node.WorkflowNodeID, "node_type": node.NodeType, "iteration": iteration,
			"agent_type": node.AgentDefinitionID,
		})

		// Build minimal input (compressed state + memory + previous outputs)
		input, err := c.buildNodeInput(ctx, sessionID, node, env)
		if err != nil {
			markStopped(c.db, ctx, loopRunID, "failed", "build input: "+err.Error())
			return err
		}

		// Execute try-run wrapper
		tryRun := &TryRun{
			TryRunID:       uuid.New().String(),
			LoopRunID:      loopRunID,
			SessionID:      sessionID,
			TenantID:       session.TenantID,
			WorkflowNodeID: node.WorkflowNodeID,
			AttemptNumber:  1,
			Status:         "started",
			StartedAt:      time.Now(),
		}
		output, execErr := c.executeNodeWithTryRun(ctx, tryRun, node, input, sessionID)

		if execErr != nil {
			log.Printf("[loop] node execution failed: %v", execErr)
			c.emit(session.TenantID, sessionID, "node_error", map[string]any{
				"node_id": node.WorkflowNodeID, "error": execErr.Error(),
				"agent_type": node.AgentDefinitionID,
			})
			// For Phase 2 skeleton: mark failed and stop
			markStopped(c.db, ctx, loopRunID, "failed", execErr.Error())
			return c.sessions.UpdateStatus(ctx, sessionID, "failed")
		}

		// Store output in memory and fold it into the accumulated env so later
		// nodes and edge conditions can see it.
		if output != nil {
			_ = c.memoryStore.Put(ctx, session.TenantID, sessionID, "", memory.MemoryShortTerm,
				"output_"+node.WorkflowNodeID, output, "node_execution", node.WorkflowNodeID,
				1.0, 0)
			for k, v := range output {
				env[k] = v
			}
			c.emit(session.TenantID, sessionID, "node_completed", map[string]any{
				"node_id": node.WorkflowNodeID, "output_keys": keysOf(output),
				"agent_type": node.AgentDefinitionID,
			})
		}

		// Select next edge using the accumulated env for condition evaluation.
		nextEdge := workflow.SelectNextEdge(c.rules, graph, node.WorkflowNodeID, env)
		if nextEdge == nil {
			markStopped(c.db, ctx, loopRunID, "completed", "no outgoing edges")
			c.writePerTaskDecisions(ctx, session.TenantID, sessionID, env)
			return c.sessions.UpdateStatus(ctx, sessionID, "completed")
		}

		// Update session to next node
		nextNodeID := nextEdge.ToNodeID
		if err := c.sessions.UpdateCurrentNode(ctx, sessionID, session.CurrentWorkflowID, nextNodeID); err != nil {
			markStopped(c.db, ctx, loopRunID, "failed", "update node: "+err.Error())
			return err
		}
		session.CurrentNodeID = nextNodeID

		// Update loop run
		_, _ = c.db.ExecContext(ctx,
			`UPDATE public.agentic_loop_runs SET iteration_count = $1, current_node_id = $2, updated_at = NOW() WHERE loop_run_id = $3`,
			iteration+1, nextNodeID, loopRunID)

		iteration++

		// Pause for human input if node requires it
		if node.NodeType == "human_input" {
			markStopped(c.db, ctx, loopRunID, "waiting_for_user", "node requires human input")
			return c.sessions.UpdateStatus(ctx, sessionID, "waiting_for_user")
		}
	}
}

func (c *Controller) buildNodeInput(ctx context.Context, sessionID string, node types.WorkflowNode, env map[string]any) (map[string]any, error) {
	input := map[string]any{
		"node_id":     node.WorkflowNodeID,
		"node_type":   node.NodeType,
		"node_name":   node.NodeName,
		"session_id":  sessionID,
		"accumulated": env,
	}
	// Load latest compressed state
	state, err := c.sessions.LoadLatestCompressedState(ctx, sessionID)
	if err == nil && state != nil {
		input["state"] = state
	}
	// Load previous node outputs from short-term memory
	items, _ := c.memoryStore.Query(ctx, types.MemoryScope{SessionID: sessionID, MemoryType: string(memory.MemoryShortTerm), Limit: 10})
	if len(items) > 0 {
		var prev []map[string]any
		for _, it := range items {
			var v map[string]any
			_ = json.Unmarshal(it.ValueJSON, &v)
			prev = append(prev, v)
		}
		input["previous_outputs"] = prev
	}
	return input, nil
}

func (c *Controller) executeNodeWithTryRun(ctx context.Context, tr *TryRun, node types.WorkflowNode, input map[string]any, sessionID string) (map[string]any, error) {
	// Insert try-run record
	inJSON, _ := json.Marshal(input)
	_, _ = c.db.ExecContext(ctx,
		`INSERT INTO public.try_run_events (try_run_id, loop_run_id, session_id, tenant_id, workflow_node_id, attempt_number, status, input_json, started_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		tr.TryRunID, tr.LoopRunID, tr.SessionID, tr.TenantID, tr.WorkflowNodeID, tr.AttemptNumber, tr.Status, inJSON, tr.StartedAt)

	// Find executor. A missing executor is a hard failure: silently mocking a
	// successful output would let a misconfigured workflow report completion.
	var output map[string]any
	var err error
	handled := false
	for _, ex := range c.executors {
		if ex.CanHandle(node.NodeType) {
			output, err = ex.Execute(ctx, node, input, sessionID)
			handled = true
			break
		}
	}
	if !handled {
		err = fmt.Errorf("no executor registered for node type %q", node.NodeType)
	}

	// Record completion
	outJSON, _ := json.Marshal(output)
	status := "completed"
	if err != nil {
		status = "failed"
	}
	latency := int(time.Since(tr.StartedAt).Milliseconds())
	_, _ = c.db.ExecContext(ctx,
		`UPDATE public.try_run_events SET status = $1, output_json = $2, completed_at = NOW(), latency_ms = $3 WHERE try_run_id = $4`,
		status, outJSON, latency, tr.TryRunID)

	// Record observability event for metrics/usage dashboards
	if c.obsRecorder != nil {
		model := ""
		if output != nil {
			if m, ok := output["_model"].(string); ok {
				model = m
			}
		}
		c.obsRecorder.RecordNodeExecution(ctx, tr.SessionID, tr.TenantID, tr.LoopRunID,
			tr.WorkflowNodeID, node.AgentDefinitionID, model, latency)
	}

	return output, err
}

// writePerTaskDecisions implements D12: after a chapter session loop completes,
// iterate all tasks bound to the session and write per-task decisions. The
// accumulated env map from the loop is examined for a top-level "decision" or
// per-task decisions in "task_decisions" (map[task_id]decision).
//
// Correctness: a task is only marked completed+decided when the loop actually
// produced a decision for it. If no decision exists (e.g. the loop terminated
// early without reaching an evaluator node, or the session had no workflow),
// the task is moved to "needs_review" with a NULL decision rather than
// fabricated as compliant. Fabricating "comply" for work that never ran would
// silently fake compliance results.
func (c *Controller) writePerTaskDecisions(ctx context.Context, tenantID, sessionID string, env map[string]any) {
	schemaName := dbTenantSchema(tenantID)
	quotedSchema := fmt.Sprintf(`"%s"`, schemaName)

	rows, err := c.db.QueryContext(ctx, fmt.Sprintf(
		`SELECT task_id FROM %s.tasks WHERE session_id = $1`, quotedSchema,
	), sessionID)
	if err != nil {
		log.Printf("[loop] writePerTaskDecisions: query tasks: %v", err)
		return
	}
	defer rows.Close()

	globalDecision, _ := env["decision"].(string)
	perTask, _ := env["task_decisions"].(map[string]any)

	// If no explicit "decision" was emitted by a workflow node, try to derive
	// one from the evaluator's findings or overall_status. The evaluator node
	// (node_eval_hazardous) outputs {"findings": [...], "risk_level": "..."}
	// or {"overall_status": "compliant"|"partially_compliant"|"non_compliant"}.
	// Without this bridge, tasks always land in needs_review because no node
	// ever sets env["decision"] directly.
	if globalDecision == "" {
		globalDecision = deriveDecisionFromEnv(env)
	}

	resultSummary := extractResultSummary(env)
	if resultSummary == "" {
		resultSummary = deriveSummaryFromEnv(env)
	}
	// Whether the loop actually executed at least one node for this session.
	// If it didn't, there is no basis for any decision.
	didWork := c.sessionProducedWork(ctx, sessionID)

	for rows.Next() {
		var taskID string
		if err := rows.Scan(&taskID); err != nil {
			continue
		}

		decision := globalDecision
		if pt, ok := perTask[taskID].(string); ok && pt != "" {
			decision = pt
		}

		if decision == "" {
			// No decision was actually produced. Do not fabricate compliance.
			if !didWork {
				// Loop never executed a node — leave the task untouched so it
				// can be re-run later. Writing "completed/comply" here would
				// be a fabricated result.
				continue
			}
			// Loop ran but didn't reach a decision for this task — flag it
			// for human review instead of faking compliance.
			_, err := c.db.ExecContext(ctx, fmt.Sprintf(
				`UPDATE %s.tasks SET task_status = 'needs_review', decision = NULL, result_summary = $1, updated_at = NOW() WHERE task_id = $2`,
				quotedSchema,
			), coalesceStr(resultSummary, "loop completed without a decision"), taskID)
			if err != nil {
				log.Printf("[loop] writePerTaskDecisions: update task %s: %v", taskID, err)
			}
			c.emit(tenantID, sessionID, "task_updated", map[string]any{
				"task_id": taskID, "status": "needs_review", "decision": nil,
			})
			continue
		}

		// A real decision was produced — persist it as the single source of
		// truth. task_status='completed'; comply vs noncomply lives in decision.
		_, err := c.db.ExecContext(ctx, fmt.Sprintf(
			`UPDATE %s.tasks SET task_status = 'completed', decision = $1, result_summary = $2, updated_at = NOW() WHERE task_id = $3`,
			quotedSchema,
		), decision, resultSummary, taskID)
		if err != nil {
			log.Printf("[loop] writePerTaskDecisions: update task %s: %v", taskID, err)
		}

		c.emit(tenantID, sessionID, "task_updated", map[string]any{
			"task_id": taskID, "status": "completed", "decision": decision,
		})
	}
}

// sessionProducedWork reports whether at least one try_run_event was recorded
// for the session — i.e. the loop actually executed a workflow node. Decisions
// should only be written when work happened.
func (c *Controller) sessionProducedWork(ctx context.Context, sessionID string) bool {
	var n int
	err := c.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM public.try_run_events WHERE session_id = $1`,
		sessionID).Scan(&n)
	if err != nil {
		return false
	}
	return n > 0
}

func coalesceStr(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}

func extractResultSummary(env map[string]any) string {
	if s, ok := env["result_summary"].(string); ok {
		return s
	}
	if s, ok := env["rationale"].(string); ok {
		return s
	}
	if s, ok := env["summary"].(string); ok {
		return s
	}
	return ""
}

func dbTenantSchema(tenantID string) string {
	return "tenant_" + tenantID
}

func markStopped(db *sql.DB, ctx context.Context, loopRunID, status, reason string) {
	_, err := db.ExecContext(ctx,
		`UPDATE public.agentic_loop_runs SET status = $1, stop_reason = $2, completed_at = NOW(), updated_at = NOW() WHERE loop_run_id = $3`,
		status, reason, loopRunID)
	if err != nil {
		log.Printf("[loop] failed to mark loop run stopped: %v", err)
	}
}

// TryRun mirrors the try_run_events row for in-flight tracking.
type TryRun struct {
	TryRunID       string
	LoopRunID      string
	SessionID      string
	TenantID       string
	WorkflowNodeID string
	AttemptNumber  int
	Status         string
	StartedAt      time.Time
}

func keysOf(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// deriveDecisionFromEnv attempts to infer a comply/noncomply decision from the
// evaluator node's output when no node explicitly set env["decision"].
//
// The evaluator (node_eval_hazardous) outputs:
//   - overall_status: "compliant" | "partially_compliant" | "non_compliant"
//   - findings: [ { status: "Compliant" | "Non-Compliant", ... } ]
//   - risk_level: "Low" | "Medium" | "High"
//
// Mapping rules:
//   - overall_status = "non_compliant" or any finding has status containing "non" → "noncomply"
//   - overall_status = "compliant" or all findings are compliant → "comply"
//   - anything else (partially_compliant, or no evaluator output) → "" (no fabrication)
func deriveDecisionFromEnv(env map[string]any) string {
	// Check overall_status first — most authoritative.
	if overall, ok := env["overall_status"].(string); ok && overall != "" {
		switch strings.ToLower(strings.ReplaceAll(overall, "-", "_")) {
		case "non_compliant", "noncompliant", "fail":
			return "noncomply"
		case "compliant", "pass":
			return "comply"
		}
	}

	// Fall back to scanning findings array.
	findings, ok := env["findings"].([]any)
	if !ok || len(findings) == 0 {
		return ""
	}

	hasNonCompliant := false
	allCompliant := true
	for _, f := range findings {
		fm, ok := f.(map[string]any)
		if !ok {
			continue
		}
		status, _ := fm["status"].(string)
		statusLower := strings.ToLower(status)
		if strings.Contains(statusLower, "non") || strings.Contains(statusLower, "fail") {
			hasNonCompliant = true
			allCompliant = false
		} else if !isExplicitlyCompliant(statusLower) {
			// Ambiguous status (e.g. "INCOMPLETE", "PENDING", "UNKNOWN") —
			// don't claim compliance.
			allCompliant = false
		}
	}

	if hasNonCompliant {
		return "noncomply"
	}
	if allCompliant {
		return "comply"
	}
	// Mixed or ambiguous findings — don't fabricate a decision.
	return ""
}

// isExplicitlyCompliant returns true only when the status string clearly
// indicates compliance (not just absence of non-compliance).
func isExplicitlyCompliant(statusLower string) bool {
	for _, s := range []string{"compliant", "comply", "pass", "complete", "met"} {
		if strings.Contains(statusLower, s) {
			return true
		}
	}
	return false
}

// deriveSummaryFromEnv builds a human-readable result summary from evaluator
// output when the evaluator didn't emit a dedicated summary field.
func deriveSummaryFromEnv(env map[string]any) string {
	var parts []string

	if overall, ok := env["overall_status"].(string); ok && overall != "" {
		parts = append(parts, "Overall: "+overall)
	}

	if risk, ok := env["risk_level"].(string); ok && risk != "" {
		parts = append(parts, "Risk: "+risk)
	}

	if findings, ok := env["findings"].([]any); ok && len(findings) > 0 {
		compliant, nonCompliant := 0, 0
		for _, f := range findings {
			fm, ok := f.(map[string]any)
			if !ok {
				continue
			}
			status, _ := fm["status"].(string)
			sl := strings.ToLower(status)
			if strings.Contains(sl, "non") || strings.Contains(sl, "fail") {
				nonCompliant++
			} else {
				compliant++
			}
		}
		parts = append(parts, fmt.Sprintf("%d compliant, %d non-compliant findings", compliant, nonCompliant))
	}

	return strings.Join(parts, "; ")
}
