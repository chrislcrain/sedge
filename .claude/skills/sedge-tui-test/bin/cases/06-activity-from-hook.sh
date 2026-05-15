#!/usr/bin/env bash
# 06-activity-from-hook.sh — SPEC.md §5.6
#
# Invariant under test: the activity indicator (the dot beside each
# worktree row) is derived ONLY from hookstate + tmux active path, never
# from claude's session JSONL mtime.
#
# Mechanics:
#   - sedge.tui.loadWorktreesCmd inspects hookstate.Read(wtPath) per row
#     and classifies it via hookstate.Classify(). Background worktrees
#     promote to WtWaiting / WtApproval based on the recorded Event;
#     non-background worktrees ignore hook state entirely.
#   - The rendered dot character distinguishes:
#       ◐  WtBackground (gray half)
#       ●  WtActive | WtWaiting | WtApproval (solid; colour differs)
#       ○  WtDormant (no live tmux window)
#   - Refresh tick is 3s (internal/tui/model.go refreshInterval).
#
# Test plan:
#   1. Register one project; create two worktrees (feat-bg, feat-slot).
#      After the second create, feat-slot occupies the slot and feat-bg
#      sits in its own background tmux window — so feat-bg's dot is
#      governed by hook state.
#   2. Pre-stage a fake JSONL session file under feat-bg's claude project
#      dir AND back-date its mtime to 1970-01-01. This is the negative
#      control: if the indicator were (incorrectly) reading JSONL mtime,
#      the back-dated file would push the row to "idle" regardless of
#      hook state. We assert dots track hookstate even with the file
#      back-dated, proving JSONL mtime is not a signal.
#   3. Hand-write feat-bg's hook-state to event=Notification, wait one
#      tick, assert feat-bg's dot is ● (approval pending — orange).
#   4. Overwrite the hook-state with event=Stop, wait one tick, assert
#      feat-bg's dot reverts to ◐ (background, idle).
#   5. (Belt-and-suspenders for the JSONL negative control:) touch the
#      JSONL forward to "now" and re-assert step 4. The hook state alone
#      governs the dot.
#
# Why we don't pin ANSI colour codes: SKILL.md "Limitations" steers cases
# away from literal escape codes. The ● vs ◐ character distinction is
# enough to differentiate Approval from Background — which is the actual
# classification we're verifying. The colour is just lipgloss styling on
# the same character.

set -euo pipefail
source "$(dirname "$0")/../tmuxq"

harness_setup

# ---------------------------------------------------------------------------
# Env-hygiene workaround (mirrors case 03/04): restart sedge with explicit
# env forwarding so the host's zsh init can't shadow the harness scratch
# SEDGE_HOME / CLAUDE_CONFIG_DIR.
# ---------------------------------------------------------------------------
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

# ---------------------------------------------------------------------------
# Fixture: one throwaway git repo, registered as a sedge project. We use
# the CLI (writes config.toml under our scratch SEDGE_HOME) and then `r`
# in the TUI to reload.
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
"$SEDGE_BIN" add "$REPO" >/dev/null
t send-keys -t "$SEDGE_PANE" 'r'
wait_for 5 't_capture "$SEDGE_PANE" | grep -q "repo"' \
  || { echo "  ERROR: project repo never appeared"; t_capture "$SEDGE_PANE" || true; exit 1; }

# ---------------------------------------------------------------------------
# Encoding helpers — mirror internal/hookstate.encode and the encode used
# by the Claude project-dir scheme (same rules: /, ., space, ~ → '-').
# ---------------------------------------------------------------------------
encode_path() { printf '%s' "$1" | tr '/.~ ' '----'; }

# ---------------------------------------------------------------------------
# Row / cursor helpers (lifted from case 03; TODO(tmuxq) extract).
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

  # If the project row is already expanded (i.e. "+ new session" is
  # already visible on screen), don't press Enter — that would collapse
  # it. Instead, navigate the cursor directly to "+ new session" using
  # cursor_to_row_matching.
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

# ---------------------------------------------------------------------------
# Create two worktrees. After both, feat-bg is in its background tmux
# window (visible to sedge but not the slot) and feat-slot occupies the
# slot.
# ---------------------------------------------------------------------------
# With a single registered project, the cursor starts on row 0 = the
# project row. cursor_to_top is enough; we don't need the highlight
# scanner to find it.
cursor_to_top
WT_BG=$(drive_create_worktree "$REPO" "feat-bg") || exit 1
t select-pane -t "$SEDGE_PANE"

# For the second create, the project row is already expanded and "+ new
# session" is visible. drive_create_worktree detects that and jumps
# straight to the sentinel instead of toggling Enter (which would
# collapse the row).
cursor_to_top
WT_SLOT=$(drive_create_worktree "$REPO" "feat-slot") || exit 1
t select-pane -t "$SEDGE_PANE"

echo "  [debug] WT_BG   = $WT_BG"
echo "  [debug] WT_SLOT = $WT_SLOT"

# Pre-stage a fake JSONL session file for feat-bg, then back-date it. If
# the indicator were tracking JSONL mtime (it shouldn't per SPEC §5.6),
# this 1970 timestamp would push feat-bg's row toward "idle/stale"
# regardless of hookstate. We assert the opposite below.
JSONL_DIR="$CLAUDE_CONFIG_DIR/projects/$(encode_path "$WT_BG")"
mkdir -p "$JSONL_DIR"
JSONL_FILE="$JSONL_DIR/control-session.jsonl"
printf '{"type":"meta","note":"jsonl-mtime-negative-control"}\n' > "$JSONL_FILE"
# Touch to 1970-01-01 00:00:00 UTC. Use `touch -d` (GNU) with a fallback
# to `touch -t` (BSD) — both honour CCYYMMDDhhmm.SS / "YYYY-MM-DD".
if ! touch -d '1970-01-01 00:00:00 UTC' "$JSONL_FILE" 2>/dev/null; then
  touch -t 197001010000.00 "$JSONL_FILE"
fi
echo "  [debug] back-dated JSONL: $(ls -la "$JSONL_FILE")"

# ---------------------------------------------------------------------------
# State-file path used by hookstate.Write — we write directly so we don't
# have to drive the stub claude through a fake hook chain.
# ---------------------------------------------------------------------------
STATE_DIR="$SEDGE_HOME/hook-state"
STATE_FILE="$STATE_DIR/$(encode_path "$WT_BG").json"
mkdir -p "$STATE_DIR"

write_state() {
  # Usage: write_state <event>
  # Emits an RFC3339-Nano timestamp via the host's `date` so hookstate.Read
  # can parse it back through json.Unmarshal -> time.Time.
  local event="$1"
  local now
  now=$(date -u +'%Y-%m-%dT%H:%M:%S.000000000Z')
  cat > "$STATE_FILE.tmp" <<EOF
{
  "event": "$event",
  "at": "$now",
  "tool_name": "",
  "session_id": "harness-$event"
}
EOF
  mv "$STATE_FILE.tmp" "$STATE_FILE"
}

# Return the dot character that precedes "feat-bg" in the rendered tree.
# Captures the plain (no-ANSI) pane text and pulls the glyph between
# "│ " and " feat-bg". Returns empty if not found.
dot_for_bg() {
  t_capture "$SEDGE_PANE" \
    | awk '/feat-bg/{print; exit}' \
    | sed -nE 's/.*│[[:space:]]+(.)[[:space:]]+feat-bg.*/\1/p'
}

# Wait up to ceiling seconds for dot_for_bg to equal $want.
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

# Sanity baseline: with no hook state and feat-bg in background, the dot
# should be ◐. Give the TUI a tick to settle after the worktree create.
sleep 3.5
baseline=$(dot_for_bg)
echo "  [debug] baseline dot for feat-bg: '$baseline'"
assert_eq '◐' "$baseline" "feat-bg renders ◐ (background) when no hook state is recorded"

# ---------------------------------------------------------------------------
# Step 1: hand-write Notification → expect ● (approval pending — orange).
# We pause up to 8s (≈2 ticks plus slack). Determinism note: the tick is
# 3s, and we touched the state file freshly so hookstate.Classify won't
# treat it as stale (10-minute window per hookstate.go).
# ---------------------------------------------------------------------------
write_state "Notification"
wait_for_dot '●' 8
echo "  PASS: feat-bg renders ● after Notification hook event"

# ---------------------------------------------------------------------------
# Step 2: overwrite with Stop → expect ◐ (background, idle).
# ---------------------------------------------------------------------------
write_state "Stop"
wait_for_dot '◐' 8
echo "  PASS: feat-bg renders ◐ after Stop hook event"

# ---------------------------------------------------------------------------
# Step 3 (JSONL negative-control belt-and-suspenders): touch JSONL forward
# to "now", reassert that the dot still reflects the (unchanged) Stop
# hook state — i.e. an mtime move on JSONL does not flip the dot.
# ---------------------------------------------------------------------------
touch "$JSONL_FILE"  # mtime = now
sleep 4              # one tick + slack
dot=$(dot_for_bg)
assert_eq '◐' "$dot" \
  "feat-bg dot tracks hookstate, not JSONL mtime (post-forward-touch)"

# ---------------------------------------------------------------------------
# Step 4: flip hook state again with the JSONL mtime now fresh — the dot
# must still respond to hookstate alone.
# ---------------------------------------------------------------------------
write_state "Notification"
wait_for_dot '●' 8
echo "  PASS: feat-bg renders ● after second Notification (JSONL mtime irrelevant)"

# harness_teardown fires from the EXIT trap installed by tmuxq.
exit 0
