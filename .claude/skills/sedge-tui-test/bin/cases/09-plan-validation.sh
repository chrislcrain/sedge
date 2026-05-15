#!/usr/bin/env bash
# 09-plan-validation.sh — SPEC.md §5.9
#
# Invariant under test: plan validation rejects plans with duplicate ids,
# unknown deps, or self-loops *before* spawning any workers.
#
# Flow:
#   1. Boot one ephemeral git repo as a sedge project.
#   2. Reload sedge so it picks it up, create one worktree (claude in slot).
#   3. Walk cursor to the worktree row, press `W`. Wait for the planner
#      pane to appear below the slot. Record pane count P0 in sedge's
#      window and the planner pane id.
#   4. For each of three malformed plan.json variants — (a) duplicate
#      ids, (b) depends_on references an unknown id, (c) self-loop —
#      write the file with an mtime past the W-press baseline, wait 2
#      ticks (~6s+settle), then assert *independently*:
#        - pane count in sedge's window still == P0 (no workers spawned),
#        - planner pane still alive in the pane list,
#        - plan.json still on disk (validation does not delete it),
#        - the validation error specific to that branch is surfaced in
#          the sedge pane status line.
#   5. Write a well-formed plan; assert the review modal renders (the
#      plan name and the (y/N) prompt appear in the sedge pane), and
#      that pane count is *still* P0 — workers only spawn on `Y`.
#
# This case relies on the harness stub for `claude`: the planner pane
# runs the stub (which sleeps forever on stdin), so plan.json is written
# directly from the harness rather than via a real planner.

set -euo pipefail
source "$(dirname "$0")/../tmuxq"

harness_setup

# ------------------------------------------------------------------
# Same SEDGE_HOME relaunch dance as case 03: the default harness_setup
# spawns sedge via a login shell whose ~/.zshrc may re-export the user's
# real SEDGE_HOME, silently shadowing the harness's scratch dir. Re-exec
# sedge in the same pane with the harness env forwarded explicitly.
# TODO(tmuxq): teach harness_setup to bypass shell rc files so this
# dance isn't needed per-case.
# ------------------------------------------------------------------
t kill-session -t t 2>/dev/null || true
t new-session -d -s t -x 200 -y 50
t send-keys -t t:0 \
  "exec env SEDGE_HOME='$SEDGE_HOME' CLAUDE_CONFIG_DIR='$CLAUDE_CONFIG_DIR' SEDGE_BIN='$SEDGE_BIN' PATH='$PATH' '$SEDGE_BIN' --inside-tmux" \
  Enter
if ! wait_for 5 't_capture t:0 | grep -qE "no projects registered|sedge"'; then
  echo '  ERROR: relaunched sedge never produced output'
  t_capture t:0 || true
  exit 1
fi
SEDGE_PANE=$(t list-panes -t t:0 -F '#{pane_id}' | head -1)
export SEDGE_PANE
SEDGE_WIN=$(t display-message -p -t "$SEDGE_PANE" '#{window_id}')

# ------------------------------------------------------------------
# Ephemeral fixture: one throwaway git repo so sedge has a project to
# attach a worktree to. Isolated git identity so we don't depend on
# the host's global git config.
# ------------------------------------------------------------------
mk_repo() {
  local path="$1"
  mkdir -p "$path"
  git -C "$path" init -q -b main
  git -C "$path" config user.email "harness@sedge.test"
  git -C "$path" config user.name  "sedge harness"
  : > "$path/README.md"
  git -C "$path" add README.md
  git -C "$path" -c commit.gpgsign=false commit -q -m "init"
}

REPO_A="$HARNESS_TMP/repo-a"
mk_repo "$REPO_A"

# Register via the CLI so the config write is unambiguous.
SEDGE_HOME="$SEDGE_HOME" CLAUDE_CONFIG_DIR="$CLAUDE_CONFIG_DIR" \
  "$SEDGE_BIN" add "$REPO_A" >/dev/null

# Reload the TUI so it picks up the new config.
t send-keys -t "$SEDGE_PANE" 'r'
wait_for 5 't_capture "$SEDGE_PANE" | grep -q "repo-a"' \
  || { echo "  ERROR: project repo-a never appeared"; t_capture "$SEDGE_PANE" || true; exit 1; }

# ------------------------------------------------------------------
# Small wrappers around the running sedge pane.
# TODO(tmuxq): extract once a third case wants the same shape.
# ------------------------------------------------------------------
cursor_to_top() {
  local i
  for i in $(seq 1 20); do
    t send-keys -t "$SEDGE_PANE" 'k'
    sleep 0.02
  done
  sleep 0.2
}
current_row_text() {
  t capture-pane -e -p -t "$SEDGE_PANE" \
    | grep -E $'\x1b\\[48;5;237m' \
    | head -1 \
    | sed -E 's/\x1b\[[0-9;]*m//g'
}
cursor_to_row_matching() {
  local pattern="$1" limit="${2:-15}"
  cursor_to_top
  local i row
  for i in $(seq 0 "$limit"); do
    row=$(current_row_text)
    if [[ -n "$row" ]] && echo "$row" | grep -Eq "$pattern"; then
      printf '%s\n' "$row"
      return 0
    fi
    t send-keys -t "$SEDGE_PANE" 'j'
    sleep 0.05
  done
  echo "  ERROR: no row matched /$pattern/ within $limit steps" >&2
  return 1
}
# Drive the +new-session flow from a project row. Returns the worktree
# path on stdout.
drive_create_worktree() {
  local project_path="$1" session="$2"
  local wt_path="$SEDGE_HOME/worktrees/$(basename "$project_path")/$session"

  t send-keys -t "$SEDGE_PANE" Enter
  sleep 0.2
  wait_for 5 't_capture "$SEDGE_PANE" | grep -q "+ new session"' \
    || { echo "  ERROR: '+ new session' never rendered for $project_path"; t_capture "$SEDGE_PANE" || true; return 1; }
  t send-keys -t "$SEDGE_PANE" 'j'
  sleep 0.1
  t send-keys -t "$SEDGE_PANE" Enter

  wait_for 5 't_capture "$SEDGE_PANE" | grep -qi "name for new session"' \
    || { echo "  ERROR: session prompt didn't appear"; t_capture "$SEDGE_PANE" || true; return 1; }
  t send-keys -t "$SEDGE_PANE" "$session"
  t send-keys -t "$SEDGE_PANE" Enter

  wait_for 5 't_capture "$SEDGE_PANE" | grep -qi "pick a branch"' \
    || { echo "  ERROR: branch picker didn't appear"; t_capture "$SEDGE_PANE" || true; return 1; }
  t send-keys -t "$SEDGE_PANE" Enter

  wait_for 5 't_capture "$SEDGE_PANE" | grep -qi "worktree path"' \
    || { echo "  ERROR: worktree-path prompt didn't appear"; t_capture "$SEDGE_PANE" || true; return 1; }
  t send-keys -t "$SEDGE_PANE" Enter

  wait_for 15 "[ -d '$wt_path' ]" \
    || { echo "  ERROR: worktree dir never created at $wt_path"; return 1; }
  wait_for 15 "t_panes | awk -F'\t' -v w='$SEDGE_WIN' -v p='$wt_path' '\$2==w && \$4==p{n++}END{exit !n}'" \
    || { echo "  ERROR: new worktree never landed in sedge's window"; t_panes; return 1; }
  printf '%s\n' "$wt_path"
}

# ------------------------------------------------------------------
# Build the worktree, then drive the W flow.
# ------------------------------------------------------------------
cursor_to_top
WT=$(drive_create_worktree "$REPO_A" "feat-a") || exit 1
t select-pane -t "$SEDGE_PANE"

# Land cursor on the worktree row (rowWorktree carries the "│" branch
# glyph) before sending W. currentRow() must resolve to rowWorktree
# for actOrchestrate to fire orchestratePlannerCmd.
cursor_to_row_matching "│.*feat-a\$|│.*feat-a " 15 >/dev/null \
  || { echo "  ERROR: could not locate worktree row for feat-a"; t_capture "$SEDGE_PANE" || true; exit 1; }

# Pane snapshot pre-W: 2 panes in sedge's window (sedge + slot claude).
pre_w_panes=$(t_panes | awk -F'\t' -v w="$SEDGE_WIN" '$2==w{n++}END{print n+0}')
assert_eq 2 "$pre_w_panes" "two panes in sedge's window pre-W (sedge + slot)"

t send-keys -t "$SEDGE_PANE" 'W'

# orchestratePlannerCmd splits the slot vertically (-v -l 40%) to host
# the planner. Wait until sedge's window contains 3 panes.
wait_for 15 "[ \"\$(t_panes | awk -F'\t' -v w='$SEDGE_WIN' '\$2==w{n++}END{print n+0}')\" -ge 3 ]" \
  || { echo "  ERROR: planner pane never appeared after W"; t_panes; exit 1; }
sleep 0.5  # let the spawn + select-pane settle so pane order is stable.

# Record baseline pane count P0 and capture the planner pane id (the
# most-recently-created pane in sedge's window — list-panes is ordered
# by creation index, so `tail -1` picks the planner).
P0=$(t_panes | awk -F'\t' -v w="$SEDGE_WIN" '$2==w{n++}END{print n+0}')
PLANNER_PANE=$(t_panes | awk -F'\t' -v w="$SEDGE_WIN" '$2==w{print $1}' | tail -1)
echo "  INFO: P0=$P0 planner=$PLANNER_PANE"
assert_eq 3 "$P0" "sedge window holds exactly 3 panes after W (sedge + slot + planner)"

# Refocus sedge so subsequent send-keys land on the TUI (W select-panes
# the planner pane). The planner stub will idle on stdin forever, so it
# stays alive across the whole case unless explicitly killed.
t select-pane -t "$SEDGE_PANE"

# ------------------------------------------------------------------
# Plan-injection helpers. The harness writes plan.json directly because
# the stub claude does not write plans; we're testing the poll +
# validate + review path, not the planner LLM itself.
# ------------------------------------------------------------------
PLAN_FILE="$WT/.sedge/orchestration/plan.json"
mkdir -p "$(dirname "$PLAN_FILE")"

# Bump mtime monotonically into the future so the poller never sees a
# stale baseline. We touch -d "+Ns" after the write because filesystem
# mtime resolution can be coarse on some hosts; pushing into the future
# eliminates the race entirely.
PLAN_BUMP_SECS=0
write_plan() {
  PLAN_BUMP_SECS=$((PLAN_BUMP_SECS + 5))
  printf '%s\n' "$1" > "$PLAN_FILE"
  touch -d "+${PLAN_BUMP_SECS} seconds" "$PLAN_FILE"
}

# Flatten the sedge pane capture so error messages that wrap across the
# 34-col sedge pane still match a fixed substring search.
sedge_flat() {
  t_capture "$SEDGE_PANE" | tr -d '\n' | tr -s ' '
}

# Independent malformed-branch assertion bundle. Each branch must
# satisfy *all four* conditions; a failure on any one fails the branch.
assert_malformed_rejected() {
  local label="$1" err_substr="$2"
  # 2 ticks @ 3s + render settle margin. The poller runs on the tick
  # handler; we want to give it room for two attempts in case the
  # write landed just after a tick fired.
  sleep 7
  local panes
  panes=$(t_panes | awk -F'\t' -v w="$SEDGE_WIN" '$2==w{n++}END{print n+0}')
  assert_eq "$P0" "$panes" "$label: pane count in sedge window unchanged (no workers spawned)"
  if ! t_panes | awk -F'\t' '{print $1}' | grep -Fxq "$PLANNER_PANE"; then
    printf '  FAIL: %s: planner pane %s vanished\n' "$label" "$PLANNER_PANE"
    t_panes
    return 1
  fi
  printf '  PASS: %s: planner pane %s still alive\n' "$label" "$PLANNER_PANE"
  if [[ ! -f "$PLAN_FILE" ]]; then
    printf '  FAIL: %s: plan.json removed by validator\n' "$label"
    return 1
  fi
  printf '  PASS: %s: plan.json still on disk\n' "$label"
  # The review modal renders "Spawn N worker pane(s)?" for valid plans.
  # Its absence confirms we never crossed into modeReviewPlan.
  if t_capture "$SEDGE_PANE" | grep -qE 'Spawn [0-9]+ worker pane'; then
    printf '  FAIL: %s: review modal rendered for malformed plan\n' "$label"
    t_capture "$SEDGE_PANE" | sed 's/^/    /'
    return 1
  fi
  printf '  PASS: %s: review modal not rendered\n' "$label"
  # The branch-specific error must appear in sedge's status line.
  # We grep the flattened capture so a line-wrapped error still matches.
  if ! sedge_flat | grep -Fq "$err_substr"; then
    printf '  FAIL: %s: expected error substring %q not visible\n' "$label" "$err_substr"
    echo "    --- raw sedge pane capture ---"
    t_capture "$SEDGE_PANE" | sed 's/^/    /'
    echo "    --- end raw capture ---"
    echo "    pane dims: $(t_dims "$SEDGE_PANE" width)x$(t_dims "$SEDGE_PANE" height)"
    return 1
  fi
  printf '  PASS: %s: validator surfaced %q\n' "$label" "$err_substr"
}

# ------------------------------------------------------------------
# Branch 1 — duplicate session ids.
# orchestration.Validate emits: `duplicate session id %q`.
#
# Visible-substring caveat: bubbletea's renderer clips each line to the
# sedge pane width (~40 cols). The status line reads
#   error: invalid plan: duplicate session id "alpha"
# but only the first ~40 cols are captured. After "error: invalid plan: "
# (21 cols) only 19 cols remain → "duplicate session i" (the id is
# clipped). We grep for "duplicate" which is uniquely diagnostic of
# this branch regardless of clipping.
# ------------------------------------------------------------------
write_plan '{
  "name": "dup-ids",
  "summary": "two sessions share the same id",
  "sessions": [
    {"id": "alpha", "task": "first alpha",  "depends_on": []},
    {"id": "alpha", "task": "second alpha", "depends_on": []}
  ]
}'
assert_malformed_rejected "branch 1 (duplicate ids)" "duplicate"

# ------------------------------------------------------------------
# Branch 2 — depends_on references an unknown id.
# orchestration.Validate emits: `session %q depends on unknown session %q`.
# With id="unk" the visible 19 cols spell out "session \"unk\" depen" —
# the literal `"unk"` substring is uniquely diagnostic of this branch
# (it is the session id we authored only for branch 2).
# ------------------------------------------------------------------
write_plan '{
  "name": "unknown-dep",
  "summary": "depends_on points at a session that does not exist",
  "sessions": [
    {"id": "unk", "task": "task unk", "depends_on": ["ghost"]}
  ]
}'
assert_malformed_rejected "branch 2 (unknown dep)" '"unk"'

# ------------------------------------------------------------------
# Branch 3 — self-loop (session depends on itself).
# orchestration.Validate emits: `session %q depends on itself`.
# With id="loop" the visible 19 cols spell "session \"loop\" depe" —
# the literal `"loop"` substring is uniquely diagnostic of this branch.
# (The trailing "itself" is clipped; we cannot rely on it.)
# ------------------------------------------------------------------
write_plan '{
  "name": "self-loop",
  "summary": "session lists its own id in depends_on",
  "sessions": [
    {"id": "loop", "task": "task loop", "depends_on": ["loop"]}
  ]
}'
assert_malformed_rejected "branch 3 (self-loop)" '"loop"'

# ------------------------------------------------------------------
# Valid plan — the review modal must render. We assert the plan name
# and the (y/N) prompt appear in the sedge pane. We deliberately do
# NOT press Y here: workers must remain unspawned (pane count == P0)
# until the user explicitly approves.
# ------------------------------------------------------------------
write_plan '{
  "name": "valid-plan",
  "summary": "well-formed; review modal must render",
  "sessions": [
    {"id": "alpha", "task": "task alpha", "depends_on": []},
    {"id": "beta",  "task": "task beta",  "depends_on": ["alpha"]}
  ]
}'

wait_for 10 'sedge_flat | grep -Fq "Plan: valid-plan"' \
  || { echo "  FAIL: review modal never showed plan name"; sedge_flat | fold -w 100 | sed 's/^/    /'; exit 1; }
echo "  PASS: review modal shows plan name"

if ! sedge_flat | grep -Fq "(y/N)"; then
  echo "  FAIL: review modal missing (y/N) prompt"
  sedge_flat | fold -w 100 | sed 's/^/    /'
  exit 1
fi
echo "  PASS: review modal shows (y/N) prompt"

if ! sedge_flat | grep -Eq 'Spawn 2 worker pane'; then
  echo "  FAIL: review modal missing 'Spawn 2 worker pane(s)' line"
  sedge_flat | fold -w 100 | sed 's/^/    /'
  exit 1
fi
echo "  PASS: review modal shows 'Spawn 2 worker pane(s)'"

panes_now=$(t_panes | awk -F'\t' -v w="$SEDGE_WIN" '$2==w{n++}END{print n+0}')
assert_eq "$P0" "$panes_now" "valid plan: pane count unchanged until Y pressed"

# harness_teardown fires via the EXIT trap.
exit 0
