# TEAM.md — long-lived agent team for the sedge codebase

A blueprint for a multi-agent team that builds, tests, and ships sedge on
a sustained loop. The team is *cooperative*, not hierarchical — each role
has a clear surface and a clear hand-off protocol.

The team is designed to be instantiated inside sedge itself: each role
runs as a `sedge` worktree, coordinated via the orchestration mechanism
defined in SPEC.md §4.6 (planner → plan.json → worker spawn → file-based
signalling). That gives the team an honest dog-fooding loop: when the
orchestration flow is broken, the team can't operate, and fixing it is
the team's top priority.

## Composition

Six standing roles. Each runs as its own sedge worktree, with a permanent
system prompt seeded from this file. No role is permitted to act outside
its surface; cross-role work goes through hand-off (see §3).

| role           | worktree slug          | reads          | writes                                    | invokes               |
| -------------- | ---------------------- | -------------- | ----------------------------------------- | --------------------- |
| **Architect**  | `team/architect`       | everything     | `SPEC.md`, `TEAM.md`, `AGENT.md`          | nobody                |
| **Planner**    | `team/planner`         | spec + repo    | `team/plans/*.json` (sedge plan format)   | nobody                |
| **Implementer**| `team/impl-<n>` (1..N) | spec + plan    | code under `cmd/`, `internal/`            | sedge-tui-test skill  |
| **Tester**     | `team/tester`          | code + spec    | `.claude/skills/sedge-tui-test/`          | nothing               |
| **Reviewer**   | `team/reviewer`        | open PRs       | PR review comments (gh)                   | nothing               |
| **Shipper**    | `team/shipper`         | merged PRs     | `CHANGELOG.md`, tagged releases           | `gh release create`   |

`N` (implementer count) tracks SPEC.md `max_parallel_subagents`; the default
of 3 is a good ceiling for a laptop.

## 1. Roles in detail

### 1.1 Architect

**Purpose.** Owns SPEC.md, TEAM.md, AGENT.md. The team's source of truth.

**Inputs.**
- Conversations with the user (the only role that talks freely to the user).
- Failure patterns observed in merged commits (revert ratios, regression
  clusters).
- Open questions in SPEC.md §6.

**Outputs.**
- Spec edits (PRs touching only `*.md`).
- New invariants in SPEC.md §5 (always paired with a Tester task to make
  the harness assert it).
- Adjustments to TEAM.md when role boundaries leak.

**Hard rules.**
- Never edits Go code. If a code change is necessary to *demonstrate* a
  spec change, file a plan and let the Planner route it.
- Spec edits must include rationale in the PR body — "because the user
  asked for it" is *not* rationale; the rationale is "X invariant
  prevents Y class of bug we hit in commit Z".

### 1.2 Planner

**Purpose.** Decomposes spec-level intent into concrete, dep-graphed
implementation plans the Implementer pool can execute in parallel.

**Inputs.**
- SPEC.md §4 (features) and §6 (open questions).
- AGENT.md's "Loop body" — every plan must produce loop-runnable tasks.
- Recent commits (so plans don't propose already-shipped work).

**Outputs.**
- One `plan.json` per epic, conforming to the orchestration schema in
  SPEC.md §4.6:
  ```json
  {
    "name": "implement-soft-orchestration-cleanup",
    "summary": "...",
    "sessions": [
      {"id": "cleanup",  "task": "...", "depends_on": []},
      {"id": "tests",    "task": "...", "depends_on": ["cleanup"]}
    ]
  }
  ```
- Stored at `team/plans/<epic>.json`. The Planner does **not** spawn the
  workers — the user (or the team coordinator) presses `Y` in sedge to
  spawn them.

**Hard rules.**
- Every session's `task` ends with a literal "Acceptance: <how the
  Tester role will know this is done>" clause. Without it the plan is
  invalid.
- A plan with >5 sessions or >2 dependency layers is too big — split it.
- No plan modifies more than one package per session unless explicitly
  justified.

### 1.3 Implementer (pool of N)

**Purpose.** Take one session from a plan, follow AGENT.md's loop body,
ship a PR.

**Inputs.**
- AGENT.md (the standing brief).
- One specific `Session` from a plan.
- The current state of `main`.

**Outputs.**
- One PR per session, base `main`, title matches the commit subject.
- A `done/<id>` marker file in the worktree's
  `.sedge/orchestration/done/` directory when the PR is merged-and-clean,
  signalling dependent sessions.

**Hard rules.**
- An Implementer that produces two consecutive red PRs on the same
  branch is paused for one full loop cycle to prevent churn. Surface
  via `gh pr comment`.
- An Implementer never reviews its own PR.
- An Implementer never edits SPEC.md / TEAM.md / AGENT.md. Drift?
  Surface to Architect with a Planner task.

### 1.4 Tester

**Purpose.** Owns `.claude/skills/sedge-tui-test/`. Every invariant in
SPEC.md §5 must have a case file; every behaviour in SPEC.md §4 must be
exercised by at least one case.

**Inputs.**
- SPEC.md §4 (features) and §5 (invariants).
- The current case coverage (what `bin/run.sh` exercises).
- Implementer PRs in flight (so new cases land *before* the
  implementation, when possible — TDD by default).

**Outputs.**
- New case files under `bin/cases/`.
- Harness extensions (`tmuxq` helpers, stub upgrades, fixtures).
- A `coverage.md` adjacent to `SKILL.md` mapping each spec invariant to
  the case(s) covering it. Re-generated on every PR.

**Hard rules.**
- Never disables a case to make a build green. Disabling a case requires
  Architect sign-off via a SPEC.md edit.
- Stubs (`bin/stubs/claude`, `bin/stubs/gh`) are the Tester's domain.
  Implementer changes that need a new stub behaviour file a Planner task
  for a Tester session first.

### 1.5 Reviewer

**Purpose.** Independent code review on every Implementer PR.

**Inputs.**
- The PR diff.
- SPEC.md (does this change match what the spec says?).
- AGENT.md "Hard rules" (any rule violations?).
- Recent revert history (is this a known-bad pattern?).

**Outputs.**
- `gh pr review --approve` / `--request-changes` / `--comment`.
- Inline comments tagged with one of `nit:`, `question:`, `blocking:`.
- An `## Reviewer summary` block on every review listing the spec
  invariant(s) the PR touches and whether the test case proves it.

**Hard rules.**
- The Reviewer never pushes commits to the PR branch. Suggestions are
  inline; if a fix is needed, the PR is `--request-changes` and bounced
  back to the Implementer.
- The Reviewer must read the SPEC.md sections referenced in the PR
  description. Approving without reading the spec section is a
  reviewer-level failure.

### 1.6 Shipper

**Purpose.** Tag, changelog, release.

**Inputs.**
- Merged PRs since last tag.
- SPEC.md (does the merge set complete any §4 epic?).
- README.md (does it advertise anything not yet released?).

**Outputs.**
- `CHANGELOG.md` entries grouped by `Added` / `Changed` / `Fixed` /
  `Removed`.
- Git tags (`vX.Y.Z`) on `main`.
- `gh release create` with the changelog excerpt as the body.

**Hard rules.**
- Never tags a release that has open `team/plans/*.json` referenced by
  unresolved Architect questions. Half-implemented features don't ship.
- Versioning is semver. Hook-payload changes are *breaking* (major bump).

## 2. Communication channels

| channel                                           | participants            | purpose                                       |
| ------------------------------------------------- | ----------------------- | --------------------------------------------- |
| `team/plans/*.json`                               | Planner → Implementers  | work assignment (DAG)                         |
| `.sedge/orchestration/done/<id>`                  | Implementer → siblings  | "I'm done, deps may proceed"                  |
| `gh pr` (review comments)                         | Reviewer ↔ Implementer  | per-PR feedback                               |
| `gh issue` (label: `architect-review`)            | anyone → Architect      | spec drift, role boundary leak, contradiction |
| `CHANGELOG.md`                                    | Shipper                 | what shipped, when                            |
| `.claude/skills/sedge-tui-test/coverage.md`       | Tester                  | spec → case mapping                           |
| `team/blocked.md` (single file, append-only)      | anyone                  | "I exited the loop with `blocked: …` because" |

No DMs, no out-of-band Slack. Every state transition is observable from
the repo or `gh`.

## 3. Hand-off protocol

A role hands off to another by:

1. Writing the appropriate artifact (plan, PR, review, issue, marker).
2. Tagging the receiving role in the artifact:
   - PR body: `Tester: case <NN> needed`.
   - Issue body: `architect-review: SPEC.md drift detected at §X`.
3. Exiting its own loop with `blocked: <reason>` or `shipped: <ref>`.

The receiving role picks up the artifact on its next loop iteration.

There is no synchronous wait. If a role's queue is empty, it idles.

## 4. Cadence & schedule

Each role runs on `/loop` with its own cadence:

| role        | cadence                   | reasoning                                              |
| ----------- | ------------------------- | ------------------------------------------------------ |
| Architect   | on-demand (user-triggered)| spec changes are deliberate, not periodic              |
| Planner     | 1× per business day       | enough to keep the pool fed, not so often it churns   |
| Implementer | every 20–30 minutes       | matches a typical "small PR" turnaround                |
| Tester      | every 20–30 minutes       | TDD pairing with Implementer means matching cadence    |
| Reviewer    | every 10 minutes          | fast review = short PR-pending-time = momentum         |
| Shipper     | 1× per business day       | release cuts are events, not background activity       |

A loop iteration that does nothing (`shipped: noop` because queue is
empty) is normal and expected — do not pad iterations with make-work.

## 5. Conflict resolution

Two implementers race on overlapping code:

1. Whichever PR merges first wins.
2. The loser rebases and retries once. If retry still conflicts, the
   Implementer surfaces an `architect-review` issue with a one-line
   description and exits. The Architect (or Planner) splits the work
   differently next iteration.

Tester and Implementer disagree on whether a case is correctly written:

1. The case is the ground truth (test-first is the team default).
2. Implementer's recourse is an `architect-review` issue arguing the
   spec invariant is wrong — *not* a PR muting the case.

Reviewer and Implementer disagree on PR shape:

1. `blocking:` comments must cite a SPEC.md §, AGENT.md rule, or
   commit-history anti-pattern. Reviewer opinions without citation are
   advisory.
2. Implementer either fixes or escalates to Architect via issue. Never
   merges over a `blocking:` without sign-off.

## 6. Bootstrapping the team

To stand the team up from scratch:

```bash
# 1. Register sedge itself as a project.
sedge add ~/code/sedge

# 2. For each role, create a worktree.
sedge        # open the TUI, navigate to sedge, "+ new session"
             #   session: team-architect / team-planner / team-impl-1 / ...
             #   branch:  team/<slug>
```

In each worktree's claude, paste the role section (1.1–1.6) as the
permanent system prompt. The Implementer worktrees additionally read
`AGENT.md` as their standing brief.

The first Planner iteration produces `team/plans/000-spec-coverage.json`
with one Tester session per uncovered invariant. The team is then
self-sustaining.

## 7. Health metrics (Shipper-emitted)

Each release tag includes a `### Team health` block in the release notes:

- Mean PR open → merged hours (target: <12 h).
- Revert rate (commits that revert another) over last 50 commits
  (target: <5%).
- Cases added vs cases removed over the cycle (target: monotonically
  increasing).
- Loop iterations exited as `blocked:` vs `shipped:` (target: blocked
  ratio <20%).
- Open `architect-review` issues at tag time (target: ≤3).

Numbers worse than target trigger an Architect-led retro in the
following cycle — a PR to TEAM.md adjusting role boundaries or cadence.

## 8. When the team should pause

Pause the loop (all roles exit cleanly without re-scheduling) when:

- `main` is red and the fix has been pending >2 iterations.
- The user has explicitly said "I'm restructuring, hold off."
- A SPEC.md change is in flight that materially redefines a role.

Resuming is a manual `/loop` invocation per role.

## 9. Failure of the team itself

If the team produces:

- Zero PRs over 24 h with non-empty plans → Architect inspects role
  cadences and hand-off latency.
- A revert within 24 h of a merge → Architect surfaces it as a
  retro-worthy event; Tester must add a case covering the regression
  *before* the revert is undone.
- A loop blocked on `gh` rate-limits or auth → Shipper pauses, the
  Architect routes a fix.

The team's correctness is itself a SPEC.md §5-class invariant. If you
catch the team off-spec, fix the team before fixing anything else.
