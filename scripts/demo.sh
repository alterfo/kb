#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DEMO_ROOT="${KB_DEMO_ROOT:-${TMPDIR:-/tmp}/kb-demo-root}"
LEON_QA_COUNT="${KB_DEMO_LEON_QA_COUNT:-6}"
KB_LLM_BASE_URL="${KB_LLM_BASE_URL:-http://127.0.0.1:11434}"
LLM_MODEL="${KB_LLM_MODEL:-qwen3.8:latest}"
EMBED_MODEL="${KB_EMBED_MODEL:-qwen3-embedding}"
ADDR="${KB_DEMO_ADDR:-127.0.0.1:8080}"

case "$LEON_QA_COUNT" in
  ''|*[!0-9]*) echo "KB_DEMO_LEON_QA_COUNT must be a non-negative integer" >&2; exit 2 ;;
esac

NO_PROXY_HOST="$(printf '%s' "$KB_LLM_BASE_URL" | sed -E 's#^[a-z]+://([^/:]+)(:[0-9]+)?.*#\1#')"
KB_NO_PROXY="${KB_NO_PROXY:-$NO_PROXY_HOST}"

canon_path() {
  local p="${1:-}"
  [ -n "$p" ] || return 1
  if [ -d "$p" ]; then
    (cd "$p" && pwd -P) 2>/dev/null || return 1
  else
    local parent
    parent="$(dirname "$p")"
    [ -d "$parent" ] || return 1
    printf '%s/%s\n' "$( (cd "$parent" && pwd -P) 2>/dev/null )" "$(basename "$p")"
  fi
}

case "${1:-prepare}" in
  reset)
    demo_canon="$(canon_path "$DEMO_ROOT" 2>/dev/null)" || {
      echo "refusing to delete unresolvable demo root: $DEMO_ROOT" >&2
      exit 2
    }
    refuse_reset() {
      echo "refusing to delete unsafe demo root: $DEMO_ROOT" >&2
      exit 2
    }
    for root in / "$HOME" "$REPO_ROOT" /tmp /home /Users /private ${TMPDIR:-}; do
      [ -n "$root" ] || continue
      root_canon="$(canon_path "$root" 2>/dev/null)" || continue
      case "$root" in
        "$HOME"|"$REPO_ROOT"|/home|/Users)
          if [ "$demo_canon" = "$root_canon" ] || [[ "$demo_canon" == "$root_canon"/* ]]; then
            refuse_reset
          fi
          ;;
        *)
          if [ "$demo_canon" = "$root_canon" ]; then
            refuse_reset
          fi
          ;;
      esac
    done
    rm -rf "$DEMO_ROOT"
    echo "removed demo root: $DEMO_ROOT"
    exit 0
    ;;
  prepare|index|serve) ;;
  *)
    echo "usage: $0 [prepare|index|serve|reset]" >&2
    exit 2
    ;;
esac

mkdir -p "$DEMO_ROOT/legal" "$DEMO_ROOT/leon-ai" "$DEMO_ROOT/leon-code"
cp "$REPO_ROOT/internal/importer/legalru/testdata/sample.md" "$DEMO_ROOT/legal/sample.md"
cp "$REPO_ROOT/internal/importer/legalru/testdata/gold/gk-rf-part1.md" "$DEMO_ROOT/legal/gk-rf-part1.md"
cp "$REPO_ROOT/internal/importer/legalru/testdata/gold/plenum-25-2015.md" "$DEMO_ROOT/legal/plenum-25-2015.md"

jq -r '.[0:'"$LEON_QA_COUNT"'][]
  | [.id, .question, .expected] | @tsv' \
  "$REPO_ROOT/testdata/leon-qa/qa_pairs.json" |
while IFS=$'\t' read -r id question expected; do
  slug="$(printf '%s' "$id" | tr '/:' '--' | tr -c 'A-Za-z0-9_.-' '-')"
  title_yaml="'$(printf '%s' "$question" | sed "s/'/''/g")'"
  printf '%s\n' \
    '---' \
    'source: leon-ai' \
    "id: $slug" \
    "title: $title_yaml" \
    '---' \
    '' \
    "# $question" \
    '' \
    "$expected" > "$DEMO_ROOT/leon-ai/$slug.md"
done

{
  printf '%s\n' \
    '---' \
    'source: leon-code' \
    'kind: code' \
    'id: leon-repo-main.go' \
    'path: leon-repo/main.go' \
    'title: Leon demo code' \
    '---'
  cat "$REPO_ROOT/cmd/kb/testdata/leon-repo/main.go"
} > "$DEMO_ROOT/leon-code/leon-repo-main.md"

if [[ "${1:-prepare}" == "prepare" ]]; then
  echo "prepared demo corpus at $DEMO_ROOT"
  exit 0
fi

make -C "$REPO_ROOT" build

KB_ROOT="$DEMO_ROOT" \
PERSIST_DIR="$DEMO_ROOT/.persist" \
KB_LLM_BASE_URL="$KB_LLM_BASE_URL" \
KB_LLM_MODEL="$LLM_MODEL" \
KB_EMBED_MODEL="$EMBED_MODEL" \
KB_NO_PROXY="$KB_NO_PROXY" \
  "$REPO_ROOT/bin/kb" reindex "$DEMO_ROOT"

if [[ "${1:-prepare}" == "serve" ]]; then
  echo "starting kb serve on $ADDR (demo root $DEMO_ROOT)"
  exec env \
    KB_ROOT="$DEMO_ROOT" \
    PERSIST_DIR="$DEMO_ROOT/.persist" \
    KB_LLM_BASE_URL="$KB_LLM_BASE_URL" \
    KB_LLM_MODEL="$LLM_MODEL" \
    KB_EMBED_MODEL="$EMBED_MODEL" \
    KB_NO_PROXY="$KB_NO_PROXY" \
    "$REPO_ROOT/bin/kb" serve -addr "$ADDR"
fi
