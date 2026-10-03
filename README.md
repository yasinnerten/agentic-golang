# agentic-golang

**A declarative, database-driven multi-agent runtime in Go.**

Multi-provider LLM routing with health-aware failover · two-tier semantic caching ·
built-in cost/latency/quality observability · policy-driven retries · a
rules-first / LLM-second execution model. **No Python. Single static binary.**

> 📖 Full documentation & design notes: **https://yasinnerten.github.io/agentic-golang**

---

## What it is

`agentic-golang` turns a **workflow stored in your database** — nodes, edges, and
agent definitions — into a running multi-agent process. Each agent classifies,
routes, collects, evaluates, or generates, and a loop walks the graph evaluating
edge conditions to decide what runs next.

The defining principle is **rules-first, LLM-second**: a node only calls a model
when its definition explicitly allows it; otherwise it resolves deterministically.
Everything an agent *is* — its input/output JSON schemas, its model/prompt/retry/
cache/memory policies, and its capability flags — lives in the database, not in
code. **The runtime is the interpreter; the database is the program.**

In short, it is a Go-native alternative to the Python "Pydantic AI + Langfuse"
stack, with the model router, semantic cache, and observability all living in one
binary next to Postgres.

## Why Go (and not Python)?

We did **not** choose Go for raw speed. We chose it because an *agent
orchestration and serving layer* is a long-lived, highly-concurrent, typed network
service — exactly Go's home turf — while Python's strength (the ML/training
ecosystem) isn't needed in-process when you talk to models over HTTP.

- **Single static binary** — no interpreter, no virtualenv, no runtime dependency
  resolution. Tiny `FROM scratch`/distroless images; "works on my machine" drift
  disappears.
- **Concurrency as a first-class primitive** — goroutines + channels fit fan-out
  agent nodes, parallel provider health checks, token streaming, and background
  GC naturally. No GIL, no viral `async`.
- **Static typing + a real compiler** — whole classes of errors caught at build
  time; large refactors stay safe. The type *is* the contract.
- **Built for long-lived services** — flat, predictable memory; graceful shutdown
  via `context`; designed for always-on daemons.
- **One language, one toolchain** — API, runtime, workers, and CLIs share
  `go fmt`, `go vet`, the race detector, and built-in testing.
- **Stability** — the Go 1 compatibility promise; code keeps building for years.

The honest rule of thumb: **Python for the model/ML work; Go for the runtime and
orchestration around it.** Full argument: [docs/why-go-over-python.md](docs/why-go-over-python.md).

## Features

| Area | What you get |
|---|---|
| Flows & loops | DB-stored workflow graph, CEL edge conditions, cycles bounded by iteration/runtime limits |
| Subagents | `subagent` nodes run child workflows in child sessions; any workflow can also be exposed to a model as a tool |
| Parallelism | `parallel` nodes fan out to agents or workflows concurrently, each with its own model |
| Models | Model per agent (policy) or per node, with ordered fallbacks; 5 providers (Anthropic, Azure, Gemini, OpenAI, Ollama) |
| Tools | Typed Go functions as tools (schema derived from the struct), parallel tool calls, errors fed back to the model |
| Agents | Declarative definitions with JSON I/O schemas and capability flags; rules-first, LLM only where allowed |
| Durability | Every session, run and node attempt is a Postgres row; runs pause for human input and resume |
| Observability | Per-node model, latency, token usage and a full tool-call trace *(cost tracking: roadmap)* |
| Cache | Exact-match (SHA-256) result cache *(semantic tier and executor wiring: roadmap)* |
| Reliability | Retry policies (exponential / linear / fixed + jitter) *(not yet wired into the loop)* |
| Structured output | Schema-validated model output with a reflective repair loop *(roadmap — see docs)* |
| Expert prompts | Per-agent `prompt_templates` (persona + few-shot), generic and pluggable *(roadmap)* |

How each of these maps to LangGraph, Google ADK, the OpenAI Agents SDK and
others, with config examples: **[docs/patterns.md](docs/patterns.md)**.

## Where it can be used

The engine is domain-agnostic. Natural fits:

- **Regulatory / compliance assessment** (its origin) — classify a subject, collect
  evidence, evaluate it against a checklist, generate a report.
- **Document/RAG pipelines** — ingest → chunk → embed → retrieve → reason → summarize.
- **Multi-step decisioning** — underwriting, triage, KYC/onboarding, content
  moderation: branch on rules first, call an LLM only at the ambiguous nodes.
- **Back-office automation** — ticket routing, data extraction + validation, report
  generation, with human-review gates for low-confidence results.

## Quick start

```bash
git clone https://github.com/yasinnerten/agentic-golang
cd agentic-golang
docker compose up -d        # Postgres + pgvector
make migrate                # create the schema
make test                   # unit tests
make test-integration       # + Postgres integration tests
make example                # CEL routing, no database needed
make example-agents         # parallel agents, a subagent and a tool, end to end
```

## Status

Early/extracted. The engine was first built inside a private product
(Lexelligence) and now builds as an independent Go module. See the roadmap in
[docs/architecture.md](docs/architecture.md). Issues and PRs welcome.

## License

MIT — see [LICENSE](LICENSE).
