// Command research-team runs a small multi-agent workflow end to end:
//
//	fanout (parallel)  ─┬─ optimist: researcher agent on model "demo-small"
//	                    └─ skeptic:  critic agent (model policy "fast")
//	fact_check (subagent) ── runs the wf_fact_check workflow in a child session
//	write (agent + tools) ── editor agent calls the word_count tool, then answers
//
// It needs only Postgres with the migrations applied (make compose-up
// migrate). The models are served by an in-process demo provider so it runs
// without API keys; swap in providers.NewOpenAIProvider etc. for real models.
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/yasinnerten/agentic-golang/agentic/agents"
	"github.com/yasinnerten/agentic-golang/agentic/executor"
	"github.com/yasinnerten/agentic-golang/agentic/loopcontroller"
	"github.com/yasinnerten/agentic-golang/agentic/memory"
	"github.com/yasinnerten/agentic-golang/agentic/sessions"
	"github.com/yasinnerten/agentic-golang/agentic/tools"
	"github.com/yasinnerten/agentic-golang/agentic/workflow"
	"github.com/yasinnerten/agentic-golang/services/modelrouter"
)

const seed = `
DELETE FROM public.workflow_definitions WHERE workflow_id IN ('wf_research_team', 'wf_fact_check');

INSERT INTO public.model_policies (model_policy_id, primary_model, fallback_models) VALUES
  ('fast',  'demo-small', '[]'),
  -- demo-large is "down" in the demo provider, so this policy shows fallback.
  ('smart', 'demo-large', '["demo-small"]')
ON CONFLICT (model_policy_id) DO NOTHING;

INSERT INTO public.agent_definitions (agent_definition_id, agent_name, agent_type, description, default_model_policy_id, can_use_llm, can_use_tools) VALUES
  ('researcher', 'Researcher', 'worker', 'Argues for the idea.',          'smart', TRUE, FALSE),
  ('critic',     'Critic',     'worker', 'Argues against the idea.',      'fast',  TRUE, FALSE),
  ('checker',    'Checker',    'worker', 'Checks the claims.',            'fast',  TRUE, FALSE),
  ('editor',     'Editor',     'worker', 'Writes the final verdict.',     'smart', TRUE, TRUE)
ON CONFLICT (agent_definition_id) DO NOTHING;

INSERT INTO public.workflow_definitions (workflow_id, workflow_name, entry_node_id) VALUES
  ('wf_fact_check',    'Fact check',    'wf_fact_check/check'),
  ('wf_research_team', 'Research team', 'wf_research_team/fanout');

INSERT INTO public.workflow_nodes (workflow_node_id, workflow_id, node_type, agent_definition_id, llm_allowed, tool_allowed, config_json) VALUES
  ('wf_fact_check/check', 'wf_fact_check', 'agent', 'checker', TRUE, FALSE, '{}'),
  ('wf_research_team/fanout', 'wf_research_team', 'parallel', NULL, TRUE, FALSE,
   '{"branches": [
      {"key": "optimist", "agent_definition_id": "researcher", "model": "demo-small"},
      {"key": "skeptic",  "agent_definition_id": "critic"}
    ]}'),
  ('wf_research_team/fact_check', 'wf_research_team', 'subagent', NULL, FALSE, FALSE,
   '{"workflow_id": "wf_fact_check", "output_key": "facts"}'),
  ('wf_research_team/write', 'wf_research_team', 'agent', 'editor', TRUE, TRUE,
   '{"tools": ["word_count"], "max_tool_rounds": 3}');

INSERT INTO public.workflow_edges (workflow_edge_id, workflow_id, from_node_id, to_node_id, condition_expression, priority) VALUES
  ('wf_research_team/e1', 'wf_research_team', 'wf_research_team/fanout',     'wf_research_team/fact_check', 'size(optimist.opinion) > 0 && size(skeptic.opinion) > 0', 10),
  ('wf_research_team/e2', 'wf_research_team', 'wf_research_team/fact_check', 'wf_research_team/write',      'facts.verified == true', 10);
`

func main() {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://agentic:agentic@localhost:5432/agentic?sslmode=disable"
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, seed); err != nil {
		log.Fatalf("seed (did you run make migrate?): %v", err)
	}

	// Models: one router, many providers. Each agent picks its model through
	// its model policy, or a node/branch overrides it.
	router := modelrouter.NewRouter()
	router.Register(demoProvider{})

	// Tools: a typed Go function; its JSON Schema comes from wordCountArgs.
	wordCount := tools.MustFunc("word_count", "Count the words in a text",
		func(_ context.Context, a wordCountArgs) (map[string]int, error) {
			return map[string]int{"words": len(strings.Fields(a.Text))}, nil
		})
	toolbox, err := tools.NewRegistry(wordCount)
	if err != nil {
		log.Fatal(err)
	}

	sm := sessions.NewManager(db)
	ctrl := loopcontroller.NewController(db, sm, memory.NewStore(db), workflow.NewBuilder(db))
	ctrl.RegisterExecutor(executor.NewLLMExecutor(agents.NewRegistry(db), router, "demo-small", executor.WithTools(toolbox)))

	sess, err := sm.Create(ctx, "demo", "research", "", nil)
	if err != nil {
		log.Fatal(err)
	}
	res, err := ctrl.RunWorkflow(ctx, sess.SessionID, "wf_research_team",
		map[string]any{"idea": "rewrite the billing service in Go"}, loopcontroller.LoopConfig{})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("\nstatus: %s (%s)\n\n", res.Status, res.Reason)
	// _model is the editor's (the last node): its "smart" policy fell back
	// from demo-large to demo-small.
	for _, key := range []string{"optimist", "skeptic", "facts", "verdict", "_model", "_tool_calls"} {
		b, _ := json.MarshalIndent(res.Output[key], "", "  ")
		fmt.Printf("%s: %s\n", key, b)
	}
}

type wordCountArgs struct {
	Text string `json:"text" desc:"The text to count"`
}

// demoProvider stands in for a real model so the example runs offline. It
// answers in character for each agent, calls word_count when that tool is
// offered, and reports "demo-large" as unavailable to exercise fallbacks.
type demoProvider struct{}

func (demoProvider) Name() string                      { return "demo" }
func (demoProvider) Models() []string                  { return []string{"demo-small", "demo-large"} }
func (demoProvider) HealthCheck(context.Context) error { return nil }

func (demoProvider) Complete(_ context.Context, req modelrouter.LLMRequest) (*modelrouter.LLMResponse, error) {
	if req.Model == "demo-large" {
		return nil, fmt.Errorf("demo-large is overloaded")
	}
	system := req.Messages[0].Content
	reply := func(v map[string]any) (*modelrouter.LLMResponse, error) {
		v["answered_by"] = req.Model
		b, _ := json.Marshal(v)
		return &modelrouter.LLMResponse{Content: string(b), Meta: modelrouter.LLMMeta{Model: req.Model}}, nil
	}

	switch {
	case strings.Contains(system, "Researcher"):
		return reply(map[string]any{"opinion": "Go gives us one static binary and cheap concurrency."})
	case strings.Contains(system, "Critic"):
		return reply(map[string]any{"opinion": "A rewrite freezes features for a quarter."})
	case strings.Contains(system, "Checker"):
		return reply(map[string]any{"verified": true, "note": "both claims are plausible"})
	case strings.Contains(system, "Editor"):
		last := req.Messages[len(req.Messages)-1]
		if len(req.Tools) > 0 && last.Role != "tool" {
			return &modelrouter.LLMResponse{ToolCalls: []modelrouter.ToolCall{{
				ID: "call_1", Name: "word_count",
				Arguments: json.RawMessage(`{"text":"Rewrite incrementally behind a facade, starting with invoicing."}`),
			}}, Meta: modelrouter.LLMMeta{Model: req.Model}}, nil
		}
		return reply(map[string]any{"verdict": "Rewrite incrementally behind a facade, starting with invoicing.", "tool_said": last.Content})
	}
	return reply(map[string]any{})
}
