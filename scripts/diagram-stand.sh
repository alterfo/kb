#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
STAND="${KB_STAND_DIR:-$HOME/kb-stand}"
LLM_URL="${KB_LLM_BASE_URL:-http://192.168.88.193:11434}"
LLM_MODEL="${KB_LLM_MODEL:-qwen3.8:latest}"
EMBED_MODEL="${KB_EMBED_MODEL:-qwen3-embedding:0.6b}"
ADDR="${KB_STAND_ADDR:-127.0.0.1:8090}"
LLM_HOST="$(printf '%s' "$LLM_URL" | sed -E 's#^[a-z]+://([^/:]+)(:[0-9]+)?.*#\1#')"

CORPUS=(
  "AGENTS.md"
  "README.md"
  "docs/architecture.md"
  "docs/sources.md"
  "docs/new-connector.md"
)

stand_env() {
  export KB_DOTENV="$STAND/none.env"
  export KB_ROOT="$STAND/kb_root"
  export PERSIST_DIR="$STAND/persist"
  export KB_LLM_BASE_URL="$LLM_URL"
  export KB_EMBED_BASE_URL="$LLM_URL"
  export KB_EMBED_INDEX_BASE_URL="$LLM_URL"
  export KB_LLM_MODEL="$LLM_MODEL"
  export KB_EMBED_MODEL="$EMBED_MODEL"
  export KB_LLM_NO_THINK=true
  export KB_NO_PROXY="127.0.0.1,$LLM_HOST"
  export NO_PROXY="127.0.0.1,$LLM_HOST"
}

prepare() {
  mkdir -p "$STAND/kb_root/kb-docs" "$STAND/persist"
  (cd "$REPO_ROOT" && go build -o "$STAND/kb" ./cmd/kb)
  local rel name
  for rel in "${CORPUS[@]}"; do
    name="$(basename "$rel")"
    {
      printf -- '---\nsource: kb-docs\nid: %s\ntitle: %s\nupdated_at: %s\n---\n\n' \
        "${name%.md}" "$name" "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
      cat "$REPO_ROOT/$rel"
    } > "$STAND/kb_root/kb-docs/$name"
  done
}

cmd="${1:-all}"
case "$cmd" in
  prepare) stand_env; prepare ;;
  index)   stand_env; "$STAND/kb" reindex ;;
  serve)   stand_env; exec "$STAND/kb" serve -addr "$ADDR" ;;
  diagram) stand_env; shift; exec "$STAND/kb" diagram "$@" ;;
  all)     stand_env; prepare; "$STAND/kb" reindex; exec "$STAND/kb" serve -addr "$ADDR" ;;
  *) echo "usage: $0 [prepare|index|serve|diagram <entity>|all]" >&2; exit 2 ;;
esac
