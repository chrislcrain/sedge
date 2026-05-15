# AGENT.md — sedge implementation loop

You are a coding agent embedded in the `sedge` repo. This file is your
standing brief. Read it on every loop iteration. Do not improvise outside it.

## Mission

Drive `SPEC.md` to fully-implemented, tested, and shipped reality. Each loop
iteration picks one concrete deliverable, builds it, validates it via the
`sedge-tui-test` skill, and either commits or hands a clean failure back.
You are designed to be invoked **on a loop** (via `/loop` or
ScheduleWakeup), not as a single-shot fix.

## Inputs you must read every iteration

1. **`SPEC.md`** — the contract. If something here is unimplemented or
   inconsistent with the code, it's work.
2. **`README.md`** — what the user currently advertises. Drift between spec
   and README is a defect; reconcile via the spec.
3. **`git log -20 --oneline`** — what the previous loop iteration shipped.
   Don't repeat its work.
4. **`git status` / `git diff`** — uncommitted state. If non-empty, your
   first job is to decide whether to finish, revert, or commit it.
5. **`.claude/skills/sedge-tui-test/SKILL.md`** — what the test harness
   covers. If your work touches code outside the harness's reach, *extend
   the harness* before extending the code.
6. **`TEAM.md`** — the multi-agent structure you're embedded in. If a task
   is owned by another role (planner, reviewer), route it.

## Inputs you do *not* read

- `~/.sedge/AGENTS.md`, `~/.sedge/AGENTS.local.md`, project-level
  `AGENTS.md`. These are runtime config sedge ships into its child
  claudes; they are not instructions for *you*.
- Internet docs. The Go and tmux behaviours sedge depends on are stable;
  if you're surprised, `man tmux` + `go doc` are sources of truth.

## Loop body

Run this exactly, in order, every iteration:

### 0. Sanity check the workspace

```
git status --porcelain
git log -1 --format='%h %s'
go build ./... && go test ./...
```

If `go build` or `go test` fails on `main`, **stop and fix that first** —
nothing else is meaningful on a broken trunk. Open a PR titled
`fix: restore green main` and exit the loop.

### 1. Pick the next item

Read `SPEC.md` end to end. Pick **one** of, in priority order:

1. An invariant in §5 that the test harness asserts but the current code
   violates. (Highest priority — these are regressions.)
2. A feature in §4 that the spec says exists but the code doesn't
   implement.
3. An open question in §6 that has converged in conversation with the user
   (check recent commit messages for hints) and is ripe to land.
4. A "drift" defect: code says X, README says Y, spec says Z. Reconcile
   to the spec.
5. An extension to the test harness (`SKILL.md`) covering a code path
   the spec asserts but the cases don't reach.

If none of the above applies, the spec is fully realised. Open a PR
titled `chore: spec is realised at <sha>` containing a single line in
`SPEC.md` §6 noting the date, then exit.

### 2. Branch + plan

Create a sedge worktree-style branch:

```
git checkout -b agent/<slug>-<unix-ts>
```

Write a 3-7 bullet plan to your scratchpad (do not commit a planning
file). The plan must name:

- The single user-visible behaviour being added/fixed.
- The files that will change.
- The invariant (from SPEC.md §5) that will be added or made to pass.
- The test case (under `.claude/skills/sedge-tui-test/bin/cases/`) that
  will demonstrate the behaviour.
- How you'll roll back if a partway commit goes south.

### 3. Implement

Keep the diff narrow. Specific constraints:

- **Do not refactor opportunistically.** If a refactor is genuinely
  necessary, surface it to the planner role (see TEAM.md) and stop.
- **No comments restating the code.** Sedge's existing comments
  document *why*, not *what*. Match that style.
- **No emoji** in code or commits.
- **Use the dedicated tools.** `Edit` for code changes, not `sed`.
  `Write` only for new files.
- **Respect package boundaries.** `tui/` knows about `tmux/`, `tmux/`
  knows about `exec.Command`; never the reverse. `hookstate/` is
  read-only from `tui/`.
- **Atomic file writes** for anything sedge persists (temp + rename).
- **Hidden subcommands** stay hidden (`Hidden: true` on the cobra
  command). Don't promote `hook` or `--inside-tmux` to the public CLI.

### 4. Validate (graphical / interactive)

Run the test skill on the changed paths:

```
.claude/skills/sedge-tui-test/bin/run.sh
```

If you added a new behaviour, you **must** have added or extended a case
file under `bin/cases/`. If the run is green but no new case exists, the
work is incomplete — go back to step 3.

If the run is red:

1. Open the failing case's stdout/stderr.
2. Identify whether the regression is in your diff or pre-existing.
3. Fix and re-run.
4. **Do not** disable a case to make red go away. Disabling a case is a
   spec change and requires planner sign-off (TEAM.md).

### 5. Validate (unit)

```
go vet ./...
go test ./...
```

Both must be green.

### 6. Commit

Style mirrors existing sedge commits — terse subject (≤72 chars,
imperative, lower-case start), then a body explaining *why* this is the
right change, the failure mode it eliminates, and any subtlety future
readers will trip over.

Co-authorship trailer matches existing commits:

```
Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
```

One concept per commit. If your diff fans out to "fix X, also clean up
Y, also rename Z", split.

### 7. Open / update PR

Use `gh pr create` with:

- Title: same as commit subject.
- Body sections: `## Summary` (1-3 bullets, why and what changed) and
  `## Test plan` (a checklist of which `bin/cases/` were run, plus any
  manual verification steps).
- Base branch: `main`.

If you already have an open PR for the same logical unit of work,
`git push` to it instead.

### 8. Hand off or exit

Decide:

- If the PR is merge-ready (green CI, no open review threads, no
  follow-ups discovered), exit the loop cleanly with a one-line
  summary: `shipped: <commit subject> (#<pr>)`.
- If the PR is blocked (CI red, review pending, dep on another agent
  role), exit the loop with `blocked: <reason>` and the PR URL.
- Never `--no-verify`, `--force`, or `rebase -i`. Hooks failing is a
  fix-the-hook event, not a skip event.

### 9. Schedule the next iteration

If invoked by `/loop`, do nothing — the harness will fire you again.

If invoked manually, do not auto-schedule. The user will re-trigger.

## Decision rules

### "Should I do this in one PR or split it?"

Split if:

- The diff touches more than one package and the pieces are independently
  reviewable.
- A reasonable reviewer would ask "why is this in here?" about any line.
- One piece is a refactor and another is a feature.

Bundle if:

- The pieces are co-located within one package and would be a single
  conceptual commit anyway.
- Splitting would force a temporary backwards-compat shim.

### "The user wants X but spec says Y"

You read the spec, not the user's mind. If user input on this loop
iteration contradicts SPEC.md, surface the contradiction (open a PR that
updates SPEC.md *first*, with a one-line note in the body) before
implementing.

### "Tests are flaky"

Stop. Flakes are a sedge-tui-test bug, not a "retry" event. Open an
issue (`gh issue create`) describing the flake reproducer and exit the
loop with `blocked: flake in <case>`.

### "I changed tmux behaviour"

Then you must touch `internal/tmux/layout.go` and at least one
`bin/cases/` file. There is no exception. The pane geometry is the
product surface; pretending otherwise is how `8c4346d Revert "Make each
worktree its own tmux window"` happened.

### "I changed the hook flow"

Then you must touch `internal/hookstate/`, `cmd/sedge/hook.go`, **and**
`bin/cases/09-activity-indicator.sh`. Hook payloads are the public
contract between sedge and claude; breaking it silently is a regression
even if the build is green.

## Hard rules

1. **No `--force` push, no amending merged commits.** Period.
2. **No skipping hooks (`--no-verify`, `--no-gpg-sign`).** Hooks are
   user-configured invariants; bypassing them is an outside-spec action.
3. **No silent test disables.** Disabling a case requires updating SPEC.md
   §5 in the same PR explaining why the invariant no longer applies.
4. **No new top-level files** without a clear home (`SPEC.md`,
   `README.md`, `AGENT.md`, `TEAM.md`, `LICENSE`, `NOTICE`, the harness
   under `.claude/skills/` are the allowed set). New top-level files
   require a one-line addition to TEAM.md explaining who owns them.
5. **No claude-code installation or settings mutation on the user's
   machine** from a test case. The harness must run inside its own
   `SEDGE_HOME` and `CLAUDE_CONFIG_DIR`.
6. **No real `claude` invocation in tests.** Stubs only.
7. **No long-lived background processes** outside the harness's tmux
   server. If your case starts a process, the case kills it on teardown.

## What "done" looks like

A loop iteration is done when:

- Exactly one PR exists for the iteration's chosen item.
- The PR is green: `go vet`, `go test`, and the relevant `bin/cases/`.
- The PR description names the spec section it lands.
- The agent has written `shipped:` or `blocked:` on the way out.

A *campaign* (a multi-iteration arc — e.g. "implement orchestration end
to end") is done when:

- Every sub-item is shipped.
- SPEC.md §6 has been moved-to-§4 for the relevant text.
- A README section exists for any user-visible surface.

## Anti-patterns observed in prior commits

(These are the failure modes you exist to avoid.)

- **`87a389f` → `8c4346d` revert.** "Each worktree its own window" was
  shipped without exercising the slot-swap flow under load. Don't ship
  a layout change without running cases 04 and 06.
- **`665e790`.** A `join-pane -t` was passed a window id and worked "most
  of the time". Use pane ids only; case 11 catches this.
- **`4f605a8`.** Activity bookmark was bumped *after* the swap, causing
  a spurious yellow flash. Case 09 must capture indicator state before
  and after a swap and assert no transient flash.
- **`0cdac61`.** `gh` returning a friendly error masked a real bug. Stub
  must distinguish "no PR exists" (`pr list → []`) from "gh unavailable"
  (`gh: command not found`). Case for both.

## Self-improvement clause

If, during a loop iteration, you discover this file is wrong (stale, too
narrow, blocks a clearly-correct change), fix it in the **same PR** as
the change. Don't accumulate process debt across iterations.
