---
title: Flows, loops, subagents and tools
---

# Flows, loops, subagents and tools

Every orchestration feature here is something a well-known agent framework
already does well. This page names where each idea comes from, and shows how it
looks in `agentic-golang`. The main difference is that here a workflow is
**data in Postgres** (nodes, edges and `config_json`), not code, so you can
change a flow without redeploying.

A runnable version of everything below is in
[`examples/research-team`](../examples/research-team/main.go)
(`make compose-up migrate example-agents`).

| Feature | Borrowed from | How it works here |
|---|---|---|
| Graph with conditional edges | LangGraph `StateGraph`, `add_conditional_edges` | `workflow_edges` with a CEL condition and a priority |
| Loops / cycles | LangGraph cycles, ADK `LoopAgent` | An edge back to an earlier node; bounded by `MaxIterations` / `MaxRuntimeMs` |
| Model per agent, fallbacks | ADK `model=`, OpenAI Agents SDK, CrewAI `llm=`, LiteLLM fallbacks | `model_policies` row per agent; `config_json.model` per node |
| Function tools with inferred schema | OpenAI Agents SDK `@function_tool`, ADK `FunctionTool`, Genkit, Eino | `tools.NewFunc` derives the JSON Schema from a Go struct |
| Parallel tool calls | OpenAI / Anthropic parallel tool use | All calls in one model turn run concurrently |
| Tool errors go back to the model | OpenAI Agents SDK `failure_error_function` | A failing tool returns `{"error": ...}`; the node continues |
| Subagents (sub-workflows) | LangGraph subgraphs, ADK `sub_agents`, Claude Agent SDK subagents | `subagent` node runs a child workflow in a child session |
| Agents as tools | OpenAI Agents SDK `agent.as_tool()`, ADK `AgentTool` | `Controller.AgentTool` wraps a workflow as a tool |
| Parallel fan-out | ADK `ParallelAgent`, LangGraph `Send` | `parallel` node with agent or workflow branches |
| Human in the loop | LangGraph `interrupt`, ADK tool confirmation | `human_input` node pauses with status `waiting_for_user` |
| Durable state | LangGraph checkpointers, Temporal | Every run, node attempt and session is a Postgres row |

## Model per agent

Point an agent at a model policy, and the policy at a primary model with
ordered fallbacks:

```sql
INSERT INTO model_policies (model_policy_id, primary_model, fallback_models, temperature)
VALUES ('smart', 'claude-sonnet-4-5', '["gpt-4o", "llama3.1:8b"]', 0.2);

UPDATE agent_definitions SET default_model_policy_id = 'smart' WHERE agent_definition_id = 'editor';
```

A node can override the model for one step:

```json
{ "model": "gpt-4o-mini", "fallback_models": ["llama3.1:8b"], "max_tokens": 512 }
```

Precedence: node `config_json` → agent model policy → executor default. If a
model fails, the next one is tried, and the error lists every model that failed.

## Tools

```go
type WeatherArgs struct {
	City  string `json:"city" desc:"City name, e.g. Paris"`
	Units string `json:"units,omitempty" enum:"metric,imperial"`
}

weather := tools.MustFunc("get_weather", "Current weather for a city",
	func(ctx context.Context, a WeatherArgs) (Weather, error) { /* ... */ })

toolbox, _ := tools.NewRegistry(weather)
exec := executor.NewLLMExecutor(registry, router, "llama3.1:8b", executor.WithTools(toolbox))
```

The schema comes from the struct: `json` tags give names, `omitempty` or a
pointer makes a field optional, `desc` and `enum` tags add detail.

A node is offered tools only when the node has `tool_allowed` **and** its agent
has `can_use_tools`. `config_json` can narrow the set and bound the loop:

```json
{ "tools": ["get_weather"], "max_tool_rounds": 4 }
```

The node's output gains `_tool_calls` (every call with its arguments and result
or error) and `_usage` (tokens summed over all model calls).

Tool calling works with all five providers. Each provider's wire format
(OpenAI `tool_calls`, Anthropic `tool_use` blocks, Gemini `functionCall` parts,
Ollama) is handled inside the provider.

## Subagents

A `subagent` node runs another workflow in a child session:

```json
{ "workflow_id": "wf_fact_check", "output_key": "facts" }
```

The child starts with the parent's accumulated state. When it completes, the
keys it added or changed are stored under `facts`, together with
`facts._session_id`, so the next edge can route on them:

```
facts.verified == true
```

The child session has `parent_session_id` set and its own runs and try-runs,
so you can inspect it on its own. If the child does not complete, the subagent
node fails with the child's reason. Nesting is capped at 5 levels by default
(`SetMaxSubagentDepth`).

### Agents as tools

Instead of fixing the delegation in the graph, let the model decide:

```go
translator, _ := ctrl.AgentTool("translator", "Translate text into another language", "wf_translator")
toolbox.Register(translator)
```

The model calls it with `{"task": "...", "context": {...}}`. Each call runs the
workflow as a child session of the caller and returns what it produced.

## Parallel fan-out

```json
{
  "branches": [
    { "key": "fast",    "agent_definition_id": "writer", "model": "gpt-4o-mini" },
    { "key": "careful", "agent_definition_id": "writer", "model": "claude-sonnet-4-5",
      "config": { "temperature": 0 } },
    { "key": "review",  "workflow_id": "wf_review" }
  ],
  "max_concurrency": 3,
  "fail_fast": false
}
```

- An **agent branch** runs one agent with its own model and config. It inherits
  the parallel node's `llm_allowed` / `tool_allowed` and is recorded as its own
  try-run (`<node>/<key>`).
- A **workflow branch** runs a whole child workflow as a subagent.
- Every branch gets the same input, and each result lands under its key:
  `careful.score > fast.score`.
- With `fail_fast`, the first error cancels the other branches and fails the
  node. Without it, failures are listed in `branch_errors` and the node
  succeeds as long as one branch did.

## Loops

A loop is an edge back to an earlier node:

```
work ──▶ check ──(count < 3)──▶ work
              └──(count >= 3)─▶ done
```

`MaxIterations` and `MaxRuntimeMs` on `LoopConfig` bound every run. Numbers from
node outputs (always JSON numbers) compare cleanly with integer literals in
conditions.

## Not yet

These are on the roadmap, again following existing frameworks:

- **Handoffs** (OpenAI Agents SDK): transfer the conversation to another agent
  instead of calling it and coming back.
- **MCP tools** (Claude Agent SDK, ADK): load tools from an MCP server into a
  `tools.Registry`.
- **Streaming** (most frameworks): stream tokens through the loop to
  subscribers.
- **Structured-output validation** (Pydantic AI): validate the final JSON
  against the agent's `output_schema` and ask the model to repair it.
