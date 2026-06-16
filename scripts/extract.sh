#!/usr/bin/env bash
# extract.sh — carry the agentic engine packages out of the private product
# (Lexelligence) into this public module, rewriting import paths.
#
# Source layout (private):  lex-backend/internal/{agentic,services/*,shared/types}
# Target layout (public):   github.com/yasinnerten/agentic-golang/{agentic,services/*,shared/types}
#
# Packages are intentionally placed at the MODULE ROOT (not under internal/) so
# external consumers can import them. Run from the repo root: scripts/extract.sh
set -euo pipefail

MODULE="github.com/yasinnerten/agentic-golang"
LEX_ROOT="${LEX_ROOT:-..}"                # private repo root (parent by default)
SRC="$LEX_ROOT/internal"
HERE="$(cd "$(dirname "$0")/.." && pwd)"

# Engine packages to carry (self-contained: depend only on shared/types + each
# other + uuid/go-chi).
PKGS=(
  "agentic"
  "services/modelrouter"
  "services/semanticcache"
  "services/observability"
  "services/retryengine"
  "services/embeddings"
  "services/rag"
  "services/rulesengine"
  "shared/types"
)

TEST_EXCLUDES=(
  "agentic/loopcontroller/evidence_batch_e2e_test.go"
  "agentic/loopcontroller/loop_e2e_test.go"
)

echo "==> Copying packages from $SRC"
for p in "${PKGS[@]}"; do
  mkdir -p "$HERE/$(dirname "$p")"
  rm -rf "${HERE:?}/$p"
  cp -R "$SRC/$p" "$HERE/$p"
  echo "    carried $p"
done

echo "==> Removing product-coupled e2e tests"
for f in "${TEST_EXCLUDES[@]}"; do
  rm -f "$HERE/$f"
  echo "    excluded $f"
done

echo "==> Rewriting import paths (lex-backend/internal -> $MODULE)"
# GNU/BSD sed compatible in-place edit.
find "$HERE" -name '*.go' -type f -print0 | while IFS= read -r -d '' f; do
  sed -i.bak "s#lex-backend/internal#$MODULE#g" "$f" && rm -f "$f.bak"
done

echo "==> go mod tidy"
( cd "$HERE" && go mod tidy ) || echo "    (tidy reported issues — see decoupling list below)"

echo
echo "==> Files still referencing the private module (need manual decoupling):"
echo "    These import the product's web transport (api/ws), DB helper (platform/db),"
echo "    or HTTP handlers — replace with small interfaces so the engine stays"
echo "    transport/framework-agnostic (roadmap Phase 6)."
grep -rl "lex-backend" "$HERE" --include='*.go' || echo "    none 🎉"

echo
echo "Done. Review, decouple flagged files, then 'go build ./...'."
