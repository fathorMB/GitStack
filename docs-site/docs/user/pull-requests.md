---
id: pull-requests
title: Pull requests
---

A pull request (PR) merges one branch of a repository into another branch of
the **same** repository. Pull requests and issues share one numbering: `#12`
is either, never both. Forks are not supported.

## Open a pull request

Opening one needs `write` on the repository. People with only `read` propose a
change by opening an issue.

- In the web UI, use **New pull request** on the repository.
- With `gs`, from a clone that has the branch pushed:

  ```sh
  gs pr create --draft --title "Fix the crash" --body "fixes #12"
  gs pr create --head agent/12-crash --base main --json number,url
  ```

The target defaults to the repository's main branch. A pull request can be a
**draft**: a draft cannot be merged and asks nobody for a review until you
click **Ready for review** (`gs pr ready 7 --reviewer alice`).

A pull request is **open** (draft or ready), **closed** or **merged**. A closed
one can be reopened while its branch still exists; a merged one is final.
Closing a pull request never deletes the branch.

## Close issues from a pull request

The same keywords as in commit messages (`fixes #12`, `closes #12`, …) work in
the pull request description. The issue shows *PR #7 will close this issue*, and
when the pull request is merged into the main branch it closes as **completed**,
with any merge method. There is no manual link: a pull request that only
mentions `#12` does not close it.

## Review

Anyone who can read the repository comments on the pull request. In **Files
changed** you can comment on a line or a group of lines, then send everything as
one review: **Comment**, **Approve** or **Request changes**.

- Conversations on lines can be resolved by the author or by anyone with `write`.
- A `suggestion` block is shown as a diff; it cannot be applied with one click.
- Comments on lines that later commits changed are marked **outdated**.
- Every file has a **Viewed** checkbox. It is personal: it only hides the diff
  for you and reopens it when the file changes.
- The author and anyone with `write` can request a review, or request it again
  after changes. Requested reviewers need at least `read`.

**Who can approve.** People with `write` approve or request changes, but not on
their own pull request. An agent can review and even "approve", but its approval
is shown as such and **does not count**: a person is always needed.

From the command line:

```sh
gs pr review 7 --comment -b "Note on cli/x.go"
gs pr review 7 --approve
gs pr review 7 --request-changes -F review.txt
gs pr comment 7 --body "Thanks, merging."
```

## Merge

Anyone with `write` (agents included) merges when the checks below allow it:

- **Merge commit** or **squash**, chosen at merge time (squash is proposed).
  An admin can switch one of the two off. There is no rebase.
- *Delete branch* after the merge; the repository can also delete it
  automatically (off by default).
- **Conflicts.** The page shows *This branch has conflicts* with the list of
  files and disables the merge. Resolve them locally with the instructions shown
  (`git`, or `gs pr checkout 7`). A branch that is only *behind* the target can
  be merged. There is no conflict editor and no *Update branch*.

```sh
gs pr merge 7 --squash --delete-branch --yes
```

`gs pr merge` and `gs pr close` ask for confirmation; without a terminal they
exit with code 2 unless you pass `--yes`.

### Require a pull request on the main branch

In the repository settings an admin can turn on **Require a pull request** for
the main branch (off by default). When it is on:

- nobody pushes directly to the main branch, **admins included**;
- a merge needs at least **1 approval from a person** (configurable from 1 to 5);
- new commits on the branch dismiss the existing approvals;
- the admin can also mark commit status contexts as **required** (below).

## Commit statuses

External systems report the state of a commit through the API; GitStack shows
it on the pull request, on the commit page, in the API and in `gs`.

```sh
gs api -X POST /repos/OWNER/REPO/commits/SHA/statuses \
  -f state=success -f context=ci/build -f description="build passed" \
  -f targetUrl=https://ci.example.com/run/42
gs api /repos/OWNER/REPO/commits/SHA/status      # combined state
```

- Writing a status needs `write` on the repository.
- The state is `pending`, `success`, `failure` or `error`; the `context` names
  the check (default `default`). A new status for the same context replaces the
  previous one.
- The commit must exist in the repository. `targetUrl` must be `http` or `https`.
- With required contexts configured on the main branch, a pull request cannot be
  merged until every required context is `success`.
- The CI planned for a later version will use this same API.

## Search

The issue search also finds pull requests: `is:pr`, `is:draft`,
`review:required`, `review:approved`, `review:changes-requested`, and
`author:@agents` (next to `assignee:@agents`), in the web UI, in the API and in
`gs pr list`.

```sh
gs pr list --state open --json number,title,reviewDecision
gs search prs "review:required author:@agents"
```

## Notifications and webhooks

Pull requests notify like issues. Requested reviewers follow the pull request.
You are notified when a review is requested or submitted, on comments, when a
draft becomes ready, when the pull request is merged or closed, on conflicts,
and when a required status turns red on your own pull request. Review requests
and mentions are also sent by email by default.

Webhooks have three events: `pull_request`, `pull_request_review` and
`commit_status` (see [Webhooks](./webhooks.md)).

## Working with an agent

A coding agent with `gs` and the skills follows this flow: assign the issue to
itself, work on `agent/<n>-<slug>`, open a **draft** pull request with
`fixes #n`, mark it ready once the statuses are green asking the person who
handed over the issue to review, and label the issue `needs-human` if it is
blocked. The agent can merge after a person approved. See
[gs and coding agents](./gs-and-agents.md) and
[MCP for assistants](./mcp.md).
