---
title: Why Go over Python (beyond speed)
---

# Why Go over Python — beyond speed

A common misconception is that you reach for Go because it's "fast." Raw latency is
the *least* interesting reason. An agent runtime is a **long-lived, highly
concurrent, typed network service that mostly waits on I/O** (LLM calls, the
database, embeddings). For that shape of system, Go's advantages are about
**operability, concurrency, type safety, and stability** — not microbenchmarks.

This page makes the general case (not specific to any product).

## 1. Deployment: one static binary

Go compiles to a single, self-contained, statically-linked binary. No interpreter
to install, no virtualenv, no `pip`/`poetry`/`uv` resolution at deploy time, no
"which Python 3.x" question.

- Container images can be `FROM scratch` or distroless — often **10–25 MB** vs.
  hundreds of MB for a Python base + wheels.
- The class of "works on my machine, breaks in prod" failures from differing
  interpreter versions and transitive C-extension dependencies largely disappears.
- Cross-compile for any OS/arch from one machine with `GOOS`/`GOARCH`.

Python ships *source* that needs a matching runtime and a resolved dependency tree
present wherever it runs. That is a real, recurring operational tax.

## 2. Concurrency is a first-class primitive

This is the big one for agent systems. You are constantly doing many things at once:

- fanning out **parallel agent nodes**,
- running **provider health checks** concurrently,
- **streaming tokens** to many clients,
- doing **background work** (cache GC, session expiry) while serving requests.

Go gives you **goroutines** (cheap — a few KB of stack, millions feasible) and
**channels**/`select` for coordination, with a preemptive scheduler and **no Global
Interpreter Lock**. Concurrency reads like straight-line code.

Python's options are each awkward for this:

- **`asyncio`** — cooperative, single-threaded; `async`/`await` is "viral" (one
  blocking call stalls the loop, and async-ness propagates through your whole call
  graph); CPU-bound steps still block.
- **threads** — bounded by the GIL for CPU work; useful only for I/O.
- **multiprocessing** — real parallelism but heavyweight (process spawn,
  serialization across process boundaries).

For an orchestration layer, Go's model is simply a better fit.

## 3. Static typing and a real compiler

Go is statically typed and compiled. Whole classes of bugs — typos, wrong argument
types, missing fields, nil-shaped mistakes — are caught **before the program runs**,
and large refactors stay safe because the compiler finds every call site.

Python is dynamically typed. Pydantic and `mypy` add validation and optional static
checks — and they're excellent — but they are **bolt-ons**: type hints aren't
enforced by a compiler, runtime validation costs cycles, and coverage is only as
good as your annotations and CI discipline. In Go the type *is* the contract,
enforced by the toolchain on every build.

## 4. Built for long-lived services

Go was designed at Google to write servers. It shows:

- **Flat, predictable memory** and a low-latency garbage collector tuned for
  always-on processes — no event-loop starvation, no surprise interpreter-level
  pauses.
- **`context.Context`** is the standard, idiomatic way to thread cancellation,
  deadlines, and request scope through everything — exactly what you want for
  per-request timeouts and graceful shutdown in an agent loop.
- A standard library that already contains a production HTTP server, TLS,
  JSON, and the concurrency toolkit — fewer third-party dependencies in the
  critical path.

## 5. Operational simplicity: one language, one toolchain

When the API, the agent runtime, the background workers, and the CLIs are all Go,
the whole backend shares one mental model and one set of tools:

- `go fmt` — a single canonical format; no formatter bikeshedding.
- `go vet` and the built-in **race detector** — catch concurrency bugs early.
- Built-in testing and benchmarking; reproducible module builds.

Python's tooling is powerful but fragmented (pip/poetry/uv, black/ruff/isort,
pytest, mypy), and each project assembles its own combination.

## 6. Stability and backward compatibility

The **Go 1 compatibility promise** means code written years ago still compiles and
runs on current toolchains. Upgrades are routinely boring. The Python ecosystem,
by contrast, has lived through the 2→3 migration, churn in the async APIs, and
frequent dependency-resolution conflicts. For infrastructure you intend to operate
for years, "boring upgrades" is a feature.

## 7. Resource efficiency (the cost angle, not the speed angle)

Yes, Go is fast — but the *operationally* relevant point is **footprint**. Lower
memory and CPU per request means **higher density and lower cloud bill**: fewer and
smaller instances to serve the same load, and headroom for concurrency spikes
without thrashing. That's about money and reliability, not benchmark bragging.

## The honest counter-argument

Python **owns the AI/ML ecosystem**: model training and fine-tuning, the data-science
stack (NumPy/pandas/PyTorch), and the richest set of agent frameworks
(Pydantic AI, LangChain, LlamaIndex). If your system needs to run models *in
process* or do heavy ML, Python is the right tool and Go would be fighting upstream.

But an **agent orchestration/serving layer talks to models over HTTP APIs.** It does
not need PyTorch in memory. It needs to be a robust, concurrent, typed, easily
deployed service — and to reimplement a relatively small set of patterns (a model
router, a cache, an observability writer, a retry policy) that are well within Go's
reach.

## Rule of thumb

> **Python for the model and ML work. Go for the runtime, serving, and orchestration
> around it.**

`agentic-golang` is deliberately the second thing.
