---
title: Architecture & roadmap
---

# Architecture & roadmap

## Overview

`agentic-golang` interprets a **declarative, DB-stored workflow** (nodes + edges +
agent definitions) as a running multi-agent process. A node only calls a model when
its definition allows it (`can_use_llm`); otherwise it resolves deterministically —
**rules-first, LLM-second**.

```
                         ┌──────────────────────────────────────────┐
                         │              HTTP / WS API                 │
                         └───────────────┬────────────────────────────┘
                                         │
                 ┌───────────────────────┼────────────────────────────┐
                 │                 Agentic layer                       │
                 │   sessions ── loopcontroller ── workflow graph      │
                 │       │             │                │              │
                 │   registry      executor         edges (CEL)        │
                 │   (agent defs)  (node → result)   conditions        │
                 └─────────────────────┼────────────────────────────────┘
                                       │
        ┌──────────────┬───────────────┼───────────────┬──────────────┐
   modelrouter    semanticcache   observability    retryengine   embeddings/RAG
  (5 providers,   (hash + vector   (tokens, cost,   (policy-      (pgvector)
   failover)       similarity)      latency, …)      driven)
                                       │
                         Postgres (+ pgvector) — single data plane
```

## Components

| Package | Responsibility |
|---|---|
| `agentic/sessions` | Session lifecycle (create / attach workflow / status / expire / close) |
| `agentic/agents` | Registry: load agent definitions + create runtime instances |
| `agentic/workflow` | The graph (nodes, edges) the loop walks |
| `agentic/loopcontroller` | The driving loop: pick node → execute → evaluate edges → advance |
| `agentic/executor` | Turns a node into a result; rules-first, LLM-second |
| `agentic/memory` · `reviews` | Working memory; human-review gate |
| `services/modelrouter` | Unified LLM interface over 5 providers, health-aware failover |
| `services/semanticcache` | Exact (SHA-256) + semantic (pgvector) cache |
| `services/observability` | Per-node events: tokens, cost, latency, cache hits, hallucination proxy |
| `services/retryengine` | Policy-driven retry (exponential / linear / fixed + jitter) |
| `services/embeddings` · `services/rag` | Embeddings, chunking, extraction, pgvector search |
| `services/rulesengine` | Deterministic rule evaluation feeding the rules-first path |

## Core flows

### LLM reasoning (rules-first, LLM-second)

The loop loads a node, checks whether the LLM is permitted, and only then calls the
router. The executor requests a JSON response matching the agent's `output_schema`,
parses it, and the parsed fields feed **CEL edge conditions** that pick the next node.

### Two-tier semantic cache

1. **Exact** — SHA-256 of the input; instant hit.
2. **Semantic** — embed the input, run a pgvector similarity search, accept the best
   match above a configurable threshold.

Repeated reasoning over the same inputs does not re-pay for an LLM call.

### Observability (homegrown, no SaaS in the path)

Every node execution writes a row with model, token counts, cost, latency, retry
count, cache hits, a confidence value, and a hallucination-risk proxy — queryable
directly in Postgres next to your domain data.

## Roadmap

| Phase | Work | Outcome |
|---|---|---|
| 1 | **Structured-output validation gate** in `executor`: validate model output against the agent's JSON schema; on failure, feed the error back and retry (bounded by `max_iterations`) | Malformed-output failures eliminated |
| 2 | **`prompt_templates`** schema + loader + executor wiring (see [prompt-templates.md](prompt-templates.md)) | Per-agent expert prompts possible |
| 3 | Author + ship a small **generic example domain** (replace any proprietary seed) | Runnable out of the box |
| 4 | Wire validation events + prompt versions into `observability` | Data-driven prompt iteration |
| 5 | Optional **OpenTelemetry** export from `observability` | Interop with Langfuse/Logfire/Grafana |
| 6 | Decouple `loopcontroller` broadcasting from any specific transport via an interface | Clean public API, no web-framework leak into the engine |

## Extraction note

This module was lifted out of a private product. The engine packages depend only on
a shared `types` package, each other, and two third-party libraries (`uuid`,
`go-chi`). Couplings to the original product's web transport and DB-connection
helper are being replaced with small interfaces (Phase 6) so the engine stays
transport- and framework-agnostic.
