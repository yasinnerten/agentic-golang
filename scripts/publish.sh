#!/usr/bin/env bash
# publish.sh — push the engine change to BOTH repos in one go.
#
# Model: this public repo (agentic-golang) is the source of truth for the engine;
# the private product (Lexelligence) consumes it as a Go module dependency. During
# local dev, Lexelligence's go.mod has:
#
#     require github.com/yasinnerten/agentic-golang v0.x.y
#     replace github.com/yasinnerten/agentic-golang => ./agentic-golang
#
# Flow:
#   1) commit + push the public engine repo (and tag if --tag vX.Y.Z given)
#   2) in the private repo, bump the dependency and commit + push
#
# Usage: scripts/publish.sh "commit message" [--tag v0.1.0]
set -euo pipefail

MSG="${1:?usage: publish.sh \"message\" [--tag vX.Y.Z]}"
TAG=""
[ "${2:-}" = "--tag" ] && TAG="${3:?--tag needs a version}"

MODULE="github.com/yasinnerten/agentic-golang"
PUB="$(cd "$(dirname "$0")/.." && pwd)"
LEX_ROOT="${LEX_ROOT:-$(dirname "$PUB")}"   # private repo = parent dir by default

echo "==> [1/2] Public repo: $PUB"
( cd "$PUB"
  git add -A
  git commit -m "$MSG" || echo "    (nothing to commit)"
  git push origin main
  if [ -n "$TAG" ]; then
    git tag "$TAG" && git push origin "$TAG"
    echo "    tagged $TAG"
  fi
)

echo "==> [2/2] Private repo: $LEX_ROOT"
( cd "$LEX_ROOT"
  if [ -n "$TAG" ]; then
    go get "$MODULE@$TAG"
  else
    go mod tidy
  fi
  git add -A
  git commit -m "chore: sync agentic-golang — $MSG" || echo "    (nothing to commit)"
  git push
)

echo "==> Done. Pushed to both repos."
