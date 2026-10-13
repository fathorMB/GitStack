---
id: pull-requests
title: Pull requests
---

:::note
Pull requests are **not part of v1.0**. They are planned for v1.1. This page
says how to collaborate today.
:::

## What to do in v1

- Work on a branch and push it. Anyone with `write` can push branches of the
  repository; the main branch may be protected by the repository's rules.
- Review by reading the commit pages in the web UI (history, commit diff,
  blame) or locally with `git`.
- Link the work to an issue with `fixes #n` in the commit message: the issue
  closes when the commit reaches the default branch
  (see [Issues](./issues.md#close-from-a-commit)).
- People with only `read` propose changes by opening an issue.

## Planned for v1.1

Pull requests between two branches of the same repository, with reviews and
line comments, drafts, merge or squash, conflict reporting, required reviews
on the main branch, commit statuses, `gs pr` commands and the matching
webhooks. They will share the numbering of issues.
