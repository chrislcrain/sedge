#!/usr/bin/env bash
# 03-pane-count-rehydrates.sh — SPEC.md §5.3
#
# Invariant under test: if worktree B had N panes when it was last visible,
# activating B again must restore N panes in the slot subtree next to sedge.
#
# Flow:
#   1. Register two ephemeral git repos as sedge projects.
#   2. Reload sedge so it picks them up, then create one worktree per project.
#      Creating B leaves B's pane in the slot.
#   3. Press `o` so B's slot grows to 2 panes (claude + shell).
#   4. Activate A — B's panes evacuate to B's background window (SPEC §4.3).
#   5. Activate B again — B's pane tree must swap back, all N panes intact.

set -euo pipefail
source "$(dirname "$0")/../tmuxq"

harness_setup

# The default harness_setup spawns sedge via `exec '$SEDGE_BIN' --inside-tmux`
# from a login shell. On hosts whose ~/.zshrc unconditionally re-exports
# SEDGE_HOME (e.g. `export SEDGE_HOME=/code/.sedge`), that override clobbers
# the harness's per-test tmpdir and points the running sedge at the user's
# real config. Restart sedge in the same tmux pane with the harness env
# explicitly forwarded so the under-test sedge actually sees our scratch
# SEDGE_HOME / CLAUDE_CONFIG_DIR.
# TODO(tmuxq): teach harness_setup to bypass shell rc files (e.g. via
# `exec env -i ... sedge`) so this dance isn't required per-case.
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

# --- ephemeral fixtures ----------------------------------------------------
# Two throwaway git repos. Isolated git identity per repo so we don't depend
# on the host's global git config.
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
REPO_B="$HARNESS_TMP/repo-b"
mk_repo "$REPO_A"
mk_repo "$REPO_B"

# Register both projects via the sedge CLI directly (writes config.toml
# under our scratch SEDGE_HOME). The CLI runs as a separate process from
# the parent bash, which still has the correct SEDGE_HOME exported.
SEDGE_HOME="$SEDGE_HOME" CLAUDE_CONFIG_DIR="$CLAUDE_CONFIG_DIR" "$SEDGE_BIN" add "$REPO_A" >/dev/null
SEDGE_HOME="$SEDGE_HOME" CLAUDE_CONFIG_DIR="$CLAUDE_CONFIG_DIR" "$SEDGE_BIN" add "$REPO_B" >/dev/null

# Reload the TUI so it picks up the new config.
t send-keys -t "$SEDGE_PANE" 'r'
wait_for 5 't_capture "$SEDGE_PANE" | grep -q "repo-a"' \
  || { echo "  ERROR: project repo-a never appeared"; t_capture "$SEDGE_PANE" || true; exit 1; }
wait_for 5 't_capture "$SEDGE_PANE" | grep -q "repo-b"' \
  || { echo "  ERROR: project repo-b never appeared"; t_capture "$SEDGE_PANE" || true; exit 1; }

SEDGE_WIN=$(t display-message -p -t "$SEDGE_PANE" '#{window_id}')

# --- helpers --------------------------------------------------------------

cursor_to_top() {
  # k is bounded by the model (clamps at 0), so spamming is safe. Insert
  # tiny pauses so the keys can't outrun bubbletea's event loop.
  local i
  for i in $(seq 1 20); do
    t send-keys -t "$SEDGE_PANE" 'k'
    sleep 0.02
  done
  sleep 0.2
}

# Return the highlighted (cursor) row's plain-text content.
current_row_text() {
  t capture-pane -e -p -t "$SEDGE_PANE" \
    | grep -E $'\x1b\\[48;5;237m' \
    | head -1 \
    | sed -E 's/\x1b\[[0-9;]*m//g'
}

# Step the cursor j times until the highlighted row matches the given
# extended regex. Returns the row text on success, 1 on miss.
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

# Drive the +new-session flow from a project row.
# Assumes the cursor is on a *collapsed* project row whose first j step
# would land us on "+ new session" after Enter expands it.
drive_create_worktree() {
  local project_path="$1" session="$2"
  local wt_path="$SEDGE_HOME/worktrees/$(basename "$project_path")/$session"

  # Expand the project row (Enter toggles), then step down to "+ new session".
  t send-keys -t "$SEDGE_PANE" Enter
  sleep 0.2
  wait_for 5 't_capture "$SEDGE_PANE" | grep -q "+ new session"' \
    || { echo "  ERROR: '+ new session' never rendered for $project_path"; t_capture "$SEDGE_PANE" || true; return 1; }
  t send-keys -t "$SEDGE_PANE" 'j'
  sleep 0.1
  # Sanity: the highlighted row should now be the +new-session row.
  local cur
  cur=$(current_row_text)
  if ! echo "$cur" | grep -q '+ new session'; then
    echo "  ERROR: cursor not on '+ new session' (got: $cur)"
    return 1
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
  # New claude pane exists in some tmux pane with that cwd...
  wait_for 15 "t_panes | awk -F'\t' '{print \$4}' | grep -Fxq '$wt_path'" \
    || { echo "  ERROR: no tmux pane ever ran in $wt_path"; t_panes; return 1; }
  # ...AND specifically lives in sedge's window (the slot).
  wait_for 10 "t_panes | awk -F'\t' -v w='$SEDGE_WIN' -v p='$wt_path' '\$2==w && \$4==p{n++}END{exit !n}'" \
    || { echo "  ERROR: new worktree never landed in sedge's window"; t_panes; return 1; }
  printf '%s\n' "$wt_path"
}

# Count panes inside SEDGE_WIN whose cwd == $1 (i.e. the slot subtree
# anchored at that worktree path).
count_subtree_panes() {
  local p="$1"
  t_panes | awk -F'\t' -v w="$SEDGE_WIN" -v p="$p" \
    '$2==w && $4==p {n++} END{print n+0}'
}

# Activate a worktree by session name. Uses the cursor walker to land on
# the worktree row (rowWorktree, prefixed with "│"), presses Enter once,
# and waits for B's pane to land in sedge's window. The caller guarantees
# the slot does NOT already hold the target worktree — that's the case for
# our test, since each activate_session call swaps to the *other* worktree.
activate_session() {
  local session="$1" wt_path="$2"
  # Pattern: a row containing the tree-branch glyph AND the session name.
  cursor_to_row_matching "│.*${session}\$|│.*${session} " 30 >/dev/null \
    || { echo "  ERROR: could not locate worktree row for '$session'"; t_capture "$SEDGE_PANE" || true; return 1; }
  t send-keys -t "$SEDGE_PANE" Enter
  wait_for 10 "t_panes | awk -F'\t' -v w='$SEDGE_WIN' -v p='$wt_path' '\$2==w && \$4==p{n++}END{exit !n}'" \
    || { echo "  ERROR: Enter on '$session' did not swap it into the slot"; t_panes; return 1; }
  # Settle: the post-swap refocus tick can take ~80ms; let it land so the
  # next keystroke is handled in steady-state modeList.
  sleep 0.3
  # Pop tmux focus back to sedge so subsequent send-keys land on the TUI.
  # (send-keys targets by pane id anyway, but this avoids surprises.)
  t select-pane -t "$SEDGE_PANE"
}

# --- create worktrees (each new worktree lands in the slot) ---------------
# Cursor starts at row 0 → repo-a project row.
WT_A=$(drive_create_worktree "$REPO_A" "feat-a") || exit 1
t select-pane -t "$SEDGE_PANE"

# Navigate to repo-b. After creating A, repo-a is expanded so rows are
# [repo-a, wt-feat-a, +new-a, repo-b, +new-b]. cursor_to_row_matching
# walks j until it finds the repo-b row.
cursor_to_row_matching '▸ repo-b|▾ repo-b' 15 >/dev/null \
  || { echo "  ERROR: could not find repo-b row"; exit 1; }
WT_B=$(drive_create_worktree "$REPO_B" "feat-b") || exit 1
t select-pane -t "$SEDGE_PANE"

# After creating B, B's pane is the slot. Add a shell via `o` so B's slot
# subtree grows to 2 panes. The cursor is currently on the +new-session
# row of repo-b (drive_create_worktree leaves it there); that's a
# rowNewSession, NOT a project row, so `o` falls into openCodePaneCmd
# rather than openShellAtCmd.
t send-keys -t "$SEDGE_PANE" 'o'
wait_for 10 "[ \"\$(t_panes | awk -F'\t' -v w='$SEDGE_WIN' -v p='$WT_B' '\$2==w && \$4==p{n++}END{print n+0}')\" = 2 ]" \
  || { echo "  ERROR: B never reached 2 panes in slot subtree"; t_panes; exit 1; }

panes_b_initial=$(count_subtree_panes "$WT_B")
assert_eq 2 "$panes_b_initial" "B has 2 panes (claude + shell) before swapping away"

# --- swap A in, evicting B -------------------------------------------------
activate_session "feat-a" "$WT_A" || exit 1
b_in_sedge=$(t_panes | awk -F'\t' -v w="$SEDGE_WIN" -v p="$WT_B" '$2==w && $4==p{n++}END{print n+0}')
assert_eq 0 "$b_in_sedge" "B's panes have evacuated sedge's window"

# --- swap B back in --------------------------------------------------------
activate_session "feat-b" "$WT_B" || exit 1
wait_for 10 "[ \"\$(t_panes | awk -F'\t' -v w='$SEDGE_WIN' -v p='$WT_B' '\$2==w && \$4==p{n++}END{print n+0}')\" -ge 1 ]" \
  || { echo "  ERROR: B never returned to sedge's window"; t_panes; exit 1; }

panes_b_after=$(count_subtree_panes "$WT_B")
assert_eq 2 "$panes_b_after" "B rehydrates to its original 2-pane slot subtree"
assert_eq "$panes_b_initial" "$panes_b_after" "pane count preserved across swap-out/swap-in"

# harness_teardown fires from the EXIT trap.
exit 0
