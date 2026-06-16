# agentic-golang
Opensourced go based agentic operation system, ready-to-use for my personal systems and development of the agentic products. A declarative, database-driven multi-agent runtime in Go; multi-provider LLM routing with failover, two-tier semantic caching, built-in cost/latency observability, and policy-driven retries. No Python, single binary.

## Run

```bash
go run ./cmd/agentic --db /tmp/agentic.db --agent demo --input "hello" --run-once
```

## What is implemented

- Declarative, database-driven runtime tables for agents, tasks, and semantic cache.
- Multi-provider routing with provider failover and policy-driven retries.
- Two-tier semantic caching:
  - L1 in-memory semantic cache.
  - L2 SQLite-backed semantic cache.
- Built-in observability persisted per task: provider, cache tier, attempts, cost, and latency.
