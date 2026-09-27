# LMBrain Lite Project Brain

This directory is the portable, versioned state of a project run by one lead coding agent. Markdown files are the source of truth; the LMBrain Lite app is a read-oriented view over them with two operator actions: approve a milestone and reorder priorities.

**Kit version:** read from `VERSION`.

## Layout

```
.lmbrain-lite/
  AGENT.md            operating contract for the lead agent
  OPERATOR.md         human guide
  PROJECT.md          what the project is
  ROADMAP.md          generated milestone index (do not edit)
  milestones/M-NN.md  one file per milestone; tasks are a checklist
  LOG.md              append-only action log (created on first write)
  NOTE.md             one short agent note for the Pulse page (optional)
  knowledge/          durable project knowledge, wikilinked
  skills/             Markdown runbooks (read, never executed)
  design/             operator-loaded HTML mockups
  reports/            kit feedback report
  templates/          milestone, skill, and bootstrap prompt templates
  mcp/                how the lmbrain-lite-mcp server is registered
  HARNESSES.json      optional harness intent manifest
```

## Quick start

1. Open the repository in LMBrain Lite and initialize the kit.
2. Start the lead agent and give it `templates/lead-bootstrap-prompt.md`.
3. Approve its first milestone on the Roadmap page.

Read `OPERATOR.md` for the human workflow and `AGENT.md` for the agent rules.
