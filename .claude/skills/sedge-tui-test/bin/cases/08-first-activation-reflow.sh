#!/usr/bin/env bash
# 08-first-activation-reflow.sh — SPEC.md §5.8
#
# Invariant under test:
#   First activation is the ONLY path that goes through `join-pane` and
#   causes sedge to give up columns. Every subsequent activation goes
#   through `swap-pane`, which preserves both windows' layouts and
#   preserves the global pane_id set (only window-assignment changes).
#
# Width measurements:
#   W0 — fresh sedge, no worktrees (sedge owns the full window width).
#   W1 — after creating worktree A (first activation via join-pane;
#        sedge shrank to give the slot its columns). Assert W1 != W0.
#   W2, W3, W4 — three successive A↔B activations after both worktrees
#        exist. Assert W2 == W3 == W4 == W1 (swap-pane preserves width).
#
# Pane-id stability assertion (the mechanical join-vs-swap discriminator):
#   `tmux list-panes -a -F '#{window_id} #{pane_id} #{pane_index}'`
#   captures (window_id, pane_id, pane_index) for every pane in the
#   harness server. Define the GLOBAL pane_id SET as `{pane_id}` over
#   that listing.
#     - First activation (during create A): the global pane_id set
#       grows by exactly 1 (sedge alone → sedge + A's pane).
#     - Each of the 3 subsequent A↔B activations: the global pane_id set
#       is UNCHANGED (swap-pane moves panes between windows but never
#       creates or destroys them). The per-window assignment shifts —
#       that's the "only assignment" half of the SPEC §5.8 language.
#   These two facts together MECHANICALLY discriminate join-pane (which,
#   combined with the new-window that fed it, introduces a new id) from
#   swap-pane (which only re-parents existing ids).
#
# NOTE: `Create B` also runs `new-window` before its swap-pane, so the
# pane_id set grows by 1 across that step too. That step is NOT one of
# the three "swap A↔B" activations the case asserts stability over — it's
# part of the setup. The three asserted swaps are user-initiated
# Enter-on-worktree-row activations AFTER both worktrees exist; those
# carry no new-window, so they are pure swap-pane.

set -euo pipefail
source "$(dirname "$0")/../tmuxq"

# ---- helpers (inline; same patterns as cases 01-03 / 07) -------------------

mk_repo() {
  local dir="$1"
  mkdir -p "$dir"
  ( cd "$dir" \
      && git init -q -b main \
      && git -c user.email=t@t -c user.name=t commit -q --allow-empty -m init )
}

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

# Drive `+ new session` for the project row under the cursor. Accepts
# every default; echoes the auto session name (s<unix-ts>). Caller passes
# the pre-creation pgrep count for `claude -n s<...>` so wait_for can be
# deterministic (case 02 pattern).
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

# Global SET of pane_ids across the entire harness server, sorted so it
# can be diffed line-wise. One pane_id per line, deduped.
pane_id_set() {
  t list-panes -a -F '#{pane_id}' | sort -u
}

# Per-window listing for diagnostics + the "assignment changed" half of
# the SPEC §5.8 assertion. One `#{window_id} #{pane_id}` per line, sorted.
window_pane_pairs() {
  t list-panes -a -F '#{window_id} #{pane_id}' | sort
}

# Cursor controls — copied from case 03 (which proved the
# highlighted-row-as-ground-truth approach robust to layout shifts).
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

# Walk to the worktree row for the named session, press Enter, wait for
# the session's worktree path to land in sedge's window (i.e. the swap
# completed). Caller passes the project name so we can expand-if-needed
# before searching for the row (collapsed projects hide their worktree
# rows from the rendered tree).
activate_session() {
  local session="$1" wt_path="$2" project="$3"
  cursor_to_row_matching "[▸▾] ${project}( |$|\\b)" 30 >/dev/null \
    || { echo "  ERROR: could not find project row for '$project'"; t_capture "$SEDGE_PANE" || true; return 1; }
  if current_row_text | grep -q "▸ ${project}"; then
    t send-keys -t "$SEDGE_PANE" Enter
    sleep 0.25
  fi
  cursor_to_row_matching "│.*${session}\$|│.*${session} " 30 >/dev/null \
    || { echo "  ERROR: could not locate worktree row for '$session'"; t_capture "$SEDGE_PANE" || true; return 1; }
  t send-keys -t "$SEDGE_PANE" Enter
  wait_for 10 "t_panes | awk -F'\t' -v w='$SEDGE_WIN' -v p='$wt_path' '\$2==w && \$4==p{n++} END{exit !n}'" \
    || { echo "  ERROR: swap did not land $session in sedge's window"; t_panes; return 1; }
  sleep 0.3
  t select-pane -t "$SEDGE_PANE"
}

# ---- harness setup --------------------------------------------------------
harness_setup
SEDGE_WIN=$(t display-message -p -t "$SEDGE_PANE" '#{window_id}')

# ---- W0 + initial pane-id snapshot ----------------------------------------
W0=$(t_dims "$SEDGE_PANE" width)
S0=$(pane_id_set)
n0=$(printf '%s\n' "$S0" | grep -c . || true)
printf '  INFO: W0 = %s  (panes=%s)\n' "$W0" "$n0"
assert_eq 1 "$n0" "fresh sedge has exactly 1 pane (itself)"

# ---- build the two ephemeral repos ----------------------------------------
REPO_A="$HARNESS_TMP/repo-a"
REPO_B="$HARNESS_TMP/repo-b"
mk_repo "$REPO_A"
mk_repo "$REPO_B"

add_project "$REPO_A"
add_project "$REPO_B"

# Cursor to top → on repo-a's project row.
cursor_to_top

# ---- create A (the ONE first-activation join-pane in this case) ----------
sess_a=$(create_default_worktree 0)
[[ -n "$sess_a" ]] || { echo "  FAIL: could not determine session A name"; exit 1; }
printf '  INFO: session A = %s\n' "$sess_a"

W1=$(t_dims "$SEDGE_PANE" width)
S1=$(pane_id_set)
n1=$(printf '%s\n' "$S1" | grep -c . || true)
printf '  INFO: W1 = %s  (panes=%s)\n' "$W1" "$n1"

# join-pane consumed columns from sedge.
assert_neq "$W0" "$W1" "SPEC §5.8: first activation reflows sedge pane (W0 ≠ W1)"

# Exactly one new pane_id was introduced (A's claude pane). Use comm to
# diff the two SETs of pane ids.
added_in_S1=$(comm -23 <(printf '%s\n' "$S1") <(printf '%s\n' "$S0") | wc -l | tr -d ' ')
removed_in_S1=$(comm -13 <(printf '%s\n' "$S1") <(printf '%s\n' "$S0") | wc -l | tr -d ' ')
assert_eq 1 "$added_in_S1" "first activation: exactly 1 new pane_id introduced"
assert_eq 0 "$removed_in_S1" "first activation: no pane_id destroyed"

# Resolve WT_A from live panes (the new claude's pane_current_path).
WT_A=$(t_panes | awk -F'\t' -v r="$SEDGE_HOME/worktrees/" '$4 ~ "^"r{print $4}' | sort -u | grep -m1 repo-a || true)
[[ -n "$WT_A" ]] || { echo "  FAIL: could not resolve WT_A"; t_panes; exit 1; }
printf '  INFO: WT_A = %s\n' "$WT_A"

# ---- navigate to repo-b, create B (swap-pane path, not first activation) --
for i in $(seq 1 20); do t_keys "$SEDGE_PANE" 'k'; done
t_keys "$SEDGE_PANE" Enter   # collapse repo-a
wait_for 5 "t_capture '$SEDGE_PANE' | grep -qE '▸ repo-a'"
t_keys "$SEDGE_PANE" 'j'     # land on repo-b

sleep 1.2   # auto-name (s<unix-ts>) must differ from A's
sess_b=$(create_default_worktree 1)
[[ -n "$sess_b" ]] || { echo "  FAIL: could not determine session B name"; exit 1; }
printf '  INFO: session B = %s\n' "$sess_b"
assert_neq "$sess_a" "$sess_b" "A and B have distinct auto-names"

WT_B=$(t_panes | awk -F'\t' -v r="$SEDGE_HOME/worktrees/" '$4 ~ "^"r{print $4}' | sort -u | grep -m1 repo-b || true)
[[ -n "$WT_B" ]] || { echo "  FAIL: could not resolve WT_B"; t_panes; exit 1; }
printf '  INFO: WT_B = %s\n' "$WT_B"

# Width must be PRESERVED across create B (this activation is NOT the
# first; it goes through swap-pane). Spec §5.8 only carves out a single
# reflow ever.
W_create_B=$(t_dims "$SEDGE_PANE" width)
assert_eq "$W1" "$W_create_B" "create-B activation is via swap-pane → sedge width preserved"

# After create B: B is in slot, A in its background window. Three swaps
# (A→B→A) follow. Capture the baseline pane_id set right before swap 1
# so the global-set-stability assertions diff against a stable reference.
S_baseline=$(pane_id_set)
WP_baseline=$(window_pane_pairs)
n_baseline=$(printf '%s\n' "$S_baseline" | grep -c . || true)
printf '  INFO: post-create-B pane set: %s panes\n' "$n_baseline"
assert_eq 3 "$n_baseline" "after create B: 3 panes (sedge + A + B)"

# ---- swap 1: activate A ---------------------------------------------------
S_before_1=$(pane_id_set)
WP_before_1=$(window_pane_pairs)
activate_session "$sess_a" "$WT_A" "repo-a" || exit 1
W2=$(t_dims "$SEDGE_PANE" width)
S_after_1=$(pane_id_set)
WP_after_1=$(window_pane_pairs)
printf '  INFO: W2 = %s (after swap A in)\n' "$W2"

assert_eq "$W1" "$W2" "swap 1 preserves sedge width"
assert_eq "$S_before_1" "$S_after_1" "swap 1: global pane_id set unchanged"
# The per-window assignment MUST have changed (A and B exchanged windows).
# Otherwise no swap actually happened and the width-equality is vacuous.
assert_neq "$WP_before_1" "$WP_after_1" "swap 1: per-window assignment changes"

# ---- swap 2: activate B ---------------------------------------------------
S_before_2=$(pane_id_set)
WP_before_2=$(window_pane_pairs)
activate_session "$sess_b" "$WT_B" "repo-b" || exit 1
W3=$(t_dims "$SEDGE_PANE" width)
S_after_2=$(pane_id_set)
WP_after_2=$(window_pane_pairs)
printf '  INFO: W3 = %s (after swap B in)\n' "$W3"

assert_eq "$W1" "$W3" "swap 2 preserves sedge width"
assert_eq "$S_before_2" "$S_after_2" "swap 2: global pane_id set unchanged"
assert_neq "$WP_before_2" "$WP_after_2" "swap 2: per-window assignment changes"

# ---- swap 3: activate A ---------------------------------------------------
S_before_3=$(pane_id_set)
WP_before_3=$(window_pane_pairs)
activate_session "$sess_a" "$WT_A" "repo-a" || exit 1
W4=$(t_dims "$SEDGE_PANE" width)
S_after_3=$(pane_id_set)
WP_after_3=$(window_pane_pairs)
printf '  INFO: W4 = %s (after swap A in)\n' "$W4"

assert_eq "$W1" "$W4" "swap 3 preserves sedge width"
assert_eq "$S_before_3" "$S_after_3" "swap 3: global pane_id set unchanged"
assert_neq "$WP_before_3" "$WP_after_3" "swap 3: per-window assignment changes"

# ---- closing summary: W2 == W3 == W4 == W1 --------------------------------
# Already covered pairwise above, but re-state the trio explicitly so the
# SPEC §5.8 invariant is named in the case output.
assert_eq "$W1" "$W2" "SPEC §5.8: W2 == W1"
assert_eq "$W1" "$W3" "SPEC §5.8: W3 == W1"
assert_eq "$W1" "$W4" "SPEC §5.8: W4 == W1"

# ---- closing summary: pane_id stability across all 3 swaps ----------------
# Compare the post-swap-3 pane set against the baseline captured just
# after create B. Three swap-pane operations later, the global set MUST
# still be identical.
assert_eq "$S_baseline" "$S_after_3" "SPEC §5.8: global pane_id set unchanged across 3 swap-pane runs"

exit 0
