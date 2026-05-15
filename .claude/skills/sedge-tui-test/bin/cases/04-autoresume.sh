#!/usr/bin/env bash
# 04-autoresume.sh — SPEC.md §5.4
#
# Invariant under test: when sedge spawns a worktree whose Claude project
# dir ($CLAUDE_CONFIG_DIR/projects/<encoded-cwd>/) holds JSONL session
# history, the claude command line includes `--continue`. Worktrees with
# no prior JSONL history must spawn WITHOUT `--continue` — even when the
# project dir is non-empty for some unrelated reason (settings.json,
# .lock files, IDE metadata, etc).
#
# Three worktrees exercise the three cases:
#
#   feat-resume — project dir pre-seeded with a fake `*.jsonl` session
#                 file. EXPECT `--continue` present.
#   feat-fresh  — no project dir at all. EXPECT `--continue` absent.
#   feat-noise  — project dir exists and is non-empty, but contains only
#                 NON-jsonl files (settings.json, a .lock). EXPECT
#                 `--continue` ABSENT. SPEC §5.4 says history is JSONL,
#                 not "any directory entry".
#
# The encoded path scheme mirrors internal/project/recycle.go's
# encodeClaudeProject: each '/', '.', ' ', '~' becomes '-'. If sedge ever
# changes that scheme, this case will fail loudly and prompt an update.

set -euo pipefail
source "$(dirname "$0")/../tmuxq"

harness_setup

# ---------------------------------------------------------------------------
# Env-hygiene workaround (mirrors case 03): the default harness_setup spawns
# sedge via the pane's login shell, whose dot-files can re-export the user's
# real SEDGE_HOME / CLAUDE_CONFIG_DIR and silently shadow the scratch dirs.
# Restart sedge with the harness env explicitly forwarded so the under-test
# sedge actually sees our per-case tmpdir.
# TODO(tmuxq): teach harness_setup to bypass shell rc files (e.g. via
# `exec env -i ... sedge`) so this dance isn't required per-case.
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
# Fixtures: two throwaway git repos with isolated identity so we don't lean
# on host-global git config.
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

REPO_RESUME="$HARNESS_TMP/repo-resume"
REPO_FRESH="$HARNESS_TMP/repo-fresh"
REPO_NOISE="$HARNESS_TMP/repo-noise"
mk_repo "$REPO_RESUME"
mk_repo "$REPO_FRESH"
mk_repo "$REPO_NOISE"

# Mirror internal/project/recycle.go encodeClaudeProject: each /, ., space,
# or ~ → '-'. Implemented with `tr` to match the Go switch byte-for-byte.
encode_cwd() {
  printf '%s' "$1" | tr '/.~ ' '----'
}

# Default worktree path convention used by sedge's path prompt:
#   $SEDGE_HOME/worktrees/<basename(project_path)>/<session_name>
# Pre-compute the resume worktree's path so we can seed history at the
# encoded location BEFORE the spawn fires.
WT_RESUME="$SEDGE_HOME/worktrees/repo-resume/feat-resume"
WT_FRESH="$SEDGE_HOME/worktrees/repo-fresh/feat-fresh"
WT_NOISE="$SEDGE_HOME/worktrees/repo-noise/feat-noise"

# Seed JSONL for the resume worktree. A *.jsonl file is the only thing
# that should mean "this worktree has Claude session history".
SEED_DIR="$CLAUDE_CONFIG_DIR/projects/$(encode_cwd "$WT_RESUME")"
mkdir -p "$SEED_DIR"
printf '{"type":"meta","ts":"2025-01-01T00:00:00Z"}\n' > "$SEED_DIR/seed-session.jsonl"

# Defensive: make sure nothing has accidentally seeded history for the
# "fresh" worktree (e.g. a stale tmpdir reuse).
FRESH_DIR="$CLAUDE_CONFIG_DIR/projects/$(encode_cwd "$WT_FRESH")"
rm -rf "$FRESH_DIR"

# Seed the noise worktree's project dir with NON-jsonl entries only. If
# HasClaudeHistory ever degrades to "any directory entry" semantics
# (the §4.2 over-eager bug Implementer-1 flagged), this seed will trip
# --continue on a worktree that has no real session history.
NOISE_DIR="$CLAUDE_CONFIG_DIR/projects/$(encode_cwd "$WT_NOISE")"
mkdir -p "$NOISE_DIR"
printf '{"theme":"dark"}\n' > "$NOISE_DIR/settings.json"
printf '\n'                  > "$NOISE_DIR/.lock"

echo "  [debug] seeded JSONL at: $SEED_DIR/seed-session.jsonl"
echo "  [debug] seeded NON-jsonl at: $NOISE_DIR/settings.json + .lock"

# ---------------------------------------------------------------------------
# Register both projects via the CLI (writes config.toml under our scratch
# SEDGE_HOME), then reload the TUI so the new rows render.
# ---------------------------------------------------------------------------
SEDGE_HOME="$SEDGE_HOME" CLAUDE_CONFIG_DIR="$CLAUDE_CONFIG_DIR" "$SEDGE_BIN" add "$REPO_RESUME" >/dev/null
SEDGE_HOME="$SEDGE_HOME" CLAUDE_CONFIG_DIR="$CLAUDE_CONFIG_DIR" "$SEDGE_BIN" add "$REPO_FRESH"  >/dev/null
SEDGE_HOME="$SEDGE_HOME" CLAUDE_CONFIG_DIR="$CLAUDE_CONFIG_DIR" "$SEDGE_BIN" add "$REPO_NOISE"  >/dev/null

t send-keys -t "$SEDGE_PANE" 'r'
wait_for 5 't_capture "$SEDGE_PANE" | grep -q "repo-resume"' \
  || { echo "  ERROR: repo-resume never appeared"; t_capture "$SEDGE_PANE" || true; exit 1; }
wait_for 5 't_capture "$SEDGE_PANE" | grep -q "repo-fresh"' \
  || { echo "  ERROR: repo-fresh never appeared"; t_capture "$SEDGE_PANE" || true; exit 1; }
wait_for 5 't_capture "$SEDGE_PANE" | grep -q "repo-noise"' \
  || { echo "  ERROR: repo-noise never appeared"; t_capture "$SEDGE_PANE" || true; exit 1; }

# ---------------------------------------------------------------------------
# Cursor / row helpers (borrowed from case 03, kept inline so the case can
# iterate independently). TODO(tmuxq): extract once a third case wants them.
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

# Drive the +new-session flow from a *collapsed* project row.
drive_create_worktree() {
  local project_path="$1" session="$2"
  local wt_path="$SEDGE_HOME/worktrees/$(basename "$project_path")/$session"

  t send-keys -t "$SEDGE_PANE" Enter
  sleep 0.2
  wait_for 5 't_capture "$SEDGE_PANE" | grep -q "+ new session"' \
    || { echo "  ERROR: '+ new session' never rendered for $project_path"; t_capture "$SEDGE_PANE" || true; return 1; }
  t send-keys -t "$SEDGE_PANE" 'j'
  sleep 0.1
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
  # The fake claude pgrep target must include -n <session> verbatim — sedge
  # passes the session name through unmodified (internal/tmux/layout.go
  # buildClaudeCmdline). Wait for that argv to surface in pgrep.
  wait_for 15 "pgrep -fc 'claude .* -n $session' >/dev/null" \
    || { echo "  ERROR: no claude process for session $session"; pgrep -af claude || true; return 1; }
  printf '%s\n' "$wt_path"
}

# ---------------------------------------------------------------------------
# Drive: create resume / fresh / noise worktrees in that order. The
# cursor_to_row_matching walker handles the changing layout as project
# rows expand below the cursor.
# ---------------------------------------------------------------------------
cursor_to_row_matching '▸ repo-resume|▾ repo-resume' 15 >/dev/null \
  || { echo "  ERROR: could not find repo-resume row"; exit 1; }
drive_create_worktree "$REPO_RESUME" "feat-resume" >/dev/null
t select-pane -t "$SEDGE_PANE"

cursor_to_row_matching '▸ repo-fresh|▾ repo-fresh' 15 >/dev/null \
  || { echo "  ERROR: could not find repo-fresh row"; exit 1; }
drive_create_worktree "$REPO_FRESH" "feat-fresh" >/dev/null
t select-pane -t "$SEDGE_PANE"

cursor_to_row_matching '▸ repo-noise|▾ repo-noise' 15 >/dev/null \
  || { echo "  ERROR: could not find repo-noise row"; exit 1; }
drive_create_worktree "$REPO_NOISE" "feat-noise" >/dev/null
t select-pane -t "$SEDGE_PANE"

# ---------------------------------------------------------------------------
# Inspect argv via pgrep. Each session name is unique within this harness's
# process tree (we made up "feat-resume" / "feat-fresh"), so the matches
# are unambiguous.
# ---------------------------------------------------------------------------
resume_cmd=$(pgrep -af "claude .* -n feat-resume" | head -1)
fresh_cmd=$(pgrep -af "claude .* -n feat-fresh"   | head -1)
noise_cmd=$(pgrep -af "claude .* -n feat-noise"   | head -1)

[[ -n "$resume_cmd" ]] || { echo "  FAIL: no claude(-n feat-resume) found"; pgrep -af claude || true; exit 1; }
[[ -n "$fresh_cmd"  ]] || { echo "  FAIL: no claude(-n feat-fresh) found";  pgrep -af claude || true; exit 1; }
[[ -n "$noise_cmd"  ]] || { echo "  FAIL: no claude(-n feat-noise) found";  pgrep -af claude || true; exit 1; }

echo "  [debug] resume cmd: $resume_cmd"
echo "  [debug] fresh  cmd: $fresh_cmd"
echo "  [debug] noise  cmd: $noise_cmd"

assert_contains "$resume_cmd" "--continue" \
  "SPEC §5.4: claude invocation for JSONL-seeded worktree includes --continue"

if [[ "$fresh_cmd" == *"--continue"* ]]; then
  echo "  FAIL: SPEC §5.4: claude invocation for fresh worktree must omit --continue"
  echo "    fresh cmd: $fresh_cmd"
  exit 1
else
  printf '  PASS: SPEC §5.4: claude invocation for fresh worktree omits --continue\n'
fi

# §5.4 + §4.2: a project dir with NON-jsonl noise does not constitute
# history. --continue MUST be omitted for feat-noise. Until the
# implementation filters by `*.jsonl` this assertion is RED — that's
# the intended hand-off per TEAM.md §3: the case is the deliverable,
# not a green run.
if [[ "$noise_cmd" == *"--continue"* ]]; then
  echo "  FAIL: SPEC §5.4 / §4.2: claude invocation for noise-only worktree must omit --continue"
  echo "    noise cmd: $noise_cmd"
  echo "    (HasClaudeHistory should be filtering for *.jsonl, not any directory entry.)"
  exit 1
else
  printf '  PASS: SPEC §5.4: claude invocation for noise-only worktree omits --continue\n'
fi

# harness_teardown fires from the EXIT trap installed by tmuxq.
exit 0
