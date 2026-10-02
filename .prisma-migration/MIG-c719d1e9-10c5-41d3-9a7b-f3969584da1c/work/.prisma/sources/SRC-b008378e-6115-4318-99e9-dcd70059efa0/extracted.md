# Milestones

One file per milestone, named by its id: `M-01.md`, `M-02.md`, ... These files are the source of truth for the roadmap. `../ROADMAP.md` is generated from them.

Create milestones with the `lite_milestone_create` tool, or copy `../templates/milestone.md`.

## Frontmatter

```yaml
id: M-01
title: Short milestone title
status: proposed        # proposed | approved | active | done | dropped
priority: 1             # 1 = first
created: YYYY-MM-DD
updated: YYYY-MM-DD
```

Only the operator moves `proposed -> approved` (from the app). The agent moves `approved -> active -> done`.

## Tasks

Under the `## Tasks` heading, one line per task:

```
- [ ] T-01 Open task
- [x] T-02 Done task
- [!] T-03 Blocked task — why it is blocked
```

Progress shown in the app is `done / total`. A blocked task counts as open and is highlighted on the Pulse page.
