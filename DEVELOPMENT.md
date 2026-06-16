# Development & dual-repo workflow

This module (`agentic-golang`, public) is the **source of truth** for the generic
agent engine. The private product (**Lexelligence**) consumes it as a **Go module
dependency**. This file is the operator's guide for both.

During bootstrap the public repo lives *inside* the Lexelligence working tree at
`./agentic-golang/` and is **git-ignored by the parent** so it is never committed
twice. It has its own git history and its own remote.

## One-time bootstrap

```bash
# from the Lexelligence root
cd agentic-golang

# 1) carry the engine packages out of the private repo + rewrite imports
LEX_ROOT=.. scripts/extract.sh
# review the "needs manual decoupling" list it prints, fix those, then:
go build ./...
go test ./...

# 2) git is already initialised here and the remote already points at the
#    existing repo: git@github.com:yasinnerten/agentic-golang.git
git add -A
git commit -m "chore: initial extraction of agentic engine"
git push -u origin main

# 3) enable GitHub Pages (Settings ▸ Pages ▸ Source: GitHub Actions)
#    the .github/workflows/pages.yml workflow then publishes docs/ to
#    https://yasinnerten.github.io/agentic-golang
```

### Wire Lexelligence to depend on it

In the **Lexelligence** `go.mod`:

```go
require github.com/yasinnerten/agentic-golang v0.1.0

// local dev: use the in-tree copy instead of the published version
replace github.com/yasinnerten/agentic-golang => ./agentic-golang
```

Then replace the in-product `internal/agentic/*` (and the carried `services/*`)
import paths with `github.com/yasinnerten/agentic-golang/*`, and delete the
now-duplicated private copies. Lexelligence keeps **only its domain-specific
pieces**: the EU AI Act workflow seed, the domain prompt templates, and the
`internal/api`/`platform` glue.

## Day-to-day: change the engine, push both

```bash
# edit engine code under agentic-golang/, then:
cd agentic-golang
scripts/publish.sh "feat: add structured-output validation gate" --tag v0.2.0
```

`publish.sh` will:
1. commit + push (+ tag) the public repo, then
2. in Lexelligence, `go get github.com/yasinnerten/agentic-golang@v0.2.0`, commit, push.

Omit `--tag` for a quick sync that just runs `go mod tidy` against the local
`replace`.

## What stays generic vs. specific

| Concern | Public `agentic-golang` | Private Lexelligence |
|---|---|---|
| Engine (loop, registry, executor, services) | ✅ source of truth | imports as dependency |
| `prompt_templates` **mechanism** (table, loader, render) | ✅ generic | uses it |
| `prompt_templates` **content** (expert prompts) | one neutral example only | ✅ EU-AI-Act-specific seeds (kept private) |
| Domain workflow seed | small generic example | ✅ real regulatory workflow |
| Web/API/transport glue | interfaces only | ✅ concrete (chi, ws, auth) |

Regulated or proprietary prompt/seed content **never** ships in the public repo.

## Roadmap

See [docs/architecture.md](docs/architecture.md). Near-term: (1) structured-output
validation gate, (2) `prompt_templates` loader + executor wiring, (3) a runnable
generic example domain, (4) decouple the loop's broadcasting behind an interface.
