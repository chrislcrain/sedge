# sedge 🦢

_A sedge is a traditional collective noun for a group of cranes._

## What is "sedge"?

A TUI harness for [Claude Code](https://claude.com/claude-code), inspired by
[Coder Mux](https://github.com/coder/mux). sedge lets you run multiple Claude
sessions in parallel against any of your registered git repos, isolates each
in its own worktree, surfaces in-flight sub-agent calls as a live tree, and
uses real tmux windows so background sessions keep working while you're
looking at something else.

> sedge harnesses Claude Code; it doesn't replace it. The orchestrator is
> still `claude` (with auto mode and your normal account). sedge owns the
> tmux layout, the worktree lifecycle, the system-prompt merge, and the
> cross-session view.

## Status

Personal tool, MIT-licensed, tracking my own usage. Things change.

## Install

```sh
brew install go tmux                                    # if not already present
go install github.com/chrislcrain/sedge/cmd/sedge@latest
```

`go install` drops the binary at `$(go env GOPATH)/bin/sedge` — make sure
that's on your `PATH`.

Claude Code itself must be installed and on `PATH`; sedge invokes the `claude`
CLI to spawn each session.

## Quick start

```sh
sedge init                    # create ~/.sedge/{AGENTS.md,agents.json,config.toml,...}
cd ~/code/some-repo
sedge add                     # register the current repo as a sedge project
sedge                         # launch the TUI
```

If you're outside tmux, `sedge` creates and attaches a session named
`sedge`. If you're inside tmux, it opens a new window in your current
session and switches to it. Sedge always lives in its own dedicated window.

## Layout

```
┌─────────────┬──────────────────────────────────────────────┐
│ +========+  │                                              │
│ | sedge  |  │                                              │
│ +========+  │            claude session pane               │
│             │       (the "slot" — one at a time)           │
│  projects   │                                              │
│  + tree     │                                              │
│             │                                              │
│  keybinds   │                                              │
└─────────────┴──────────────────────────────────────────────┘
       ^                          ^
       |                          |
   sedge pane              "slot" pane next to sedge — swaps
   (~34 cols default)      between worktree sessions on Enter
```

Other claude sessions you've spawned live in **detached background tmux
windows** in the same session. Selecting a worktree in sedge breaks the
current slot pane back to its background window (preserving the running
process and scrollback) and joins the target's pane into the slot — no
process is killed.

## TUI

```
╔═══════════════════════════════════╗
║                    __             ║
║   _____ ___   ____/ /____ _ ___   ║
║  / ___// _ \ / __  // __ `// _ \  ║
║ (__  )/  __// /_/ // /_/ //  __/  ║
║/____/ \___/ \__,_/ \__, / \___/   ║
║                   /____/          ║
╚═══════════════════════════════════╝

  ▾ my-app   (2)
   │ ● feature-payments        ← currently in the slot (green dot)
   │ ⚠ explore-cache           ← background, has new output (amber)
   │   ↳ explorer find caches  ← live sub-agent under explore-cache
   │ + new session
─────────────────────────────
  ▸ docs   (0)
─────────────────────────────
  ▸ infra  (1)
```

| dot                | meaning                                                        |
| ------------------ | -------------------------------------------------------------- |
| `●` green          | active in the slot pane                                        |
| `⚠` amber blinking | live in background AND has new tmux activity since last viewed |
| `◐` gray           | live in background, idle                                       |
| `○` gray           | dormant — no live pane (will spawn fresh on selection)         |

Sub-agent calls (Claude's `Agent` tool) appear as `↳ <type> <description>`
indented under their parent worktree, and disappear automatically as soon as
the sub-agent's `tool_result` arrives (3 s polling).

### Keys

| key                   | action                                                                                                                                                 |
| --------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `j` / `k` (or arrows) | navigate                                                                                                                                               |
| `Enter`               | on project: expand / collapse · on worktree: swap into slot · on `+ new session`: prompt-create · on `↳ subagent`: tail its log in a pane below claude |
| `n`                   | add a new project (path prompt with `Tab` autocomplete)                                                                                                |
| `o`                   | open a shell pane to the right of the claude pane in the worktree's cwd                                                                                |
| `D`                   | delete the worktree under the cursor (confirms `y/N`, recycles to `~/.sedge/recycle/`)                                                                 |
| `P`                   | push the worktree's branch to `origin` and open (or update) a PR against its source branch (confirms `y/N`)                                            |
| `e`                   | open `~/.sedge/config.toml` in `$EDITOR`                                                                                                               |
| `r`                   | reload everything                                                                                                                                      |
| `q`                   | quit sedge — background sessions keep running                                                                                                          |
| `X`                   | confirm, then kill **all** sedge claude windows in the session and quit                                                                                |
| `Esc`                 | cancel any prompt or confirm                                                                                                                           |

Pane navigation between the sedge pane, the claude pane, and any code/log
panes uses your normal tmux bindings (`Ctrl-b h/j/k/l` by default).

## Worktree creation

Pressing `Enter` on `+ new session` opens two prompts in sequence:

1. **Session name** — a slug; empty falls back to `s<unix-timestamp>`.
2. **Branch** — interpreted three ways:
   - **empty** → branch off the project's default branch as `sedge/<session>`
   - **existing local branch** (Tab completes) → check that branch out directly in the worktree (no new branch created)
   - **any other name** → create that as a new branch off the default branch

A sidecar `.sedge-meta.toml` is written into each worktree recording its
source branch, so a later `P` can push and open a PR without re-prompting.

## Push + open PR (`P`)

Cursor on a worktree, `P`, confirm `y` → sedge runs `git push -u origin
<worktree-branch>` from inside the worktree, then opens (or updates) a PR
against `<source-branch>`. The confirm prompt is a dry-run preview:

```
Push sedge/feat-foo → PR against main?
  3 commits to push · PR exists — will update https://github.com/.../pull/42
Proceed? (y/N)
```

Three notable behaviours:

- **No direct pushes to the source branch.** If the worktree happens to be
  checked out on its source branch (`worktree_branch == source_branch`, e.g.
  you picked "check out existing" on `main`), sedge first carves the commits
  onto a fresh `sedge/<session>` branch (resetting local `main` back to
  `origin/main` when safe), updates `.sedge-meta.toml`, then pushes that
  new branch. The preview surfaces this as "*worktree is on main — will
  carve commits onto sedge/foo first*".
- **Existing PR detection.** Before creating a PR, sedge runs
  `gh pr list --head <branch> --base <source>`. If an open PR already
  exists, it skips `gh pr create` and reports the existing URL — pushing
  alone is enough because PRs track a branch ref.
- **`gh`-optional.** The push itself is plain `git push`, so it uses
  whatever auth your `origin` remote is configured for (SSH key, HTTPS
  token, etc.). `gh` is only consulted for PR list/create. If `gh` is
  missing, unauthenticated, or otherwise fails, sedge falls back to the
  *"Create a pull request for X by visiting: …"* URL that GitHub itself
  prints on first push, or synthesises a `compare/<source>...<head>?expand=1`
  URL from the origin remote on subsequent pushes. End result: SSH-only
  setups work end-to-end; you click the link to open the PR manually.

## Delete + recycle

`D` on a worktree, confirm `y` → sedge:

1. Kills the worktree's claude pane (slot or background window).
2. Runs `git worktree remove --force`.
3. Moves both the worktree dir and its Claude session history dir
   (`$CLAUDE_CONFIG_DIR/projects/<encoded-cwd>/`, falling back to
   `~/.claude/projects/<encoded-cwd>/` when the env var is unset) into
   `~/.sedge/recycle/<timestamp>-<project>-<session>/`.

The conversation history isn't gone — just recycled. Empty
`~/.sedge/recycle/` whenever you want.

## Sub-agents

`sedge init` writes `~/.sedge/agents.json` with four built-in sub-agents
sedge passes to every claude session via `claude --agents`:

| name        | purpose                               |
| ----------- | ------------------------------------- |
| `explorer`  | fast read-only codebase exploration   |
| `planner`   | implementation plans, no edits        |
| `reviewer`  | independent diff review               |
| `validator` | runs tests/lint/typecheck and reports |

Each is configured to **refuse further delegation** (depth=1 hard stop in
the prompt) so the orchestrator's children can't recurse. Add or replace
them by editing `~/.sedge/agents.json`.

In-flight calls render as `↳ <type> <description>` rows in the tree (3 s
polling against the session JSONL files). `Enter` on one splits a viewer
pane below claude that tails the sub-agent's own conversation, formatted
with ANSI color, until you close it with tmux's normal pane kill.

## Configuration — `~/.sedge/config.toml`

```toml
default_permission_mode = "auto"           # claude --permission-mode value
default_model           = ""               # empty = let claude pick
worktrees_root          = "~/.sedge/worktrees"

# layout
sedge_width_cols        = 34               # minimum width of the sedge pane;
                                           # auto-grows to fit a wider nameplate
                                           # (nameplate width + 4 cols margin)
slot_width_percent      = 80               # legacy fallback, only used if
                                           # sedge_width_cols is unset

# delegation policy (soft prompt-level guidance to the orchestrator)
max_parallel_subagents  = 3                # mirrors Mux's maxParallelAgentTasks
max_subagent_depth      = 3                # mirrors Mux's maxTaskNestingDepth
                                           # (sedge's built-in agents hard-stop at depth 1)

[[projects]]
name           = "sedge"
path           = "/Users/me/code/sedge"
default_branch = "main"
```

`sedge add <path>` and `sedge rm <name>` edit this file for you, but you
can also edit it by hand (`sedge edit` opens it in `$EDITOR`).

### Nameplate (`~/.sedge/nameplate.txt`)

The ASCII banner at the top of the sedge pane is read from
`~/.sedge/nameplate.txt`. `sedge init` seeds it with the default `sedge`
figlet. Edit it to anything you like — any plain-text content works (ANSI
escapes included). The sedge pane auto-resizes to fit the widest row
(plus a 4-column margin) on next launch. Hit `r` inside sedge to reload
the nameplate without restarting.

## Agent instruction hierarchy

sedge merges these in order, prepended with a delegation-policy preamble
generated from the config:

1. `~/.sedge/AGENTS.md` — global default (shipped by `sedge init`)
2. `~/.sedge/AGENTS.local.md` — personal overrides (gitignored)
3. `<repo>/AGENTS.md` — committed project-level
4. `<repo>/AGENTS.local.md` — per-clone overrides (gitignored)

The merged file lands at `~/.sedge/cache/prompts/<project>-<session>.md`
and is passed to claude via `--append-system-prompt-file`.

## CLI

```
sedge                          # launch TUI (auto-init if needed)
sedge init                     # write ~/.sedge defaults (idempotent)
sedge add [path]               # register project at path (default $PWD)
sedge ls                       # list registered projects
sedge rm <name>                # unregister project
sedge edit                     # open config.toml in $EDITOR
sedge clean [name] [--all]     # prune worktrees with no live tmux pane
sedge watch-agent <jsonl>      # tail and pretty-print a sub-agent's log
sedge version
```

Hidden:

```
sedge --inside-tmux            # internal: skip tmux detection, run TUI directly
```

## Differences from Mux

Mux **is** the agent loop — it talks to the model API directly and owns the
`task` tool, so it can hard-enforce parallelism and depth limits in its own
dispatcher. sedge is a harness around Claude Code; the agent loop is
Claude's. Concretely:

|                 | Mux                             | sedge                                                 |
| --------------- | ------------------------------- | ----------------------------------------------------- |
| agent loop      | custom (calls Anthropic API)    | Claude Code                                           |
| sub-agent tool  | own `task` (built-in MCP)       | Claude's `Agent` (built-in)                           |
| max parallel    | hard-enforced (queues)          | soft prompt guidance                                  |
| max depth       | hard-enforced (rejects)         | soft guidance + per-sub-agent "do not recurse" prompt |
| model providers | Anthropic, OpenAI, Ollama, etc. | Anthropic (whatever your `claude` is set to use)      |
| session storage | own JSONL                       | `$CLAUDE_CONFIG_DIR/projects/...` (default `~/.claude/projects/...`) |

If you want hard-enforced parallelism, you'd need an MCP shim that replaces
Claude Code's `Agent` tool with a sedge-owned implementation that queues. PRs
welcome but it's a real architectural move.

## License

MIT. See `LICENSE` for the full text and `NOTICE` for attribution to Coder
Mux, whose `AGENTS.md` design inspired sedge's instruction hierarchy.
