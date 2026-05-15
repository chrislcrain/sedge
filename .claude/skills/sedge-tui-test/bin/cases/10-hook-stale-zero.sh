#!/usr/bin/env bash
# 10-hook-stale-zero.sh — SPEC.md §4.5
#
# Invariant under test: the `hook_stale_minutes` knob in config.toml
# wires through to hookstate.Classify and governs the crash-fallback
# demotion. Two endpoints define the contract:
#
#   hook_stale_minutes = 0  →  demotion DISABLED. A hook-state file
#                              recording an in-flight event always wins,
#                              no matter how old the timestamp.
#   hook_stale_minutes = 1  →  demotion FIRES after one minute. An
#                              ancient timestamp (1970) collapses the
#                              dot back to the idle/background glyph.
#
# Mechanics:
#   - hookstate.Classify only promotes a *background* worktree's dot.
#     We need feat-bg in WtBackground state (live tmux window, not in
#     slot), so the case spawns a second worktree (feat-slot) whose
#     creation bumps feat-bg out of the slot. Pattern lifted from
#     case 06.
#   - Hook state is hand-written via t_encode (mirrors hookstate.encode).
#     event=PreToolUse classifies as ActivityInFlight → list[i].State
#     becomes WtWaiting → renderRow paints ●. The dot glyph (◐ vs ●) is
#     enough to discriminate; we don't pin ANSI colour codes per SKILL.md
#     "Limitations".
#   - config.toml is hand-written so the harness owns the value of
#     hook_stale_minutes from the first sedge boot. We deliberately do
#     NOT route through `sedge add`, because Save() on the current branch
#     would round-trip the file through a Config struct that may not yet
#     carry HookStaleMinutes — that round-trip silently strips the knob.
#     Once impl-1's Config field lands, this stops being a hazard.
#   - Phase 2 reload uses the `r` keybinding (actReload → reloadCfgCmd
#     → project.Load), so the running sedge picks up the new
#     hook_stale_minutes WITHOUT us needing to tear the tmux server down
#     and lose feat-bg's background-window state.
#
# Expected outcomes:
#   PHASE 1 (hook_stale_minutes=0, at=1970): assert dot == ●
#     - Until cfg.HookStaleMinutes is threaded into Classify, this
#       assertion is RED — the hard-coded 10-minute fallback demotes the
#       1970 event to ActivityIdle and the dot stays ◐. RED here is the
#       deliverable per TEAM.md §3: the case pins the contract; impl
#       turns it green.
#   PHASE 2 (hook_stale_minutes=1, at=1970): assert dot == ◐
#     - Passes today (1970 > 10min already) and continues to pass under
#       impl-1's wiring (1970 > 1min). The phase exists to prove the
#       knob is wired BOTH ways, not just for the "disable" direction.

set -euo pipefail
source "$(dirname "$0")/../tmuxq"

harness_setup

# ---------------------------------------------------------------------------
# Throwaway git repo with isolated identity (host git config never read).
# ---------------------------------------------------------------------------
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

REPO="$HARNESS_TMP/repo"
mk_repo "$REPO"

# ---------------------------------------------------------------------------
# Write config.toml with hook_stale_minutes=0 BEFORE sedge boots. We don't
# rely on `sedge add` because that round-trips through Save(cfg) which, on
# branches predating impl-1's HookStaleMinutes field, would silently strip
# the knob from disk. Hand-writing the file means the value is exactly
# what we wrote until the case itself rewrites it.
# TODO(tmuxq): extract a `write_config` helper once a third case wants this.
# ---------------------------------------------------------------------------
write_config() {
  # Usage: write_config <hook_stale_minutes>
  local stale="$1"
  cat > "$SEDGE_HOME/config.toml" <<EOF
default_permission_mode = "auto"
worktrees_root = "$SEDGE_HOME/worktrees"
sedge_width_cols = 34
hook_stale_minutes = $stale

[[projects]]
name = "repo"
path = "$REPO"
default_branch = "main"
EOF
}
write_config 0
echo "  [debug] wrote config.toml with hook_stale_minutes=0"

# ---------------------------------------------------------------------------
# Env-hygiene workaround (mirrors case 03/04/06): kill the harness's initial
# sedge and restart with explicit env forwarding so the host's shell init
# can't shadow the harness scratch dirs. After this, sedge has freshly
# loaded our config with hook_stale_minutes=0.
# ---------------------------------------------------------------------------
t kill-session -t t 2>/dev/null || true
t new-session -d -s t -x 200 -y 50
t send-keys -t t:0 \
  "exec env SEDGE_HOME='$SEDGE_HOME' CLAUDE_CONFIG_DIR='$CLAUDE_CONFIG_DIR' SEDGE_BIN='$SEDGE_BIN' PATH='$PATH' '$SEDGE_BIN' --inside-tmux" \
  Enter
if ! wait_for 5 't_capture t:0 | grep -q "repo"'; then
  echo '  ERROR: relaunched sedge never rendered the seeded project row'
  t_capture t:0 || true
  exit 1
fi
SEDGE_PANE=$(t list-panes -t t:0 -F '#{pane_id}' | head -1)
export SEDGE_PANE

# ---------------------------------------------------------------------------
# Row / cursor helpers (inline; same patterns as cases 03/04/06).
# TODO(tmuxq): extract once a third case needs them — already three do.
# ---------------------------------------------------------------------------
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

drive_create_worktree() {
  local project_path="$1" session="$2"
  local wt_path="$SEDGE_HOME/worktrees/$(basename "$project_path")/$session"

  if t_capture "$SEDGE_PANE" | grep -q "+ new session"; then
    cursor_to_row_matching '\+ new session' 15 >/dev/null \
      || { echo "  ERROR: could not find existing '+ new session' row"; return 1; }
  else
    t send-keys -t "$SEDGE_PANE" Enter
    sleep 0.2
    wait_for 5 't_capture "$SEDGE_PANE" | grep -q "+ new session"' \
      || { echo "  ERROR: '+ new session' never rendered"; t_capture "$SEDGE_PANE" || true; return 1; }
    t send-keys -t "$SEDGE_PANE" 'j'
    sleep 0.1
    local cur
    cur=$(current_row_text)
    if ! echo "$cur" | grep -q '+ new session'; then
      echo "  ERROR: cursor not on '+ new session' (got: $cur)"
      return 1
    fi
  fi
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
  wait_for 15 "pgrep -fc 'claude .* -n $session' >/dev/null" \
    || { echo "  ERROR: no claude process for session $session"; pgrep -af claude || true; return 1; }
  printf '%s\n' "$wt_path"
}

# Pull the dot glyph that precedes "feat-bg" in the rendered tree. Plain
# capture (no ANSI), then sniff the single rune between "│ " and " feat-bg".
dot_for_bg() {
  t_capture "$SEDGE_PANE" \
    | awk '/feat-bg/{print; exit}' \
    | sed -nE 's/.*│[[:space:]]+(.)[[:space:]]+feat-bg.*/\1/p'
}

wait_for_dot() {
  local want="$1" ceiling="${2:-8}"
  local start=$SECONDS dot
  while :; do
    dot=$(dot_for_bg)
    if [[ "$dot" == "$want" ]]; then
      return 0
    fi
    if (( SECONDS - start >= ceiling )); then
      echo "  TIMEOUT: waited ${ceiling}s for feat-bg dot == '$want'" >&2
      echo "  last observed: '$dot'" >&2
      echo "  --- pane ---" >&2
      t_capture "$SEDGE_PANE" >&2 || true
      echo "  ------------" >&2
      return 1
    fi
    sleep 0.5
  done
}

# ---------------------------------------------------------------------------
# Create two worktrees so feat-bg ends up in WtBackground (the only state
# from which hookstate.Classify can promote the dot — see model.go §loadWorktreesCmd).
# ---------------------------------------------------------------------------
cursor_to_top
WT_BG=$(drive_create_worktree "$REPO" "feat-bg") || exit 1
t select-pane -t "$SEDGE_PANE"

cursor_to_top
WT_SLOT=$(drive_create_worktree "$REPO" "feat-slot") || exit 1
t select-pane -t "$SEDGE_PANE"

echo "  [debug] WT_BG   = $WT_BG"
echo "  [debug] WT_SLOT = $WT_SLOT"

# ---------------------------------------------------------------------------
# Hand-write feat-bg's hook-state with an ANCIENT timestamp. PreToolUse
# classifies as ActivityInFlight; under hook_stale_minutes=0 the staleness
# check must be skipped entirely, so the dot must read ● regardless of
# the 1970 `at`.
# ---------------------------------------------------------------------------
STATE_DIR="$SEDGE_HOME/hook-state"
STATE_FILE="$STATE_DIR/$(t_encode "$WT_BG").json"
mkdir -p "$STATE_DIR"
cat > "$STATE_FILE.tmp" <<EOF
{
  "event": "PreToolUse",
  "at": "1970-01-01T00:00:00Z",
  "tool_name": "Bash",
  "session_id": "harness-stale-zero"
}
EOF
mv "$STATE_FILE.tmp" "$STATE_FILE"
echo "  [debug] wrote ancient PreToolUse hook-state: $STATE_FILE"

# ---------------------------------------------------------------------------
# PHASE 1 ASSERTION — hook_stale_minutes=0 disables demotion.
#
# Under the wired implementation the staleness guard is skipped, so the
# 1970 PreToolUse event survives and feat-bg's row renders ●.
#
# Until cfg.HookStaleMinutes is threaded into hookstate.Classify, this
# assertion is RED: the hard-coded 10-minute fallback demotes the event
# to ActivityIdle and the dot stays ◐. That RED is the intended hand-off
# (TEAM.md §3); do not weaken the assertion.
# ---------------------------------------------------------------------------
phase1_dot=$(dot_for_bg)
echo "  [debug] phase 1 baseline dot for feat-bg before settle: '$phase1_dot'"
if wait_for_dot '●' 8; then
  echo "  PASS: SPEC §4.5: hook_stale_minutes=0 keeps ancient PreToolUse promoted to ● (in-flight)"
else
  echo "  FAIL: SPEC §4.5: hook_stale_minutes=0 should disable the crash-fallback demotion;"
  echo "        feat-bg's 1970 PreToolUse must render ● but rendered '$(dot_for_bg)' instead."
  echo "        (Likely cause: hookstate.Classify is not threading cfg.HookStaleMinutes yet.)"
  exit 1
fi

# ---------------------------------------------------------------------------
# PHASE 2 — rewrite config to hook_stale_minutes=1, hot-reload via `r`,
# and verify the dot demotes back to ◐ (background, idle). Same hook-state
# file on disk, same 1970 timestamp — only the knob changes.
#
# Why `r` instead of restarting the tmux server: actReload calls
# project.Load() and rebuilds m.cfg in the live sedge, so the new
# hook_stale_minutes flows into the next loadWorktreesCmd render WITHOUT
# tearing down the tmux windows that keep feat-bg in WtBackground. A
# server restart would Dormant-ify both worktrees and the dot logic
# would no longer touch hook state at all (see model.go: hookstate.Read
# is only consulted when State == WtBackground).
# ---------------------------------------------------------------------------
write_config 1
echo "  [debug] rewrote config.toml with hook_stale_minutes=1"

t send-keys -t "$SEDGE_PANE" 'r'

# Give reloadCfgCmd + loadAllWorktreesCmd time to fire (they cascade as
# tea.Msg's, not on the 3s tick), then settle one tick of slack.
if wait_for_dot '◐' 10; then
  echo "  PASS: SPEC §4.5: hook_stale_minutes=1 demotes the same 1970 PreToolUse back to ◐ (idle)"
else
  echo "  FAIL: SPEC §4.5: hook_stale_minutes=1 should fire the crash-fallback demotion;"
  echo "        feat-bg's 1970 PreToolUse must render ◐ but rendered '$(dot_for_bg)' instead."
  exit 1
fi

# harness_teardown fires from the EXIT trap installed by tmuxq.
exit 0
