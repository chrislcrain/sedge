#!/usr/bin/env bash
# 07-orphan-free-exit.sh — SPEC.md §5.7
#
# Invariant under test:
#   `q`  breaks the slot back to its own background window; sedge's window
#        closes; every worktree's claude keeps running. The formerly-slotted
#        worktree must now live in its own background window.
#   `X`  kills every window whose primary pane is rooted in `worktrees_root`;
#        every matching claude pgrep returns 0. A window whose pane lives
#        OUTSIDE worktrees_root is untouched.
#
# Two-phase flow:
#   Phase 1 (q):
#     1. Stand up two worktrees A and B (B is the current slot).
#     2. Plant a "decoy" tmux window whose cwd is OUTSIDE worktrees_root.
#     3. Press `q` on the sedge pane.
#     4. Assert: sedge's original window is gone; A and B claudes still alive
#        (pgrep ≥ 1 each); a window with cwd == B's worktree path now exists
#        as a standalone background window.
#   Phase 2 (X):
#     5. Relaunch sedge in a fresh tmux window (same session, same scratch
#        SEDGE_HOME), explicit env forwarding so the harness's worktrees_root
#        is what the new sedge sees.
#     6. Press `X`, then `y` to confirm the modal.
#     7. Assert: every worktree-rooted window is gone; pgrep counts for A
#        and B drop to 0; decoy window still in `list-windows`.
#
# The decoy assertion is the load-bearing half of §5.7 — it discriminates
# "kill everything in worktrees_root" from "kill everything in the session",
# which is the regression the spec language is guarding against.

set -euo pipefail
source "$(dirname "$0")/../tmuxq"

# ---- helpers (inline; same patterns as cases 01-03) -----------------------

mk_repo() {
  local dir="$1"
  mkdir -p "$dir"
  ( cd "$dir" \
      && git init -q -b main \
      && git -c user.email=t@t -c user.name=t commit -q --allow-empty -m init )
}

# Drive the `n` add-project prompt for an absolute path.
add_project() {
  local path="$1"
  local name
  name=$(basename "$path")
  t_keys "$SEDGE_PANE" 'n'
  wait_for 5 "t_capture '$SEDGE_PANE' | grep -qi 'Path to git repo'"
  t_keys "$SEDGE_PANE" -l "$path"
  t_keys "$SEDGE_PANE" Enter
  wait_for 5 "t_capture '$SEDGE_PANE' | grep -qF '$name'"
}

# Drive `+ new session` for the project row under the cursor. Accepts every
# default; echoes the slugified session name (e.g. "s1715812345") on stdout.
# Caller provides the pre-creation `claude -n s<ts>` count so we can wait for
# the new spawn deterministically (case 02 pattern).
create_default_worktree() {
  local prev_count="${1:-0}"
  t_keys "$SEDGE_PANE" Enter
  wait_for 5 "t_capture '$SEDGE_PANE' | grep -qF '+ new session'"
  t_keys "$SEDGE_PANE" 'j'
  t_keys "$SEDGE_PANE" Enter
  wait_for 5 "t_capture '$SEDGE_PANE' | grep -qi 'Name for new session'"
  t_keys "$SEDGE_PANE" Enter
  wait_for 5 "t_capture '$SEDGE_PANE' | grep -qi 'Pick a branch'"
  t_keys "$SEDGE_PANE" Enter
  wait_for 5 "t_capture '$SEDGE_PANE' | grep -qi 'Worktree path'"
  t_keys "$SEDGE_PANE" Enter
  wait_for 15 "[[ \$(pgrep -cf 'claude .* -n s[0-9][0-9]*') -gt $prev_count ]]" 1>&2
  pgrep -af 'claude .* -n s[0-9][0-9]*' \
    | sed -nE 's/.* -n (s[0-9]+).*/\1/p' \
    | sort -u | sort -t s -k 2 -n | tail -1
}

# ---- harness setup --------------------------------------------------------
harness_setup

REPO_A="$HARNESS_TMP/repo-a"
REPO_B="$HARNESS_TMP/repo-b"
mk_repo "$REPO_A"
mk_repo "$REPO_B"

# Capture the original sedge window id so phase-1 can verify it's gone
# after `q`.
SEDGE_WIN=$(t display-message -p -t "$SEDGE_PANE" '#{window_id}')

# Resolve the worktrees_root sedge will actually use. With $SEDGE_HOME
# set under $HARNESS_TMP, xdg.DefaultWorktreesRoot() resolves to
# $SEDGE_HOME/worktrees. That's what cleanExitCmd compares against in
# the phase-2 X path, so anchor the decoy strictly outside it.
WT_ROOT="$SEDGE_HOME/worktrees"
DECOY_DIR="$HARNESS_TMP/decoy-outside-root"
mkdir -p "$DECOY_DIR"

# ---- register projects + spin up two worktrees ----------------------------
add_project "$REPO_A"
add_project "$REPO_B"

# Cursor walks to top → on repo-a.
for i in $(seq 1 20); do t_keys "$SEDGE_PANE" 'k'; done

sess_a=$(create_default_worktree 0)
[[ -n "$sess_a" ]] || { echo "  FAIL: could not determine session A name"; exit 1; }
printf '  INFO: session A = %s\n' "$sess_a"

# Navigate to repo-b. After creating A, repo-a is expanded; collapse it and
# step down once to land on repo-b.
for i in $(seq 1 20); do t_keys "$SEDGE_PANE" 'k'; done
t_keys "$SEDGE_PANE" Enter
wait_for 5 "t_capture '$SEDGE_PANE' | grep -qE '▸ repo-a'"
t_keys "$SEDGE_PANE" 'j'

# Distinct auto-name (s<unix-ts>) requires a beat between creates.
sleep 1.2

sess_b=$(create_default_worktree 1)
[[ -n "$sess_b" ]] || { echo "  FAIL: could not determine session B name"; exit 1; }
printf '  INFO: session B = %s\n' "$sess_b"

assert_neq "$sess_a" "$sess_b" "sessions A and B have distinct auto-names"

# Locate each worktree's filesystem path from the live panes — sedge's
# pane_current_path == the worktree dir for each fresh claude pane.
WT_A=$(t_panes | awk -F'\t' -v r="$WT_ROOT" '$4 ~ "^" r "/" && $4 !~ "decoy" { paths[$4]=1 } END { for (p in paths) print p }' | grep -m1 repo-a || true)
WT_B=$(t_panes | awk -F'\t' -v r="$WT_ROOT" '$4 ~ "^" r "/" && $4 !~ "decoy" { paths[$4]=1 } END { for (p in paths) print p }' | grep -m1 repo-b || true)
[[ -n "$WT_A" && -n "$WT_B" ]] || {
  echo "  FAIL: could not resolve WT_A / WT_B from panes"
  t_panes
  exit 1
}
printf '  INFO: WT_A = %s\n' "$WT_A"
printf '  INFO: WT_B = %s\n' "$WT_B"

# Sanity: B is the slot (it was created most recently → it's in sedge's
# window). A is in a background window.
b_in_sedge=$(t_panes | awk -F'\t' -v w="$SEDGE_WIN" -v p="$WT_B" '$2==w && $4==p{n++} END{print n+0}')
assert_eq 1 "$b_in_sedge" "B is the current slot before q"

# ---- plant the decoy window (outside worktrees_root) ----------------------
# A long-lived foreground process so the window doesn't self-close on EOF.
# Using `sh -c 'while true; do sleep 60; done'` so plain SIGHUP from tmux
# kill-window (if it were targeted at us by a buggy X) would actually take
# the window down — i.e. survival is a real signal, not "process refused
# to die".
t new-window -d -n decoy -c "$DECOY_DIR" \
  "sh -c 'while true; do sleep 60; done'"
DECOY_WIN=$(t list-windows -F '#{window_id}|#{window_name}' \
              | awk -F'|' '$2=="decoy"{print $1; exit}')
[[ -n "$DECOY_WIN" ]] || { echo "  FAIL: decoy window did not appear"; t_windows; exit 1; }

# Confirm decoy's pane really is outside worktrees_root — if this fails,
# the rest of the case is testing the wrong thing.
decoy_cwd=$(t_panes | awk -F'\t' -v w="$DECOY_WIN" '$2==w{print $4; exit}')
case "$decoy_cwd" in
  "$WT_ROOT"/*)
    echo "  FAIL: decoy cwd $decoy_cwd is INSIDE worktrees_root $WT_ROOT"
    exit 1 ;;
esac
printf '  INFO: decoy window = %s  cwd = %s\n' "$DECOY_WIN" "$decoy_cwd"

# ---- Phase 1: press `q` ---------------------------------------------------
# Snapshot pgrep counts so we can prove the swap-out preserves the
# processes (SPEC §5.7 first half).
before_a=$(pgrep -cf "claude .* -n $sess_a" || true)
before_b=$(pgrep -cf "claude .* -n $sess_b" || true)
assert_eq 1 "$before_a" "A's claude alive before q"
assert_eq 1 "$before_b" "B's claude alive before q"

t_keys "$SEDGE_PANE" 'q'

# Sedge's original window must disappear. detachSlotCmd breaks B out
# first, then tea.Quit takes down the now-sedge-only window.
wait_for 10 "! t list-windows -F '#{window_id}' | grep -qx '$SEDGE_WIN'" \
  || { echo "  FAIL: sedge window $SEDGE_WIN still present after q"; t_windows; exit 1; }
assert_neq "$SEDGE_WIN" "$(t list-windows -F '#{window_id}' | grep -x "$SEDGE_WIN" || echo gone)" \
  "sedge window closes after q"

# Both claude processes still alive — `q` is non-destructive.
after_q_a=$(pgrep -cf "claude .* -n $sess_a" || true)
after_q_b=$(pgrep -cf "claude .* -n $sess_b" || true)
assert_eq "$before_a" "$after_q_a" "SPEC §5.7: A's claude survives q"
assert_eq "$before_b" "$after_q_b" "SPEC §5.7: B's claude survives q"

# Formerly-slotted worktree (B) is now in its own background window —
# i.e. there exists a window (≠ original sedge window) whose pane cwd is
# B's worktree path.
b_bg_wins=$(t_panes | awk -F'\t' -v p="$WT_B" '$4==p{print $2}' | sort -u)
[[ -n "$b_bg_wins" ]] || { echo "  FAIL: no window hosts B after q"; t_panes; exit 1; }
for w in $b_bg_wins; do
  assert_neq "$SEDGE_WIN" "$w" "B's home window is not the (now-dead) sedge window"
done

# Decoy untouched.
wait_for 2 "t list-windows -F '#{window_id}' | grep -qx '$DECOY_WIN'" \
  || { echo "  FAIL: decoy window vanished during phase 1"; t_windows; exit 1; }

# ---- Phase 2: relaunch sedge, press `X` -----------------------------------
# Spawn a fresh sedge in a new tmux window. Pass the harness env explicitly
# so the new pane process sees the scratch SEDGE_HOME / stub PATH (case 03
# pattern: bypass any login-shell init that might re-export user globals).
t new-window -d -n sedge2 -t t \
  "env SEDGE_HOME='$SEDGE_HOME' CLAUDE_CONFIG_DIR='$CLAUDE_CONFIG_DIR' SEDGE_BIN='$SEDGE_BIN' PATH='$PATH' '$SEDGE_BIN' --inside-tmux"

SEDGE2_WIN=$(t list-windows -F '#{window_id}|#{window_name}' \
               | awk -F'|' '$2=="sedge2"{print $1; exit}')
[[ -n "$SEDGE2_WIN" ]] || { echo "  FAIL: relaunched sedge window not visible"; t_windows; exit 1; }
SEDGE2_PANE=$(t list-panes -t "$SEDGE2_WIN" -F '#{pane_id}' | head -1)

if ! wait_for 5 "t_capture '$SEDGE2_PANE' | grep -qiE 'sedge|project|worktree'"; then
  echo "  FAIL: relaunched sedge produced no output"
  t_capture "$SEDGE2_PANE" || true
  exit 1
fi

# Press X to trigger the modeConfirmCleanExit prompt, then confirm with y.
t_keys "$SEDGE2_PANE" 'X'
wait_for 5 "t_capture '$SEDGE2_PANE' | grep -qi 'Kill all sedge claude sessions'" \
  || { echo "  FAIL: X did not open the clean-exit confirmation modal"; t_capture "$SEDGE2_PANE" || true; exit 1; }
t_keys "$SEDGE2_PANE" 'y'

# Wait for sedge2 window to be gone (tea.Quit fires after cleanExitCmd).
wait_for 10 "! t list-windows -F '#{window_id}' | grep -qx '$SEDGE2_WIN'" \
  || { echo "  FAIL: sedge2 window $SEDGE2_WIN still present after X-y"; t_windows; exit 1; }

# Every worktree-rooted window is dead. Equivalent: no pane in the session
# has a pane_current_path starting with worktrees_root.
remaining_wt_panes=$(t_panes | awk -F'\t' -v r="$WT_ROOT/" '$4 ~ "^"r{n++} END{print n+0}')
assert_eq 0 "$remaining_wt_panes" "SPEC §5.7: every worktree-rooted window killed by X"

# pgrep for each session must drop to 0.
after_x_a=$(pgrep -cf "claude .* -n $sess_a" || true)
after_x_b=$(pgrep -cf "claude .* -n $sess_b" || true)
assert_eq 0 "$after_x_a" "A's claude killed by X"
assert_eq 0 "$after_x_b" "B's claude killed by X"

# Decoy window survives — this is the discriminating assertion.
wait_for 2 "t list-windows -F '#{window_id}' | grep -qx '$DECOY_WIN'" \
  || { echo "  FAIL: decoy window outside worktrees_root was killed by X"; t_windows; exit 1; }
assert_eq "$DECOY_WIN" "$(t list-windows -F '#{window_id}' | grep -x "$DECOY_WIN")" \
  "decoy window outside worktrees_root survives X (the cardinal §5.7 invariant)"

# harness_teardown fires from the EXIT trap installed in tmuxq.
exit 0
