#!/usr/bin/env bash
# verify.sh - quickly confirm the ttygs MCP server is wired up correctly.
#
# Usage: scripts/verify.sh [path/to/ttygs] [path/to/data-home]
#
# Exit code 0 = everything is healthy. Non-zero = the agent should not try
# to call the tools yet.

set -uo pipefail

BIN="${1:-${TTYGS_BIN:-$(command -v ttygs || echo ./ttygs)}}"
DATA_HOME="${2:-${TTYGS_DATA_HOME:-$PWD/data}}"

red()   { printf '\033[31m%s\033[0m\n' "$*" >&2; }
green() { printf '\033[32m%s\033[0m\n' "$*"; }
blue()  { printf '\033[34m%s\033[0m\n' "$*"; }

ok=1

blue "==> ttygs MCP server health check"
blue "    binary:    $BIN"
blue "    data home: $DATA_HOME"

# 1. Binary exists and is executable.
if [[ ! -x "$BIN" ]]; then
  red "X binary not found or not executable: $BIN"
  red "  build it with: go build -o $BIN ./cmd/ttygs"
  ok=0
else
  green "OK binary exists"
  # The CLI exits non-zero when invoked with no args, which is expected.
  # We just need to see the usage banner, so swallow the exit code.
  out=$("$BIN" 2>&1 || true)
  if [[ "$out" == *"Usage: ttygs"* ]]; then
    green "OK binary runs and prints usage"
  else
    red "X binary did not print expected usage banner"
    ok=0
  fi
fi

# 2. Data home exists and contains stars.db.
if [[ ! -d "$DATA_HOME" ]]; then
  red "X data home does not exist: $DATA_HOME"
  red "  set TTYGS_DATA_HOME or create the directory"
  ok=0
elif [[ ! -f "$DATA_HOME/stars.db" ]]; then
  red "X no stars.db in $DATA_HOME"
  red "  run: ttygs sync"
  ok=0
else
  green "OK stars.db present"
  if command -v sqlite3 >/dev/null 2>&1; then
    n=$(sqlite3 "$DATA_HOME/stars.db" 'SELECT count(*) FROM repos;' 2>/dev/null || echo 0)
    if [[ "${n:-0}" -gt 0 ]]; then
      green "OK $n repos in database"
      n_emb=$(sqlite3 "$DATA_HOME/stars.db" 'SELECT count(*) FROM chunks WHERE embedding IS NOT NULL;' 2>/dev/null || echo 0)
      if [[ "${n_emb:-0}" -gt 0 ]]; then
        green "OK $n_emb embedded chunks (indexed for vector_search)"
      else
        blue "  no embedded chunks - run: ttygs ingest"
      fi
    else
      red "X database is empty - run: ttygs sync && ttygs ingest"
      ok=0
    fi
  else
    blue "  (sqlite3 not available; skipping repo count check)"
  fi
fi

# 3. Embedder configured? (informational only)
if [[ -n "${OPENAI_API_KEY:-}" ]]; then
  green "OK OPENAI_API_KEY set (vector_search enabled)"
else
  blue "  OPENAI_API_KEY not set - vector_search will be disabled, list_repos/get_repo still work"
fi

if [[ $ok -eq 0 ]]; then
  red ""
  red "Health check failed. Fix the issues above and re-run:"
  red "    scripts/verify.sh"
  exit 1
fi

green ""
green "All checks passed. The MCP server is ready."
