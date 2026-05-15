---
name: sedge-tui-test
description: Drive the sedge TUI inside an isolated tmux server with stubbed `claude`/`gh`, then assert on pane geometry, window topology, hook state, and screen contents. Use when changes touch the tmux layout, swap/join logic, hook-driven indicators, the orchestration W flow, or any code path whose correctness can only be observed at the terminal.
---

# sedge-tui-test

A repeatable, headless way to test sedge's interactive surface.

## What this skill enables

Most of sedge's correctness lives in places `go test` can't reach:

- Whether sedge's pane *actually* keeps its width when a worktree swaps in.
- Whether the slot subtree rehydrates the right number of panes.
- Whether the activity dot flips colour within ~tick after a fake hook event.
- Whether `W` lays a planner pane *below* the slot (not in place of it).
- Whether the orchestration review modal accepts `Y` and tiles N workers.
- Whether `q` cleanly evacuates the slot to a background window.

This skill drives sedge inside an **isolated tmux server** with its own
socket, stubs `claude` and `gh` with tiny shell scripts, and uses tmux's
own introspection to assert post-conditions. Nothing touches the user's
real tmux.

## How it works (mechanical overview)

```
┌─────────────────────────────────────────────────────────────────────────┐
│  test harness (Bash + jq + tmux)                                        │
│                                                                         │
│  1. spin up tmux server on private socket:                              │
│       tmux -L sedge-test new-session -d -s t -x 200 -y 50              │
│                                                                         │
│  2. shim env so sedge sees:                                             │
│       - SEDGE_HOME=$tmpdir/.sedge                                      │
│       - CLAUDE_CONFIG_DIR=$tmpdir/.claude                              │
│       - PATH=$harness/bin:$PATH      ← stub claude + gh                │
│                                                                         │
│  3. send keystrokes to sedge:                                           │
│       tmux -L sedge-test send-keys -t t:0 'n' Enter                    │
│                                                                         │
│  4. observe:                                                            │
│       tmux -L sedge-test list-panes -a -F '#{...}'                     │
│       tmux -L sedge-test capture-pane -p -t <pane>                     │
│       cat $SEDGE_HOME/hook-state/*.json | jq .                          │
│                                                                         │
│  5. tear down:                                                          │
│       tmux -L sedge-test kill-server                                   │
└─────────────────────────────────────────────────────────────────────────┘
```

Key trick: every introspection command goes through the **named socket
`-L sedge-test`**, so the harness's tmux state is fully isolated from the
user's. The harness can run while the user's normal tmux is active.

## When to invoke

Invoke this skill whenever a change could affect:

- `internal/tmux/layout.go` — pane swap/join/break/detach logic.
- `internal/tui/model.go` — keybindings, activation flow, prompts.
- `internal/hookstate/` — event classification, state file I/O.
- `cmd/sedge/hook.go` — hook entrypoint, install-hooks.
- `internal/orchestration/` — plan parsing, worker prompt synth.
- Anything labelled "fix flicker", "fix size", "fix layout" in a commit.

Skip for pure refactors that don't touch any of the above (`go test` is
enough then).

## Prerequisites

- `tmux >= 3.2`
- `jq`
- `bash >= 4`
- Go toolchain (to build sedge with `go build -o $harness/sedge ./cmd/sedge`).
- A working directory under `$TMPDIR` writeable to ~200 MiB.

## Harness layout

```
.claude/skills/sedge-tui-test/
├── SKILL.md                  this file
├── bin/
│   ├── run.sh                top-level: build sedge, set env, run cases
│   ├── tmuxq                 introspection helpers (panes/windows/dims)
│   ├── stubs/
│   │   ├── claude            fake claude (prints session id, sleeps, sources hook env)
│   │   └── gh                fake gh (prints fake PR URL on `pr create`)
│   └── cases/
│       ├── 01-launch.sh                     sedge launches, banner visible
│       ├── 02-add-project.sh                `n` registers $PWD as project
│       ├── 03-create-worktree.sh            `+ new` flow end-to-end
│       ├── 04-swap-preserves-width.sh       INVARIANT 1
│       ├── 05-swap-preserves-process.sh     INVARIANT 2
│       ├── 06-pane-rehydration.sh           INVARIANT 3
│       ├── 07-autoresume.sh                 INVARIANT 4
│       ├── 08-hook-state-atomic.sh          INVARIANT 5
│       ├── 09-activity-indicator.sh         INVARIANT 6
│       ├── 10-clean-exit.sh                 INVARIANT 7
│       ├── 11-first-activation-reflow.sh    INVARIANT 8
│       ├── 12-plan-validation.sh            INVARIANT 9
│       └── 13-orchestrate-workers.sh        W → review → spawn N
└── fixtures/
    ├── project-a/             pre-init'd git repo
    ├── project-b/             pre-init'd git repo
    └── plans/
        ├── valid.json
        ├── dup-ids.json
        └── self-loop.json
```

## How a test case is structured

Each case under `bin/cases/` is a bash script that follows this pattern:

```bash
#!/usr/bin/env bash
# 04-swap-preserves-width.sh
set -euo pipefail
source "$(dirname "$0")/../tmuxq"      # helpers: t_keys, t_panes, t_capture, t_dims

harness_setup                            # builds sedge, spawns tmux server, runs `sedge`
register_project "$FIXTURES/project-a"
register_project "$FIXTURES/project-b"
create_worktree "project-a" "feat-1"
create_worktree "project-b" "feat-2"
activate_worktree "project-a" "feat-1"

w_before=$(t_dims "$SEDGE_PANE" width)
activate_worktree "project-b" "feat-2"   # the swap under test
w_after=$(t_dims "$SEDGE_PANE" width)

assert_eq "$w_before" "$w_after" "sedge pane width must not change on swap"

harness_teardown
```

Helpers (`tmuxq`) wrap the raw tmux commands so cases stay readable:

| helper            | wraps                                                         |
| ----------------- | ------------------------------------------------------------- |
| `t_keys K…`       | `tmux -L $sock send-keys -t $TARGET K…`                       |
| `t_panes`         | `tmux -L $sock list-panes -a -F '...'` → tab-separated rows   |
| `t_capture P`     | `tmux -L $sock capture-pane -p -t P`                          |
| `t_dims P field`  | `tmux -L $sock display-message -p -t P '#{pane_$field}'`      |
| `t_windows`       | `tmux -L $sock list-windows -F '#{window_id}|#{window_name}'` |
| `assert_eq A B M` | print PASS or FAIL with diff                                  |
| `wait_for COND`   | poll up to N seconds for a predicate                          |

## Stubs

`bin/stubs/claude` — fakes a long-running claude session. Exactly enough
to make sedge happy:

```bash
#!/usr/bin/env bash
# usage as sedge invokes it: claude --permission-mode auto --add-dir X --append-system-prompt-file P -n NAME [--continue] [--agents JSON]
NAME=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    -n) NAME="$2"; shift 2 ;;
    --continue) echo "[fake-claude] resuming $NAME" ;& *) shift ;;
  esac
done
# Fire a SessionStart hook so sedge's status pipeline sees us.
[[ -n "${SEDGE_BIN:-}" ]] && echo '{"cwd":"'"$PWD"'","session_id":"fake-'$NAME'"}' | "$SEDGE_BIN" hook SessionStart
printf '[fake-claude] %s in %s — ready\n' "$NAME" "$PWD"
# Trap signals so tmux kill-pane terminates us cleanly.
trap 'exit 0' TERM INT
# Loop forever, but read stdin so the pane stays interactive.
while IFS= read -r line; do
  # Echo a fake user prompt so the harness can drive hook events.
  case "$line" in
    __hook:*) "$SEDGE_BIN" hook "${line#__hook:}" <<<'{"cwd":"'"$PWD"'","tool_name":"Bash","session_id":"fake-'$NAME'"}' ;;
    *) printf '> %s\n' "$line" ;;
  esac
done
```

`bin/stubs/gh` — fakes the PR commands the ship flow uses:

```bash
#!/usr/bin/env bash
case "$1 $2" in
  "pr list") echo '[]' ;;                 # no existing PR
  "pr create") echo "https://github.com/test/test/pull/1" ;;
  *) exit 0 ;;
esac
```

Both stubs are made first on `PATH` via `bin/run.sh`.

## Hook event injection

To test the activity indicator, the harness fires hook events directly
against `sedge hook <event>` (bypassing the stub claude), because the
state file write is the unit of behaviour under test:

```bash
echo '{"cwd":"'"$WT_PATH"'","tool_name":"Bash","session_id":"x"}' \
  | "$SEDGE_BIN" hook PreToolUse

wait_for 'jq -r .event "$SEDGE_HOME/hook-state/$(t_encode "$WT_PATH").json" | grep -q PreToolUse'
```

`t_encode` mirrors `hookstate.encode` (replace `/.~ ` with `-`).

## Invariants under test

Each invariant in `SPEC.md §5` has a numbered case file. Failure prints
the case name, the failed assertion, a `tmux capture-pane` of every
relevant pane, and the `hook-state/` snapshot. The harness exits non-zero
on any failure so it composes with `&&` chains in AGENT.md.

## Running

```bash
.claude/skills/sedge-tui-test/bin/run.sh                # all cases
.claude/skills/sedge-tui-test/bin/run.sh 04            # single case by prefix
.claude/skills/sedge-tui-test/bin/run.sh --keep         # leave tmux server up
```

`--keep` is for debugging: the named server stays alive so you can
`tmux -L sedge-test attach` and look around.

## Limitations

- **No graphical screenshot diffing.** sedge is a pure terminal app, so
  `tmux capture-pane -p` (text only) is the ground truth.
- **Colour assertions** require `tmux capture-pane -e` (ANSI mode) and
  string-matching against escape sequences. Avoid pinning specific
  ANSI codes — match on the *style*, not the *colour code*.
- **Timing.** sedge's TUI ticks every 3 s. Use `wait_for` with a 5 s
  ceiling rather than fixed `sleep`s.
- **Window manager.** The harness assumes 200×50 columns. Don't bake
  any larger size into a case.
- **Real claude.** This skill **never** invokes real `claude`. The stub
  is a deliberate substitute.

## Adding a new case

1. Drop a script under `bin/cases/NN-name.sh` (NN ≥ 14).
2. Source `../tmuxq`, call `harness_setup` / `harness_teardown`.
3. End with an `exit 0` only if every `assert_*` passed.
4. Re-run `bin/run.sh NN` to verify.

If the new case probes a property not covered by `SPEC.md §5`, add a new
numbered invariant to the spec **first**.

## Calling this skill from an agent

```
Run the sedge tui test skill — full suite, fail loudly on any invariant
violation, then report which cases passed/failed and (for any failure)
paste the captured panes.
```

The skill is non-interactive: the agent runs `bin/run.sh`, inspects the
exit code + stdout, and reports back. No human-in-the-loop.
