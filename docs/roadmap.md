---
title: Development roadmap
---

# Development roadmap

This project is being built in **three deliberate stages**, in this order:

1. **Separate** — stand the engine up as its own repo/module that compiles on its
   own (no dependency on the private product). *In progress.*
2. **Make it usable in Lexelligence** — the private product consumes it as a Go
   module dependency, with all Lexelligence-specific content kept private (see
   [open-core overlay](#open-core-overlay-using-it-in-lexelligence)).
3. **Generalize & publish** — decouple the last product-specific seams, ship a
   neutral example domain + great docs, tag `v0.1.0`, and open it to contributors.

We treat the engine's **DB-as-program** model (a declarative workflow stored in
Postgres), the **two-tier semantic cache**, and the **rules-first / LLM-second**
execution model as the differentiators. Other Go agent frameworks (e.g.
AgenticGoKit) are builder/code-first and don't emphasize caching or a deterministic
rules layer — we borrow good ideas from them (below) without copying that shape.

## Stage A — Separate (v0.1.0 target)

- [x] Run `scripts/extract.sh` to carry the engine packages + rewrite imports.
- [x] **Decouple from the product**: replace `loopcontroller`'s websocket broadcast
      and vector storage with small interfaces so the engine is transport- and
      framework-agnostic.
- [x] `go build ./...`, `go vet ./...`, `go test ./...` all green locally.
- [x] Ship `docker-compose.yml` (Postgres + pgvector) + a `Makefile` (`migrate`,
      `test`, `example`).
- [x] Neutral, runnable **example domain** (replace any regulatory seed).

## Stage B — Usable in Lexelligence

- [ ] Lexelligence `go.mod`: `require` + local `replace => ./agentic-golang`.
- [ ] Swap Lexelligence's in-tree `internal/agentic/*` for the module import; delete
      the duplicated copies.
- [ ] Lexelligence keeps **only** its domain pieces (EU-AI-Act workflow seed, expert
      prompt content, API/auth/transport glue).
- [ ] `scripts/publish.sh` proven for the "push to both repos" flow.

## Stage C — Generalize & publish (v0.2+)

Feature milestones (several inspired by AgenticGoKit and Kunal Kushwaha's
"Building Agentic AI Systems in Go" — credited, not copied):

| Milestone | Feature | Notes / inspiration |
|---|---|---|
| v0.2 | **Structured-output validation gate** | Validate model JSON vs. the agent schema; reflective repair loop bounded by `max_iterations` |
| v0.2 | **`prompt_templates`** loader + render | Per-agent expert prompts (persona + few-shot); mechanism generic, content pluggable |
| v0.3 | **Tool calling + MCP** | Let agents call Go functions and discover tools via Model Context Protocol — *AgenticGoKit* |
| v0.3 | **Orchestration patterns** | First-class sequential / parallel / DAG / loop / sub-workflow over the existing graph — *AgenticGoKit* |
| v0.3 | **Tiered observability** | minimal / standard / detailed levels; optional **OTLP exporter** alongside the Postgres sink — *AgenticGoKit / Kushwaha* |
| v0.4 | **Mermaid workflow export** | Render a session's executed graph as a Mermaid diagram — *AgenticGoKit* |
| v0.4 | **Eval framework** | Semantic matching + LLM-as-judge with confidence scoring, building on the existing hallucination proxy — *AgenticGoKit* |
| v0.4 | **Streaming-first** | Stream tokens through the loop to subscribers — *AgenticGoKit* |
| later | **Builder API** | Optional code-first agent/workflow construction layered over the DB definitions, for users who don't want to seed SQL — *AgenticGoKit* |

## Open-core overlay (using it in Lexelligence)

The engine reads its "program" (workflows, agent definitions, prompt templates,
model/retry/cache policies) from the **database** and from an injected config
struct — it has no compiled-in domain knowledge. Lexelligence therefore supplies
its specifics **without modifying the engine**:

- Lexelligence's own private migrations/seeds provide the EU-AI-Act workflow and the
  expert prompt content.
- A thin wiring layer in Lexelligence constructs the engine services (`modelrouter`,
  `semanticcache`, `observability`, …) with product config and a concrete
  `Broadcaster`.

For local engine development that needs Lexelligence-shaped data, put it under
`agentic-golang/local/` — that path is **git-ignored in this repo** (see
`.gitignore`), so product-specific material never gets pushed to the public repo even
though you edit and push the engine from the same directory.
