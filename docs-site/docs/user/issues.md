---
id: issues
title: Issues
---

Every repository has issues, labels and milestones. Issues are numbered
`#n` per repository and are never deleted.

## Work with issues

- Anyone who can see the repository (`read`) opens issues and comments. People
  with `write` assign, label, set the milestone, and close or reopen other
  people's issues. The author can always close and reopen their own.
- An issue is **open** or **closed**, and a closed one has a reason:
  *completed*, *not planned* or *duplicate of #n*.
- Up to 10 assignees, people or agents with `write`. You can assign
  yourself.
- Mention people with `@user`, `@agent` or `@org/team`: only those who can see
  the repository are notified.
- Attach images, PDFs, text, logs or ZIP files (up to 10 MB by default). They
  are visible only to who can read the repository.
- You can edit your own text (an "edited" mark is kept), and repository admins
  can lock a discussion or hide an issue.

## Close from a commit

A commit message containing `fixes #12` (also `close`, `closes`, `closed`,
`fix`, `fixed`, `resolve`, `resolves`, `resolved`, in any case) links the
commit to the issue; when the commit enters the default branch the issue is
closed as *completed*, with a "closed by commit" note. On other branches only
the link is created. Use `owner/repo#12` for an issue of another repository
(it closes only if you can write there).

## Labels and milestones

New repositories get a default set of labels: `bug`, `enhancement`,
`documentation`, `question`, `duplicate`, `good first issue`, `agent-ready`
and `needs-human`. They are editable per repository. A milestone has a title,
a description and an optional date; an issue belongs to at most one.

## Templates

Put Markdown files in `.gitstack/ISSUE_TEMPLATE/` on the default branch. The
web UI and `gs` offer them when someone opens an issue. An optional YAML front
matter gives `title`, `about` and `labels`. See
[issue templates](https://github.com/fathorMB/GitStack/blob/main/docs/issue-templates.md).

## Search

The search uses GitHub-like qualifiers, in the web UI, in the API and in
`gs`:

```
is:open label:bug assignee:@me milestone:v1 no:assignee login crash
```

Available: `is:`, `reason:`, `label:`, `assignee:` (`@me`, `@agents`),
`author:`, `milestone:`, `no:`, `repo:`, `org:` and free text. Results are
limited to repositories you can see.

## From the terminal

```sh
gs issue list -R OWNER/REPO
gs issue create -R OWNER/REPO
gs issue view 12 -R OWNER/REPO
gs issue comment 12 -R OWNER/REPO
gs issue close 12 -R OWNER/REPO
gs label list -R OWNER/REPO
gs milestone list -R OWNER/REPO
gs search issues "is:open label:bug"
```

Add `--help` to any command; every command is in the
[CLI reference](../cli/index.md).

## Notifications

You follow an issue automatically when you author it, are assigned, comment
or are mentioned; you can also subscribe or unsubscribe, and set how much of a
repository you watch. Notifications appear in the web UI and, if the
administrator configured SMTP, by e-mail according to your settings
(**Settings → Notifications**). In the terminal use `gs notification list`,
`view` and `read`.
