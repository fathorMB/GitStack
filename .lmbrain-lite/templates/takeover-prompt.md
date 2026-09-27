# Take over an LMBrain standard project

You are the lead agent. This repository was an LMBrain standard project and has just been taken over as an LMBrain Lite project. The app did the mechanical part: `.lmbrain-lite/` exists from the bundled kit, `knowledge/`, `skills/`, `design/` and `reports/` were carried across, the sidecar is registered, and `AGENTS.md` points at the Lite contract. The semantic part is yours, and it goes through the `lite_*` tools only.

`.lmbrain/` is the standard archive: read-only, never to be updated.

Work in this order:

1. Call `lite_digest`. Expect an empty roadmap: the app created no milestone and no task, on purpose.
2. Read `.lmbrain-lite/AGENT.md`: it is your contract now.
3. Read `.lmbrain/ROADMAP.md`. Create one Lite milestone per standard milestone with `lite_milestone_create`, in the same order, keeping the outcome text. `done` stays done; everything else becomes `proposed` for the operator to approve. Do not invent a milestone that has no `### M-NN` section in the standard roadmap.
4. For each non-discarded spec, read the spec file itself, not just its title, and turn it into tasks on the milestone that referenced it with `lite_task_add`: one task per concrete deliverable, in the spec's own words, with the spec id in the task title. Specs already done become tasks set `done`. Do not invent a task that has no source in a spec.
5. Read `.lmbrain/STATUS.md` and compress it into the single Pulse note with `lite_note_set`: only what the operator must know right now.
6. For every family Lite has no home for (reviews, debts, decisions, agent profiles and proposals, handoffs, MCP specs and proposals, the contract material), either distil what is still true into a page under `knowledge/` or state explicitly that it was left behind. Never copy them into Lite as they are.
7. Log the conversion with `lite_log` as you go.
8. Finish with one report: every milestone and task you created and its source, what you distilled into `knowledge/`, and what you left behind and why.

If a spec is ambiguous, or a milestone references specs that do not exist, say so instead of guessing. Never write under `.lmbrain/`.
