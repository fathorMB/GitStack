# Operator Guide

This is the human entry point for LMBrain Lite in a project repository.

LMBrain Lite is for projects run by **one lead agent** that may hand bounded work to cheaper sub-agents, always through you. You talk to the lead in chat. The app shows you where the project stands. You keep two controls: **you approve milestones**, and **you dispatch or decline sub-agents**.

## First use in a new repository

1. Open the repository in LMBrain Lite and select **Initialize kit**. This copies `.lmbrain-lite/` into the repository and registers the `lmbrain-lite-mcp` tools in the workspace `.mcp.json`, which every host the app launches reads.
2. Start the lead agent from **Sessions** (or in your own terminal).
3. Give it `templates/lead-bootstrap-prompt.md`.
4. Read its report. It will have personalized `PROJECT.md` and proposed the first milestone.
5. Open **Roadmap** and approve the milestone. The agent can now work.

## I need a feature, a fix, or a change

1. Tell the agent in plain language.
2. It creates a milestone with tasks (status `proposed`) and tells you the id.
3. Approve it on the **Roadmap** page. Reorder priorities there if you want.
4. The agent works. Watch **Pulse**: progress bar, blocked tasks, latest log lines, and the agent note.

To change a task, a priority, or scope, tell the agent. The app does not edit tasks: one hand on the files avoids conflicts.

## The lead asks for a sub-agent

1. The lead writes a request (task, brief, files, exit criterion, and a *tier*: implementation, mechanical, or research). It appears in **Sessions** as a card; nothing starts on its own.
2. The card shows the host and the model the dispatch will actually use before you confirm it: on Claude Code the tier resolves to Sonnet for implementation and Haiku for the rest, never Opus, and you may change it. Send it to Diorama instead and the tier becomes informational — the engine is the one your Diorama configuration points at, and the app neither chooses nor names it. Then **Dispatch**: a session labelled `sub:<name>` opens with the brief already in it. Or **Decline** with a reason; the lead reads it back.
3. A Diorama sub-agent always runs in a dedicated worktree. That is not a preference: the rule that stops a sub-agent from asking for its own sub-agents lives in that worktree's `.mcp.json`, and the main tree's file belongs to the lead. The card forces the worktree, and refuses a rework whose original ran in the main tree.
4. At most two sub-agent sessions run at once (Settings → General). A dispatch above the cap waits and says so; it opens by itself when a slot frees.
5. When the sub-agent reports, the lead reviews the result. Sub-agents never change milestones, tasks, or the note.

## Reading the state

- **Pulse**: active milestone, progress, blocked tasks, the last log lines, the agent note, diagnostics.
- **Roadmap**: every milestone with its tasks. Approve and reorder here.
- **Wiki**: `knowledge/`, milestone files, skills, and root documents.
- **Sessions**: interactive agent terminals — Claude Code, natively or through a local Ollama model, and Diorama against your local engine — plus the lead's sub-agent requests.

Everything is Markdown under `.lmbrain-lite/`. Commit it with the repository so the state travels with the code.

## Agent hosts

The app launches two hosts. It never installs either, and it starts nothing on its own.

- **Claude Code**, natively or through a local Ollama model. **Settings → Harnesses** probes it and can run its own updater, `claude update`, once you confirm.
- **Diorama**, a headless harness for a local OpenAI-compatible engine. Native only: the engine and the model are Diorama's own configuration, so there is nothing for the app to choose. Diorama usually lives in a virtual environment and is often not on `PATH`, so **Settings → Harnesses** carries one setting for the machine, *Diorama path on this machine*; leave it empty to resolve `diorama` on `PATH`. The card says which of the two resolved the executable, so an older copy on `PATH` shadowing your virtual environment is visible rather than puzzling. The app never updates Diorama: its wheel belongs to `pip`, and the row says so instead of offering a button that cannot work.

Before a Diorama session opens, the app runs `diorama doctor` in the workspace and refuses the start with what doctor said. An unreachable engine, an unset model, or an `.mcp.json` Diorama could not read are yours to fix — you read the reason in the dialog rather than finding it inside a session that already opened.

## Safety model

- The app never starts an agent on its own. You start every session, and you dispatch every sub-agent (a confirmed dispatch may wait for a free slot, but you confirmed it).
- The app never edits milestone files except for the approve and reorder actions.
- `ROADMAP.md` is generated. Do not edit it by hand; the next tool call overwrites it.
- Agent hosts handle their own authentication and billing. LMBrain Lite stores no credentials in the kit.
