#!/usr/bin/env bash
# 01-pane-width-stable.sh
#
# SPEC.md §5.1 — "Sedge pane never resizes during a worktree switch."
#
# Flow:
#   1. boot two ephemeral git repos as projects A and B.
#   2. drive sedge to register both, then create one worktree per project.
#   3. activate worktree-A (first activation: one allowed reflow).
#   4. record sedge pane width.
#   5. activate worktree-B — the actual swap-pane case under test.
#   6. record sedge pane width again.
#   7. assert the two widths are equal.
set -euo pipefail
source "$(dirname "$0")/../tmuxq"

harness_setup

# ------------------------------------------------------------------
# Harness env hygiene workaround.
# The shared harness_setup exports SEDGE_HOME under $HARNESS_TMP, but
# the pane shell sources zsh init files that re-export the user's
# real SEDGE_HOME (zshrc/zshenv). Until tmuxq fixes that, this case
# scrubs prior harness-tmp project entries from whatever SEDGE_HOME
# the in-tmux sedge actually resolved to, so register_project here
# doesn't trip "project %q already registered" against stale rows.
# TODO(tmuxq): launch the in-pane sedge with `env -i` + an allowlist
# so zsh dot-files can't clobber SEDGE_HOME / PATH / CLAUDE_CONFIG_DIR.
for cfg in "$HARNESS_TMP/.sedge/config.toml" "$HOME/.sedge/config.toml" "/code/.sedge/config.toml"; do
  [[ -f "$cfg" ]] || continue
  # Drop any [[projects]] block whose path is under a previous
  # /tmp/sedge-tui-test-* tmp dir. Keep everything else intact.
  python3 - "$cfg" <<'PY' 2>/dev/null || true
import re, sys, pathlib
p = pathlib.Path(sys.argv[1])
text = p.read_text()
blocks = re.split(r'(\[\[projects\]\][\s\S]*?)(?=\[\[projects\]\]|\Z)', text)
kept = []
for b in blocks:
    if b.startswith('[[projects]]') and '/tmp/sedge-tui-test-' in b:
        continue
    kept.append(b)
new = ''.join(kept)
if new != text:
    p.write_text(new)
PY
done

# ------------------------------------------------------------------
# Build the two project repos under $HARNESS_TMP/projects/{a,b}.
# Each repo gets one committed file so DetectDefaultBranch can find
# `main` (or whatever git init.defaultBranch points to).
# ------------------------------------------------------------------
make_repo() {
  local dir="$1"
  mkdir -p "$dir"
  ( cd "$dir"
    git init -q -b main
    git config user.email "tester@example.com"
    git config user.name  "tester"
    printf 'seed\n' > README
    git add README
    git commit -q -m "seed"
  )
}
# Suffix project names with the harness tmp tail so they don't
# collide with prior runs' entries when the harness leaks into the
# user's real ~/.sedge/config.toml (the harness's SEDGE_HOME export
# can be clobbered by the pane shell's zsh init files; see the case
# postscript for what a future iteration should fix).
TAIL="${HARNESS_TMP##*/}"
PROJ_A="$HARNESS_TMP/projects/a-$TAIL"
PROJ_B="$HARNESS_TMP/projects/b-$TAIL"
make_repo "$PROJ_A"
make_repo "$PROJ_B"

# ------------------------------------------------------------------
# Small helpers that talk to the running sedge pane. These mirror the
# placeholder semantics that bin/tmuxq advertises but inlines them
# here so this case can iterate independently.
# TODO(tmuxq): extract once two cases want the same wrappers.
# ------------------------------------------------------------------
sedge_type() {
  # Send a literal string to sedge, no Enter.
  t_keys "$SEDGE_PANE" -l "$1"
}
sedge_key() {
  # Send a named key (Enter, Down, etc.) to sedge.
  t_keys "$SEDGE_PANE" "$1"
}
sedge_settle() {
  # bubbletea ticks every 3s and async cmds run between ticks; 250ms
  # is enough for a key→update→render cycle in practice. wait_for
  # callers can poll for stronger conditions where needed.
  sleep 0.25
}
wait_for_screen() {
  # Usage: wait_for_screen <regex>
  wait_for 5 "t_capture '$SEDGE_PANE' | grep -qE $(printf %q "$1")"
}

register_project() {
  # `n` opens the path prompt; type the absolute path; Enter to add.
  local path="$1"
  sedge_key 'n'
  wait_for_screen 'Path to git repo'
  sedge_type "$path"
  sedge_key Enter
  # Wait for the row to appear in the project list.
  wait_for_screen "$(basename "$path")"
}

# Walk the cursor down N rows in modeList.
cursor_down() {
  local n="$1"
  while (( n-- > 0 )); do
    sedge_key 'j'
    sedge_settle
  done
}

# Expand the project row at the current cursor and step to its
# "+ new session" sentinel.
expand_and_pick_new() {
  sedge_key Enter           # expand the project row
  wait_for_screen '\+ new session'
  cursor_down 1             # cursor was on project row; "+ new" is just below
}

create_worktree() {
  # Caller is responsible for placing the cursor on the project row
  # whose "+ new session" should be invoked. session_name is fed into
  # the session-name prompt; the rest of the flow takes defaults.
  local session_name="$1"
  expand_and_pick_new
  sedge_key Enter           # activate "+ new session"
  wait_for_screen 'Name for new session'
  sedge_type "$session_name"
  sedge_key Enter
  wait_for_screen 'Pick a branch'
  sedge_key Enter           # accept first option: default branch as base
  wait_for_screen 'Worktree path'
  sedge_key Enter           # accept default path
  # spawnSessionCmd runs git worktree add then swaps the new claude
  # into the slot. We don't refocus sedge here — its row list will
  # show the worktree once worktreesMsg lands. Poll until the dot for
  # the new session appears, or until the slot has split off (two
  # panes in window 0 means the claude joined).
  wait_for 15 "t_capture '$SEDGE_PANE' | grep -qE '$session_name' || \
               t list-panes -t t:0 -F '#{pane_id}' 2>/dev/null | wc -l | grep -qE '^[[:space:]]*[2-9]'"
}

activate_worktree_under_first_project() {
  # Re-enter the list at top, walk to the named worktree row, Enter.
  # In modeList we can't directly jump; rely on the cursor sitting
  # somewhere after a create_worktree call and walk up to the
  # worktree row. After create_worktree, focus may be on the claude
  # pane — refocus sedge first.
  t select-pane -t "$SEDGE_PANE"
  sedge_settle
}

# ------------------------------------------------------------------
# Drive the flow.
# Initial cursor is on the first project row once cfg.Projects[0]
# appears. After register_project A then register_project B, the
# tree shows two project rows; cursor stays at index 0 (project A).
# ------------------------------------------------------------------
register_project "$PROJ_A"
register_project "$PROJ_B"

# Capture the windows-list before any worktree activation: useful in
# the failure capture so a future iteration can see initial topology.
echo "  [debug] initial panes:"
t_panes | sed 's/^/    /'

# Cursor is on project A. Create worktree-A.
create_worktree "feat-a"

# After create_worktree the slot is showing the fake claude for A.
# Refocus sedge to keep driving the list.
activate_worktree_under_first_project

# Move cursor down to project B's row and create worktree-B.
# Layout after worktree-A created (project A expanded):
#   row 0 : ▸/▾ project-a
#   row 1 :   │ ● feat-a   <- the new worktree
#   row 2 :   │ + new session
#   row 3 : ▸ project-b
# So we need to step down 3 rows to land on project-b.
cursor_down 3
create_worktree "feat-b"

# Activation flow under test:
#  - We need to activate worktree-A first (post-creation it's already
#    the slot, but doing an explicit activate normalises the path so
#    the *next* activation is a pure swap, not a join).
#  - Record sedge pane width.
#  - Activate worktree-B (the swap under test).
#  - Record sedge pane width.
activate_worktree_under_first_project

# Walk cursor to worktree-A row. After creating both, project B is
# expanded too. Layout:
#   0 ▸ project-a (collapsed? no — expanded earlier remains)
#   1   │ ● feat-a
#   2   │ + new session
#   3 ▸ project-b
#   4   │ ● feat-b
#   5   │ + new session
# Cursor is at row 4 (the new worktree we just created sits at +1
# below project-b post-create_worktree's expand+down). Move up 3 to
# land on feat-a.
sedge_key 'k'; sedge_settle
sedge_key 'k'; sedge_settle
sedge_key 'k'; sedge_settle
sedge_key Enter
sedge_settle
# After Enter on a worktree row, swapToWorktreeCmd runs. Let layout
# settle, then refocus sedge to measure ITS width.
sleep 0.3
t select-pane -t "$SEDGE_PANE"
w_a=$(t_dims "$SEDGE_PANE" width)

# Now navigate to feat-b and activate it — this is the swap under
# test (both A and B have live windows; swap-pane should preserve
# sedge's column count exactly).
sedge_key 'j'; sedge_settle  # to row 2 (+ new session of a)
sedge_key 'j'; sedge_settle  # to row 3 (project-b)
sedge_key 'j'; sedge_settle  # to row 4 (feat-b)
sedge_key Enter
sleep 0.3
t select-pane -t "$SEDGE_PANE"
w_b=$(t_dims "$SEDGE_PANE" width)

echo "  [debug] sedge pane width before swap: $w_a"
echo "  [debug] sedge pane width after  swap: $w_b"
echo "  [debug] final panes:"
t_panes | sed 's/^/    /'

# Sanity: the slot must actually contain a sibling pane after the
# swap, or the width-equality assertion is trivially true. SPEC.md
# §5.1 only constrains the swap path when "both source and target
# windows already have pane trees".
slot_pane_count=$(t list-panes -t t:0 -F '#{pane_id}' | wc -l | tr -d ' ')
assert_neq "1" "$slot_pane_count" "sedge window must hold a sibling claude pane post-swap"

assert_eq "$w_a" "$w_b" "sedge pane width must not change on swap"

# harness_teardown runs from the EXIT trap installed by tmuxq.
exit 0
