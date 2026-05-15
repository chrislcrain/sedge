# sedge — Feature Specification

> sedge is a TUI harness for [Claude Code](https://claude.com/claude-code) that
> manages multiple parallel claude sessions across git worktrees inside a
> single tmux session. This document captures what sedge is *supposed to be*,
> independent of any single commit. It is the spec that AGENT.md and TEAM.md
> implement against.

## 1. Mission

sedge owns the tmux layout, the git-worktree lifecycle, and the cross-session
view. It does **not** own the agent loop — Claude Code does. sedge is the
controller; claude is the worker.

The user should be able to:

1. Register any number of git repos as "projects".
2. Spin up isolated worktrees per topic / feature / experiment.
3. Have a long-running claude in each worktree, all visible from one place.
4. Switch between worktrees as fast as switching tmux windows, *without
   losing*: the running claude process, the conversation history, the user's
   own pane splits inside that worktree, or the cwd of any pane.
5. See, at a glance, which worktrees are working, which are blocked on the
   user, and which are idle — driven by real `claude-code` hook events, not
   filesystem polling guesses.
6. Ship a worktree (push + open/update PR) without leaving the TUI.
7. Orchestrate multi-pane plans where a planner claude proposes a DAG of
   sibling worker claudes and sedge spawns them on approval.

## 2. Non-goals

- **Replacing the agent loop.** sedge does not call Anthropic's API directly.
  Parallelism and recursion limits are *soft* (prompt-level guidance), not
  hard-enforced queues.
- **Multi-host.** sedge assumes one tmux server on one machine.
- **Replacing tmux key bindings.** Pane navigation, copy mode, scrollback —
  all the user's normal tmux.
- **A general-purpose terminal multiplexer.** sedge is tmux + claude
  scaffolding; it doesn't try to be a tmux replacement.

## 3. Architecture overview

```
┌──────────────────────────────── tmux session "sedge" ─────────────────────────────────┐
│                                                                                       │
│  ┌─── window 0: sedge ─────────────────────────────────────────────────────────────┐  │
│  │  ┌─sedge pane─┐  ┌──────────────── slot pane (one at a time) ─────────────────┐ │  │
│  │  │            │  │                                                            │ │  │
│  │  │  banner    │  │  claude (worktree A)                                       │ │  │
│  │  │  tree      │  │  + any user splits owned by worktree A                     │ │  │
│  │  │  status    │  │                                                            │ │  │
│  │  │  keybinds  │  │                                                            │ │  │
│  │  └────────────┘  └────────────────────────────────────────────────────────────┘ │  │
│  └────────────────────────────────────────────────────────────────────────────────┘  │
│                                                                                       │
│  ┌─── window 1: worktree B ──────────┐  ┌─── window 2: worktree C ──────────────────┐ │
│  │  claude (background, alive)       │  │  claude (background, alive)               │ │
│  │  + worktree B's own splits        │  │  + worktree C's own splits                │ │
│  └───────────────────────────────────┘  └───────────────────────────────────────────┘ │
└───────────────────────────────────────────────────────────────────────────────────────┘
```

- sedge always lives in **its own pane** inside one tmux window.
- Exactly **one** worktree is "in the slot" — visible next to sedge.
- Every other worktree's claude pane tree lives in its **own background tmux
  window**, processes still running, scrollback intact.
- Switching is `tmux swap-pane` (pairwise across windows), so sedge's pane
  width does not change and no bubbletea reflow is triggered.

State on disk:

```
$SEDGE_HOME/                            (~/.sedge by default; env override)
├── AGENTS.md                          global system-prompt merge layer
├── AGENTS.local.md                    personal override (gitignored)
├── agents.json                        --agents JSON passed to every claude
├── config.toml                        projects, layout, delegation policy
├── nameplate.txt                      banner ascii
├── hook-state/<encoded-wt>.json       latest hook event per worktree
├── cache/prompts/<project>-<sess>.md  merged system prompts
├── recycle/<ts>-<project>-<sess>/     soft-deleted worktrees + history
└── worktrees/                         default location for managed worktrees
```

## 4. Core features

### 4.1 Worktree lifecycle

| operation     | trigger              | result                                                                                                  |
| ------------- | -------------------- | ------------------------------------------------------------------------------------------------------- |
| create        | `Enter` on `+ new`   | prompt session name → branch picker → worktree-path prompt → `git worktree add` → spawn claude in slot  |
| activate      | `Enter` on worktree  | swap that worktree's pane tree into the slot; previous slot's panes evacuate to its own window         |
| open shell    | `o` on worktree row  | split a shell to the right of claude, cwd = worktree path                                              |
| ship          | `P` on worktree row  | preflight preview, then `git push -u origin <branch>` + `gh pr create/list`                            |
| delete        | `D` on worktree row  | kill claude pane/window, `git worktree remove --force`, recycle dir + claude history                   |
| ad-hoc chat   | `A` on project row   | fresh claude at project root, no worktree, no `--continue`                                             |
| orchestrate   | `W` on worktree row  | spawn planner claude split-below; on plan approval, spawn N worker panes (see §4.6)                     |
| prune project | `D` on project row   | unregister from `config.toml`, recycle all its worktrees (repo itself untouched)                       |
| open code     | `o` on project row   | shell pane at the project's main repo path                                                              |

Branch-input semantics on create:

- empty → branch off default as `sedge/<session>`.
- existing local branch → check out directly (no new branch).
- any other string → create that branch off default.

A sidecar `.sedge-meta.toml` is written into each worktree recording its
source branch so `P` can push and open a PR without re-prompting.

### 4.2 Auto-resumption of the worktree's main claude

When the user activates a worktree:

1. sedge looks for an existing tmux window with the worktree's cwd. If
   found, that window's pane tree is the target — no new process is spawned;
   the running claude keeps running.
2. If not found, sedge spawns a new claude in a detached background window
   with `claude --continue` **iff** `$CLAUDE_CONFIG_DIR/projects/<encoded-cwd>/`
   contains JSONL session history. If no history, `--continue` is omitted.
3. The target pane tree is swapped into the slot (see §4.4).

This means **the user's main conversation per worktree is sticky**: clicking
away and back never loses context.

### 4.3 Pane and split rehydration

A worktree may contain *more than one pane*:

- The primary claude pane.
- User-opened shells (`o`).
- Sub-agent viewer splits (`Enter` on a `↳` row).
- Orchestration worker panes (post-`W` approval).
- Any manual `Ctrl-b "`, `Ctrl-b %` splits the user made.

On switch:

- All non-sedge panes in the slot evacuate to the *outgoing* worktree's
  background window, preserving their cwd, running process, and scrollback.
- All panes in the *incoming* worktree's window swap into the slot. The
  pane the user picked lands first (focused); the rest tile to its right
  (or below, per tmux layout).
- swap-pane is used pair-for-pair so neither window's layout changes. The
  sedge pane never gets resized, no flicker, no bubbletea re-render.

Asymmetric pane counts:

- *Incoming has more panes than outgoing*: extras `join-pane` into the
  slot subtree next to the new primary.
- *Outgoing has more panes than incoming*: extras `join-pane` into the
  former-incoming window, which is now the outgoing worktree's home.

First activation in a fresh sedge session is the one unavoidable case where
sedge has to give up columns to a brand-new slot pane (single reflow).

### 4.4 Slot sizing

- `sedge_width_cols` (default 34) is the **minimum** column width of the
  sedge pane. It auto-grows to the widest row in `nameplate.txt` plus a
  4-column margin.
- The slot takes the rest of the window width.
- `slot_width_percent` is a legacy fallback used only when
  `sedge_width_cols` is unset.
- swap-pane preserves both panes' widths verbatim, so the slot width
  effectively persists across switches once it's been set.

### 4.5 Hook-driven activity status

sedge does **not** poll JSONL files for activity. Instead, sedge registers
itself as a `claude-code` hook (`sedge install-hooks` merges entries into
`$CLAUDE_CONFIG_DIR/settings.json` or `~/.claude/settings.json`).

On every event, claude invokes `sedge hook <event-name>` with the standard
hook stdin payload. sedge writes a one-line state file at
`$SEDGE_HOME/hook-state/<wt-encoded>.json` containing:

```json
{ "event": "PreToolUse", "at": "<rfc3339>", "tool_name": "Bash", "session_id": "..." }
```

The TUI reads the file every refresh tick and classifies via
`hookstate.Classify()`:

| event(s)                                                  | activity            | dot      |
| --------------------------------------------------------- | ------------------- | -------- |
| `UserPromptSubmit`, `PreToolUse`, `PostToolUse`, `SubagentStop` | in flight     | yellow ● blink |
| `Notification`                                            | approval pending    | orange ● |
| `Stop`, `SessionEnd`, `SessionStart`, `PreCompact`        | idle (background)   | gray ◐   |
| state file older than `hook_stale_minutes` (default 10)   | idle (assume crash) | gray ◐   |
| no state file                                             | dormant             | gray ○   |
| worktree is in the slot                                   | active              | green ●  |

The active classification is computed from `tmux.ActiveSlotPath` —
whichever worktree's path matches the cwd of sedge's slot pane.

Hook installation is idempotent. `sedge install-hooks` may be re-run safely.

The crash-fallback threshold (last row of the table above) is the elapsed
time after which an unchanged hook-state file is taken to mean the
claude process died without writing a `Stop`/`SessionEnd` event. The
default 10 minutes is conservative enough that a long-running idle
claude is not misclassified, and short enough that an actually-crashed
session goes gray well inside one work session. It is exposed as
`hook_stale_minutes` in `config.toml` (§4.11) so test harnesses and
unusual deployments can tune it; setting it to `0` disables the
fallback entirely (file age never demotes activity).

### 4.6 Orchestration (`W` key)

`W` on a worktree spawns a **planner claude** in a split-below pane sharing
the worktree window with the existing claude (which stays visible above).
Planner system prompt instructs it to:

1. Interview the user about a multi-pane workflow.
2. Write a structured `Plan` to `<worktree>/.sedge/orchestration/plan.json`:
   ```json
   {
     "name": "fix-auth-flow",
     "summary": "rebuild login UI + migrate session middleware",
     "sessions": [
       {"id": "ui",        "task": "...", "depends_on": []},
       {"id": "middleware","task": "...", "depends_on": []},
       {"id": "integrate", "task": "...", "depends_on": ["ui","middleware"]}
     ]
   }
   ```

sedge's tick poller watches `plan.json` for an mtime past the baseline
captured at `W`-press. On a fresh write:

- Parse + validate (unique ids, deps reference known ids, no self-loop).
- Pop a review modal (`modeReviewPlan`) showing plan name, summary, and
  per-session task + dependency list.
- `Y` approves → kill planner pane, spawn one claude worker pane per
  session (horizontal split off the slot anchor), each with a per-session
  system prompt baking in: task, dep-poll loop
  (`while ! [ -e ./.sedge/orchestration/done/<dep> ]; do sleep 2; done`),
  and the completion signal
  (`touch ./.sedge/orchestration/done/<id>`). Window is then
  `select-layout tiled` for a balanced grid.
- `N`/`esc` discards the plan file so the next planner save retriggers
  review; planner pane stays alive for the user to iterate.

Workers coordinate via filesystem markers under
`.sedge/orchestration/done/<id>` — sedge is not in the runtime loop.
Re-pressing `W` kills the in-flight planner and restarts.

### 4.7 Ship (`P` key)

Pre-flight dry-run preview (`project.PreviewShip`) before any push:

```
Push sedge/feat-foo → PR against main?
  3 commits to push · PR exists — will update https://github.com/.../pull/42
Proceed? (y/N)
```

Behaviours:

- **No push to source branch.** If `worktree_branch == source_branch`,
  carve the commits onto a fresh `sedge/<session>` branch first (reset
  local source back to `origin/<source>` when safe), update
  `.sedge-meta.toml`, then push.
- **PR detection.** `gh pr list --head <branch> --base <source>` — if
  already open, skip `gh pr create` and report the URL.
- **gh-optional.** Push is plain `git push` (SSH/HTTPS auth is the user's
  remote config). `gh` is consulted only for list/create. Missing or
  unauthenticated `gh` → fall back to the `compare/<source>...<head>?expand=1`
  URL synthesised from the origin remote.

### 4.8 Sub-agents

`sedge init` writes `agents.json` with four built-in sub-agents passed via
`claude --agents`:

| name        | purpose                              | depth cap |
| ----------- | ------------------------------------ | --------- |
| `explorer`  | fast read-only codebase exploration  | 1 (hard, in prompt) |
| `planner`   | implementation plans, no edits       | 1         |
| `reviewer`  | independent diff review              | 1         |
| `validator` | runs tests/lint/typecheck, reports   | 1         |

In-flight calls render as `↳ <type> <description>` rows under the parent
worktree, polled from the session JSONL every refresh. `Enter` on one
opens a viewer pane below claude that tails the sub-agent's own
conversation via `sedge watch-agent <jsonl>` with ANSI formatting.

### 4.9 Agent instruction hierarchy

For every spawn, sedge synthesises a system-prompt file by merging:

1. `$SEDGE_HOME/AGENTS.md` (global, shipped by `init`)
2. `$SEDGE_HOME/AGENTS.local.md` (personal overrides)
3. `<repo>/AGENTS.md` (committed project-level)
4. `<repo>/AGENTS.local.md` (per-clone overrides)

Plus a delegation-policy preamble generated from
`max_parallel_subagents` / `max_subagent_depth`. The merged file lands at
`$SEDGE_HOME/cache/prompts/<project>-<session>.md` and is passed to claude
via `--append-system-prompt-file`.

### 4.10 CLI surface

```
sedge                       launch TUI (auto-init if needed)
sedge init                  write $SEDGE_HOME defaults (idempotent)
sedge add [path]            register project (default $PWD)
sedge ls                    list registered projects
sedge rm <name>             unregister project
sedge edit                  open config.toml in $EDITOR
sedge clean [name] [--all]  prune worktrees with no live tmux pane
sedge prune [name]          `git worktree prune` across registered projects
sedge watch-agent <jsonl>   tail + pretty-print sub-agent log
sedge install-hooks         merge sedge hook entries into claude settings
sedge hook <event>          (hidden) hook callback writing state files
sedge --inside-tmux         (hidden) skip tmux detection
sedge version
```

### 4.11 Configuration — `config.toml`

```toml
default_permission_mode = "auto"
default_model           = ""
worktrees_root          = "~/.sedge/worktrees"

sedge_width_cols        = 34
slot_width_percent      = 80   # legacy fallback

max_parallel_subagents  = 3
max_subagent_depth      = 3

hook_stale_minutes      = 10   # crash-fallback threshold (§4.5); 0 disables

[[projects]]
name           = "sedge"
path           = "/Users/me/code/sedge"
default_branch = "main"
```

`SEDGE_HOME` env var relocates `~/.sedge`.

## 5. Invariants (the contract)

These are the properties the test skill (see `SKILL.md`) verifies on every
loop iteration.

1. **Sedge pane never resizes during a worktree switch** when both source
   and target windows already have pane trees. Width before == width after.
2. **No claude process is killed** during a switch. `pgrep -f 'claude .* -n <session>'`
   counts the same before and after.
3. **Pane count rehydrates.** If worktree B had N panes when last viewed,
   activating it shows N panes in the slot subtree.
4. **Auto-resume.** Spawning a worktree that has JSONL history adds
   `--continue` to the claude invocation; spawning fresh history omits it.
5. **Hook state writes are atomic.** Concurrent `sedge hook` invocations
   do not corrupt the state file (temp-file + rename).
6. **Activity indicator** is derived only from hook state + tmux active
   path, never from JSONL mtime.
7. **Orphan-free exit.** `q` breaks the slot back to its own background
   window (sedge's window closes, claudes survive). `X` kills every
   window whose primary pane is rooted in `worktrees_root`.
8. **First activation** is the only path that goes through `join-pane`
   and causes a reflow. All subsequent switches go through `swap-pane`.
9. **Plan validation** rejects plans with duplicate ids, unknown deps,
   or self-loops *before* spawning any workers.

## 6. Open questions / future work

- **Hard-enforced parallelism.** Requires an MCP shim replacing claude's
  `Agent` tool with a sedge-owned queue. Currently soft.
- **Cross-machine state.** `hook-state/` is local. A remote sync would
  enable "see your home machine's worktrees from a laptop".
- **Multi-session tmux.** sedge assumes a single tmux session named
  `sedge`. Supporting attaching to arbitrary user-named sessions is
  doable but unfunded.
- **Pane snapshotting on detach.** Currently the user's tmux layout
  inside a worktree (vertical vs horizontal splits, exact ratios) is
  preserved by tmux but only as long as the worktree's background window
  exists. A `sedge` restart cold-spawns a single-pane claude and loses
  custom layouts. A `layouts.toml` per worktree could fix this.
- **Plan visualization.** The review modal lists sessions as a flat
  bulleted list. A real DAG render would be nicer for >4 sessions.
