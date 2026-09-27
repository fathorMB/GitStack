# Sub-agent brief

What a `lite_subagent_request` must carry. The sub-agent sees this brief and nothing else: not your chat, not your context, not the operator's intent. Write it for a capable stranger.

## Fields

- **task**: `M-NN/T-NN` (or `M-NN`) the job serves. It must exist on the roadmap.
- **title**: one line. The session is labelled `sub:<name>` from it.
- **tier**: the kind of work, not a model.
  - `implementation`: a feature slice with a narrow surface and few dependencies on what is in flight.
  - `mechanical`: rename, migrate, reformat, port, apply a known pattern across files.
  - `research`: find out and report; no code changes expected.
- **files**: the workspace-relative paths the sub-agent may touch. Leave empty only when the brief itself bounds the scope.
- **done_when**: the exit criterion it can check on its own: a command that must pass, a test to add and make green, an observable result.

## Rework

For rework, pass `rework_of: SA-NN` and put only the review feedback in `brief`: what is wrong, what must change, what stays. Everything else is inherited from the original request. Check `route` and `context_reused` in `lite_subagent_list` afterwards; a fresh session (`context_reused: false`) was told the original brief and the log, nothing more.

## The brief

Cover, in this order:

1. **What to build or find out.** The outcome, in one paragraph.
2. **What is already decided.** Contracts, names, shapes, and the reasons, so it does not re-decide them.
3. **What not to touch.** Files, behaviours, public surfaces.
4. **How to work.** Read and change project state only through the `lite_*` tools, never by editing `.lmbrain-lite/` files; in a git worktree that directory is a stale copy and the tools still write to the main tree. Log with `lite_log` and `by: sub:<name>` when done and on any decision the lead must know about. Never set tasks or milestone statuses. Never request sub-agents.
5. **How to report.** One message: what changed, what was verified, what was left open.

## Example

```
task: M-02/T-03
title: Refresh-token rotation
tier: implementation
files: [src/auth/refresh.ts, src/auth/refresh.test.ts]
done_when: `pnpm test` passes and refresh.test.ts covers a reused token being rejected.
brief: |
  Rotate the refresh token on every use. The session cookie keeps its current
  shape (see knowledge/auth.md); only the token store changes. Decided: tokens
  are opaque 32-byte values, stored hashed, one active token per session.
  Do not touch the login handler or the cookie helpers.
  ...
```
