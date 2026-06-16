---
title: agentic-golang
---

# agentic-golang

A declarative, database-driven **multi-agent runtime in Go**. Multi-provider LLM
routing with failover, two-tier semantic caching, built-in cost/latency/quality
observability, and policy-driven retries — in one static binary, next to Postgres.

> The runtime is the interpreter; the database is the program.

## Documentation

- **[Why Go over Python (beyond speed)](why-go-over-python.md)** — the general
  engineering case for Go as an orchestration/serving layer, not a benchmark.
- **[Architecture & roadmap](architecture.md)** — components, the core flows
  (LLM reasoning, semantic cache, observability), and what's next.
- **[Prompt templates](prompt-templates.md)** — how agents become *experts on their
  task*: a generic, pluggable per-agent prompt system.
- **[Development roadmap](roadmap.md)** — the three stages (separate → usable →
  generalize) and the feature milestones.

## At a glance

```
HTTP / WS API
   │
Agentic layer:  sessions ── loopcontroller ── workflow graph
                    │             │               │
                registry      executor        edges (CEL)
   │
services:  modelrouter · semanticcache · observability · retryengine · embeddings/rag
   │
Postgres (+ pgvector)  ── single data plane
```

## Source

[github.com/yasinnerten/agentic-golang](https://github.com/yasinnerten/agentic-golang) · MIT licensed.
