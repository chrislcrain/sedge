#!/usr/bin/env bash
# 05-hook-atomic-write.sh — SPEC.md §5.5
#
# Invariant under test: concurrent `sedge hook <event>` invocations against
# the same worktree must not corrupt the state file. internal/hookstate.Write
# implements the atomicity via CreateTemp + atomic Rename — each writer
# stages a fresh `.sedge-hook-*.json.tmp` and then renames it over the
# final path. The final file is always exactly one well-formed JSON
# document, even under tight write races.
#
# Sensitivity model (mental sabotage): if the implementation regressed to
# a plain `>` truncate-write, 50 concurrent writers would interleave their
# bytes mid-file and the final document would frequently fail jq parsing
# and/or leave .tmp siblings behind. Both conditions are explicit
# assertions below, so such a regression fails the case.
#
# Why real subprocesses: bash co-procs share a single OS process, which
# would serialise the writes and silently pass even on a broken
# implementation. `( … ) &` forks a subshell which forks `sedge` as its
# pipeline child, giving us 50 distinct OS-level writers contending for
# the same final path.

set -euo pipefail
source "$(dirname "$0")/../tmuxq"

harness_setup

# A scratch worktree path — `sedge hook` only cares that it's a string
# identifier on stdin; nothing on disk has to exist for the encode step.
WT_PATH="$HARNESS_TMP/wt"
mkdir -p "$WT_PATH"

# Mirror internal/hookstate.encode (and internal/project/recycle.go
# encodeClaudeProject): each /, ., space, ~ becomes '-'.
encode_path() { printf '%s' "$1" | tr '/.~ ' '----'; }
STATE_DIR="$SEDGE_HOME/hook-state"
STATE_FILE="$STATE_DIR/$(encode_path "$WT_PATH").json"

echo "  [debug] expected state file: $STATE_FILE"

# ---------------------------------------------------------------------------
# Fire N=50 parallel `sedge hook PreToolUse` real subprocesses. Each gets
# a distinct tool_name so we can tell which one's rename won.
# ---------------------------------------------------------------------------
N=50
pids=()
for i in $(seq 1 "$N"); do
  (
    printf '{"cwd":"%s","tool_name":"Tool-%02d","session_id":"sess-%02d"}\n' \
      "$WT_PATH" "$i" "$i" \
      | "$SEDGE_BIN" hook PreToolUse
  ) &
  pids+=("$!")
done

# Reap. Track failures so a `sedge hook` non-zero exit shows up explicitly
# rather than getting lost in pipeline noise.
fail_count=0
for pid in "${pids[@]}"; do
  if ! wait "$pid"; then
    fail_count=$((fail_count + 1))
  fi
done
assert_eq 0 "$fail_count" "all $N hook subprocesses exited 0"

# ---------------------------------------------------------------------------
# Assertion 1: exactly one final state file at the expected path.
# ---------------------------------------------------------------------------
if [[ ! -f "$STATE_FILE" ]]; then
  echo "  FAIL: $STATE_FILE not produced"
  echo "  --- contents of $STATE_DIR ---"
  ls -la "$STATE_DIR" || true
  exit 1
fi
echo "  PASS: state file present at $STATE_FILE"

# ---------------------------------------------------------------------------
# Assertion 2: no .tmp / .json.<pid> / other siblings.
# A correct CreateTemp+Rename leaves zero residue. A broken-mid-write
# implementation tends to leave half-written `.tmp` files behind.
# ---------------------------------------------------------------------------
mapfile -t stragglers < <(find "$STATE_DIR" -mindepth 1 -maxdepth 1 \
  ! -name "$(basename "$STATE_FILE")" -print)
if (( ${#stragglers[@]} > 0 )); then
  echo "  FAIL: temp-file siblings present in $STATE_DIR:"
  for f in "${stragglers[@]}"; do printf '    %s\n' "$f"; done
  exit 1
fi
echo "  PASS: no .tmp / partial siblings beside $STATE_FILE"

# ---------------------------------------------------------------------------
# Assertion 3: file parses as exactly one JSON document. jq -e returns
# non-zero on parse error or null/false result; we discard stdout.
# ---------------------------------------------------------------------------
if ! jq -e . "$STATE_FILE" >/dev/null 2>&1; then
  echo "  FAIL: state file is not valid JSON"
  echo "  --- contents ---"
  cat "$STATE_FILE" || true
  echo "  ----------------"
  exit 1
fi
echo "  PASS: state file parses as one valid JSON document"

# ---------------------------------------------------------------------------
# Assertion 4: required fields are well-formed. event must be exactly
# PreToolUse; tool_name must be one of the 50 we sent (Tool-01..Tool-50).
# Which one won is non-deterministic; that the winner is in the set is
# deterministic and is the property we're asserting.
# ---------------------------------------------------------------------------
event=$(jq -r .event "$STATE_FILE")
tool=$(jq -r .tool_name "$STATE_FILE")
at=$(jq -r .at "$STATE_FILE")

assert_eq "PreToolUse" "$event" "state.event is PreToolUse"

if ! [[ "$tool" =~ ^Tool-[0-9]{2}$ ]]; then
  echo "  FAIL: tool_name '$tool' is not one of the Tool-NN values we sent"
  exit 1
fi
echo "  PASS: state.tool_name is one of the 50 senders ($tool)"

# ---------------------------------------------------------------------------
# Assertion 5: state.at parses as RFC3339. Go's time.Time JSON marshaller
# emits the time.RFC3339Nano format. A strict regex avoids depending on
# GNU vs BSD `date` for parsing.
# ---------------------------------------------------------------------------
if ! [[ "$at" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]+)?(Z|[+-][0-9]{2}:?[0-9]{2})$ ]]; then
  echo "  FAIL: state.at '$at' is not RFC3339-shaped"
  exit 1
fi
echo "  PASS: state.at parses as RFC3339 ($at)"

# harness_teardown fires from the EXIT trap installed in tmuxq.
exit 0
