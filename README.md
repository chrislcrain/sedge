# sedge

A TUI harness for [Claude Code](https://claude.com/claude-code), inspired by
[Coder Mux](https://github.com/coder/mux). sedge runs from anywhere on your
system, spawns Claude in auto mode inside a git worktree per session, and uses
real tmux panes for layout so pane movement "just works".

## Install

```sh
brew install go tmux                              # if not already present
go install github.com/chrislcrain/sedge/cmd/sedge@latest
```

Make sure `$GOBIN` (defaults to `$(go env GOPATH)/bin`) is on `PATH`.

## Quick start

```sh
sedge init                                        # create ~/.sedge/{AGENTS.md,config.toml,...}
cd ~/code/my-repo && sedge add                    # register the current repo
sedge                                             # launch the TUI
```

If you're not already inside tmux, `sedge` creates and attaches a session named
`sedge`. If you are inside tmux, it runs the TUI in your current pane.

## Keys

| key | action |
|-----|--------|
| `j` / `↓` | next project |
| `k` / `↑` | previous project |
| `enter`  | spawn a Claude pane in a new worktree |
| `a` / `e` | open `~/.sedge/config.toml` in `$EDITOR` |
| `r`       | reload config from disk |
| `q` / `ctrl-c` | quit |

Pane movement between sedge and Claude panes uses your normal tmux bindings
(`Ctrl-b h/j/k/l` by default).

## Commands

```
sedge                          # launch TUI
sedge init                     # write ~/.sedge defaults (idempotent)
sedge add [path]               # register project at path (default cwd)
sedge ls                       # list registered projects
sedge rm <name>                # unregister project
sedge edit                     # open config in $EDITOR
sedge clean [name] [--all]     # prune worktrees with no live tmux pane
sedge version
```

## Configuration — `~/.sedge/config.toml`

```toml
default_permission_mode = "auto"
default_model           = ""                 # empty = claude default
worktrees_root          = "~/.sedge/worktrees"

[[projects]]
name           = "sedge"
path           = "/Users/me/code/sedge"
default_branch = "main"
```

## Agent instructions

sedge merges agent instructions in hierarchical order, with later layers
appended:

1. `~/.sedge/AGENTS.md` (global default, shipped by `sedge init`)
2. `~/.sedge/AGENTS.local.md` (personal overrides)
3. `<repo>/AGENTS.md` (committed project-level)
4. `<repo>/AGENTS.local.md` (per-clone overrides)

The merged file is written to `~/.sedge/cache/prompts/<project>-<session>.md`
and passed to `claude --append-system-prompt-file`.

## How it works

When you press `enter` on a project, sedge:

1. Generates a session name (`s<unix-timestamp>`).
2. Creates a git worktree at `~/.sedge/worktrees/<project>/<session>` on
   branch `sedge/<session>`.
3. Merges the AGENTS.md hierarchy into a cache file.
4. Runs `tmux split-window -h -p 75 -c <wt-path> claude --permission-mode auto
   --add-dir <project-path> --append-system-prompt-file <merged> -n <session>`.

That's it. No daemon, no extra state — Claude handles session persistence via
its own `--session-id`, and git owns the worktrees.

## License

MIT. See `LICENSE` and `NOTICE`.
