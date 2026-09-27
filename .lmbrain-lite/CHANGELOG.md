# Kit changelog

The kit version in `VERSION` always matches the LMBrain Lite app that bundles it.

## 0.2.1 — 2026-09-06

- `OPERATOR.md`: an **Agent hosts** section. The app launches Claude Code and Diorama, installs neither, and updates only the one that owns a self-updater; Diorama's path is a machine setting because it usually lives in a virtual environment, and `diorama doctor` refuses a start before a tab opens (#20).
- `OPERATOR.md`: the dispatch card names the host and the model a dispatch will actually use before it is confirmed, and a Diorama sub-agent always runs in a dedicated worktree, because the rule that stops it asking for its own sub-agents lives in that worktree's `.mcp.json` (#20, #17).
- `AGENT.md`: the tier may be informational — the operator can send a request to a host whose model is its own configuration — and `lite_subagent_list` reports the `host` beside the `model`, where `engine` means a local engine the app does not name. A worktree is not optional for a sub-agent on such a host, and the lead still never names a model or a host (#20).
- `mcp/README.md`: both hosts read the one workspace `.mcp.json`, so it is written once; a worktree gets its own with `--root` still on the main tree, and for a sub-agent that entry declares `LMBRAIN_SUBAGENT` (#20).

## 0.2.0 — 2026-09-05

- `UPGRADING.md`: describes the assisted Pulse migration, confirmation and backup, and the agent/manual fallback for locally edited or unverified files.
- `AGENT.md`: the sub-agent section is rewritten around the request flow. The lead asks with `lite_subagent_request`, the operator dispatches or declines in Sessions, the lead reads the outcome back with `lite_subagent_list`. Sub-agents still own no project state and cannot request sub-agents (#17).
- `templates/subagent-brief.md`: what a request must contain; `templates/carve-out-prompt.md` now routes the carved-out slice through a request instead of a chat prompt (#17).
- `AGENT.md`: a project taken over from LMBrain standard keeps `.lmbrain/` as legacy, read-only material; project state lives in `.lmbrain-lite/` and changes only through the `lite_*` tools. `templates/takeover-prompt.md` drives the conversion of the standard roadmap, specs and status through the tools, without inventing work (#23).
- `AGENT.md`: a Worktrees section. Sessions may run in a linked git worktree; project state stays in the main tree through the `lite_*` tools, and the `.lmbrain-lite/` copy inside a worktree is a stale copy never to be read or edited directly. `templates/subagent-brief.md` states the same rule for the sub-agent (#18).
- `AGENT.md`: rework goes back to the sub-agent that did the work, with `rework_of` and only the feedback in the brief; the three routes and what `context_reused: false` means for the lead. A sub-agent that logged its `report` stays open, awaiting review (#22).
- `mcp/README.md`: the two sub-agent tools (#17).
- `OPERATOR.md`, `mcp/README.md`: Claude Code is the only host the app launches. The Codex and Pi hosts and their registration files are gone (#19).

## 0.1.2 — 2026-09-05

- `templates/dreaming-session-prompt.md`: operator invitation to a bounded dreaming session, with the limits `dream_capture` assumes.
- `templates/carve-out-prompt.md`: ask the lead to carve an isolated feature out of the work in progress for a second agent, and to own the review of what comes back.
- `templates/resume-milestone-prompt.md`: the Sessions starter text now lives in the kit instead of the app.
- `AGENT.md`: the agent note supports Markdown; Pulse renders it.
- The app can now migrate a project's kit for you: Pulse offers a migration prompt and, when nothing would be lost, a one-click upgrade. `UPGRADING.md` remains the authority on which files the kit owns.

## 0.1.1 — 2026-09-04

First published kit.

- `AGENT.md`: operating contract for the lead agent (plan, work, log, block, sub-agents).
- `OPERATOR.md`: human guide. One control: approve milestones.
- `milestones/M-NN.md`: source of truth for the roadmap; tasks as a checklist (`[ ]`, `[x]`, `[!]`).
- `ROADMAP.md`: generated index, never edited by hand.
- `LOG.md`, `NOTE.md`: append-only action log and the one-line Pulse note.
- `templates/`: milestone, skill, and lead bootstrap prompt.
- `mcp/README.md`: registration and the `lite_*` tool list.
