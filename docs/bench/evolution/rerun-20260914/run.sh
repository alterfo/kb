#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/../../../.."
set -a
source .env
set +a
export no_proxy="127.0.0.1,localhost,192.168.88.193"
export NO_PROXY="$no_proxy"

OUTDIR="docs/bench/evolution/rerun-20260914"
KB="./bin/kb"

retry() {
  local label="$1" logfile="$2"; shift 2
  local attempt ok=0
  for attempt in 1 2 3 4 5; do
    if "$@" >> "$logfile" 2>&1; then
      ok=1
      break
    fi
    echo "=== $label: attempt $attempt failed (likely transient HF error), backing off ===" | tee -a "$logfile"
    sleep $((attempt * 30))
  done
  [ "$ok" = "1" ]
}

run_stage() {
  local name="$1" persist="$2" mode="$3" limit="$4"; shift 4
  echo "=== stage $name: start $(date -u +%FT%TZ) ==="
  date -u +%FT%TZ > "$OUTDIR/$name.start"

  if [ -s "$OUTDIR/$name.json" ]; then
    echo "=== stage $name: answers already present, skipping bench-dragon ===" | tee -a "$OUTDIR/$name.log"
  else
    if ! retry "stage $name bench-dragon" "$OUTDIR/$name.log" \
        env "$@" KB_LLM_NO_THINK=true "$KB" bench-dragon \
        -hist -doc-limit 192 \
        -persist-dir "docs/bench/evolution/$persist" \
        -answer-mode "$mode" -limit "$limit" \
        -out "$OUTDIR/$name.json"; then
      echo "=== stage $name: bench-dragon FAILED after retries, see $OUTDIR/$name.log ===" | tee -a "$OUTDIR/$name.log"
      date -u +%FT%TZ > "$OUTDIR/$name.exit-fail"
      return 1
    fi
  fi

  if [ -s "$OUTDIR/$name.score.json" ]; then
    echo "=== stage $name: score already present, skipping ===" | tee -a "$OUTDIR/$name.log"
  else
    if ! retry "stage $name score" "$OUTDIR/$name.log" \
        "$KB" bench-dragon score \
        -out "$OUTDIR/$name.score.json" \
        -history "$OUTDIR/$name.score.json.history.json" \
        "$OUTDIR/$name.json"; then
      echo "=== stage $name: score FAILED after retries, see $OUTDIR/$name.log ===" | tee -a "$OUTDIR/$name.log"
      date -u +%FT%TZ > "$OUTDIR/$name.exit-fail"
      return 1
    fi
  fi

  date -u +%FT%TZ > "$OUTDIR/$name.end"
  echo "=== stage $name: done $(date -u +%FT%TZ) ==="
}

run_stage stage0-native persist-a naive 150 \
  KB_INDEX_GRAPH=false KB_HYBRID=false

run_stage stage1-hybrid persist-a naive 150 \
  KB_INDEX_GRAPH=false KB_HYBRID=true

run_stage stage2-graph persist-b naive 150 \
  KB_INDEX_GRAPH=true KB_HYBRID=true

run_stage stage3-rerank persist-b naive 150 \
  KB_INDEX_GRAPH=true KB_HYBRID=true KB_RERANK=llm

run_stage stage4-logic persist-b got 100 \
  KB_INDEX_GRAPH=true KB_HYBRID=true KB_RERANK=llm

run_stage stage5-temporal persist-b got 100 \
  KB_INDEX_GRAPH=true KB_HYBRID=true KB_RERANK=llm \
  KB_SUPERSEDE_MODE=strict KB_DETECT_CONTRADICTIONS=true KB_LLM_MAX_TOKENS=4096

run_stage stage6-qualifiers persist-b got 100 \
  KB_INDEX_GRAPH=true KB_HYBRID=true KB_RERANK=llm \
  KB_SUPERSEDE_MODE=strict KB_DETECT_CONTRADICTIONS=true KB_LLM_MAX_TOKENS=4096 \
  KB_QUALIFIER_FILTER=true

echo "=== all stages done $(date -u +%FT%TZ) ==="
