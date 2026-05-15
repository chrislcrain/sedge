#!/usr/bin/env bash
# 02-no-kill-on-swap.sh — SPEC.md §5.2
#
# Proves: no claude process is killed when sedge swaps a different worktree
# into the visible slot. The swap is implemented via `tmux swap-pane`, which
# relocates panes between windows without restarting the processes attached
# to them. Concretely: after activating worktree A and then activating
# worktree B, the fake-claude spawned for A must still be alive (now in its
# background tmux window, not in the slot).
#
# Counter pattern: `pgrep -cf 'claude .* -n <session>'` matches the fake
# claude's command line (the stub at bin/stubs/claude is invoked with
# `-n <session>` verbatim by sedge — see internal/tmux/layout.go
# buildClaudeCmdline). The pgrep count must be 1 before AND after the swap.
#
# Caveats handled:
#  - Sedge auto-names sessions as "s<unix-ts>" when the user accepts the
#    default name. Two creates one second apart can produce identical
#    names; the case waits a beat between flows.

set -euo pipefail
source "$(dirname "$0")/../tmuxq"

# ---- helpers (inline; TODO(tmuxq): extract once a second case needs them) --

mk_repo() {
  local dir="$1"
  mkdir -p "$dir"
  ( cd "$dir" \
      && git init -q -b main \
      && git -c user.email=t@t -c user.name=t commit -q --allow-empty -m init )
}

# Drives the `n` add-project prompt for a given absolute path. Returns when
# the project name is visible in the rendered tree.
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

# Drives `+ new session` for the project row currently expanded under the
# cursor. Accepts every default; returns the slugified session name on
# stdout so the caller can pgrep for it.
#
# Precondition: cursor is on a `rowProject` line; project has no worktrees
# yet (so `+ new session` is the row immediately below it after expand).
create_default_worktree() {
  # Expand the project so `+ new session` becomes a row.
  t_keys "$SEDGE_PANE" Enter
  wait_for 5 "t_capture '$SEDGE_PANE' | grep -qF '+ new session'"
  # Down once to land on `+ new session`.
  t_keys "$SEDGE_PANE" 'j'
  t_keys "$SEDGE_PANE" Enter
  wait_for 5 "t_capture '$SEDGE_PANE' | grep -qi 'Name for new session'"
  t_keys "$SEDGE_PANE" Enter  # accept auto name
  wait_for 5 "t_capture '$SEDGE_PANE' | grep -qi 'Pick a branch'"
  t_keys "$SEDGE_PANE" Enter  # accept default branch (sedge/<auto>)
  wait_for 5 "t_capture '$SEDGE_PANE' | grep -qi 'Worktree path'"
  t_keys "$SEDGE_PANE" Enter  # accept default path → spawnSessionCmd fires
  # spawnSessionCmd creates a worktree on disk, spawns a claude window,
  # then swap-pane (or join-pane on first activation) brings it into the
  # slot. We can't rely on the spawned window still existing — join-pane
  # destroys the source window — so wait on pgrep instead. Diagnostics
  # go to stderr so the final session-name echo is captured cleanly.
  local prev_count="${1:-0}"
  wait_for 15 "[[ \$(pgrep -cf 'claude .* -n s[0-9][0-9]*') -gt $prev_count ]]" 1>&2
  # Newest session name = the highest s<unix-ts> currently in pgrep.
  local sess
  sess=$(pgrep -af 'claude .* -n s[0-9][0-9]*' \
           | sed -nE 's/.* -n (s[0-9]+).*/\1/p' \
           | sort -u | sort -t s -k 2 -n | tail -1)
  printf '%s\n' "$sess"
}

# ---- test body -------------------------------------------------------------
harness_setup

REPO_A="$HARNESS_TMP/repo-a"
REPO_B="$HARNESS_TMP/repo-b"
mk_repo "$REPO_A"
mk_repo "$REPO_B"

# 1. Register both projects. Cursor starts on row 0 and stays there
#    (addProjectCmd doesn't move the cursor).
add_project "$REPO_A"
add_project "$REPO_B"

# 2. Walk to top so we're on repo-a (the first project row).
for i in $(seq 1 20); do t_keys "$SEDGE_PANE" 'k'; done

# 3. Create worktree A. Pass the current claude-with-s-name count so the
#    helper's wait_for can tell "spawn finished" from "nothing happened".
sess_a=$(create_default_worktree 0)
[[ -n "$sess_a" ]] || { echo "  FAIL: could not determine session A name"; exit 1; }
printf '  INFO: session A = %s\n' "$sess_a"

# Snapshot the kill-counter for A *before* the swap. We scope by the
# exact session name so the real claude running outside the harness can't
# pollute the count.
before_a=$(pgrep -cf "claude .* -n $sess_a" || true)
assert_eq 1 "$before_a" "fake-claude for session A is alive before the swap" || {
  echo "  DEBUG: pgrep snapshot ↓"
  pgrep -af 'claude .* -n s[0-9]+' || true
  exit 1
}

# 4. Move cursor to repo-b. Layout after creating A's worktree:
#      ▾ repo-a (1)
#        │ ● s<ts-a>
#        │ + new session
#      ▸ repo-b (0)
#    Spam 'k' to top → cursor on repo-a project row → Enter to collapse →
#    'j' once to land on repo-b project row.
for i in $(seq 1 20); do t_keys "$SEDGE_PANE" 'k'; done
t_keys "$SEDGE_PANE" Enter
wait_for 5 "t_capture '$SEDGE_PANE' | grep -qE '▸ repo-a'"
t_keys "$SEDGE_PANE" 'j'

# Sleep a beat so the auto-generated "s<unix-ts>" for B differs from A.
sleep 1.2

# 5. Create worktree B — sedge spawns a fresh claude pane for B, then
#    swap-pane exchanges A (slot) with B. After the swap, A's pane has
#    moved back to A's background window; A's claude must still be alive.
sess_b=$(create_default_worktree 1)
[[ -n "$sess_b" ]] || { echo "  FAIL: could not determine session B name"; exit 1; }
printf '  INFO: session B = %s\n' "$sess_b"

# Sanity check: A and B must have distinct names (else the pgrep below
# can't tell them apart and the assertion is vacuous).
assert_neq "$sess_a" "$sess_b" "session A and B have distinct auto-names"

# 6. The swap under test has now happened. Assert A's claude is still
#    alive (process not killed by the swap-pane).
after_a=$(pgrep -cf "claude .* -n $sess_a" || true)
after_b=$(pgrep -cf "claude .* -n $sess_b" || true)

assert_eq "$before_a" "$after_a" "SPEC §5.2: claude(-n $sess_a) survives the swap"
assert_eq 1 "$after_b" "exactly one claude(-n $sess_b) is alive after activation"

# harness_teardown runs via the EXIT trap installed in tmuxq.
exit 0
