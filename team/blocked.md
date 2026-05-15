# blocked.md — append-only ledger of role-loop exits with `blocked: <reason>`

Format: one entry per blocked exit. Newest at the bottom. Never edit
prior entries; rewrites are an Architect-only correction (see
`TEAM.md` §2).

Entry template:

```
- <UTC RFC3339>  <role>/<worktree-slug>  blocked: <one-line reason>
  ref: <PR url / issue url / plan path>
```
