#!/usr/bin/env bash
# Top-level case runner for the sedge-tui-test skill.
#
# Builds sedge once into bin/sedge, prepends bin/stubs to PATH (so a case's
# subprocess `claude` / `gh` invocations hit the stubs, not the real
# binaries), iterates every bin/cases/*.sh script in sorted order, and
# reports pass/fail. Each case is responsible for its own tmux server
# lifecycle via harness_setup / harness_teardown (see bin/tmuxq).
#
# Usage:
#   ./bin/run.sh            run every case
#   ./bin/run.sh 04         run cases whose basename starts with "04"
#   ./bin/run.sh --keep     leave each case's tmux server up for debugging

set -euo pipefail

HARNESS_DIR=$(cd "$(dirname "$0")/.." && pwd)
REPO_ROOT=$(cd "$HARNESS_DIR/../../.." && pwd)

KEEP=0
FILTER=""
for arg in "$@"; do
  case "$arg" in
    --keep) KEEP=1 ;;
    -h|--help)
      sed -n '2,15p' "$0"
      exit 0
      ;;
    *) FILTER="$arg" ;;
  esac
done

export HARNESS_DIR REPO_ROOT KEEP
export HARNESS_BIN="$HARNESS_DIR/bin"
export FIXTURES="$HARNESS_DIR/fixtures"
export SEDGE_BIN="$HARNESS_BIN/sedge"

echo "[run.sh] building sedge -> $SEDGE_BIN"
( cd "$REPO_ROOT" && go build -o "$SEDGE_BIN" ./cmd/sedge )

# Stubs first on PATH so any subprocess `claude`/`gh` resolves to the
# fake. The real binaries (if installed) are still reachable by absolute
# path; sedge itself we expose via $SEDGE_BIN, not PATH.
export PATH="$HARNESS_BIN/stubs:$PATH"

shopt -s nullglob
cases=( "$HARNESS_DIR/bin/cases/"*.sh )

if [[ ${#cases[@]} -eq 0 ]]; then
  echo "[run.sh] no cases under bin/cases/ — nothing to run"
  exit 0
fi

pass=0
fail=0
skipped=0
declare -a failed_names=()

for case_file in "${cases[@]}"; do
  name=$(basename "$case_file" .sh)
  if [[ -n "$FILTER" && "$name" != ${FILTER}* ]]; then
    skipped=$((skipped+1))
    continue
  fi
  echo "[run.sh] ---- $name ----"
  if bash "$case_file"; then
    echo "[run.sh] PASS $name"
    pass=$((pass+1))
  else
    echo "[run.sh] FAIL $name (exit $?)"
    fail=$((fail+1))
    failed_names+=("$name")
  fi
done

echo "[run.sh] summary: $pass passed, $fail failed, $skipped skipped"
if [[ $fail -ne 0 ]]; then
  printf '[run.sh] failed:\n'
  for n in "${failed_names[@]}"; do printf '  - %s\n' "$n"; done
  exit 1
fi
exit 0
