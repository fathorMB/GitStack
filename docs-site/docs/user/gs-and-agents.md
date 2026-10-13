---
id: gs-and-agents
title: gs and skills for agents
---

`gs` is the command-line tool of GitStack, designed after `gh` and meant for
people and for coding agents. Its help texts are currently in Italian; the
[CLI reference](../cli/index.md) lists every command.

## Install

Every instance serves the tool, with the checksums, at `/downloads` (the web
UI has a **Downloads** page too). The scripts verify the SHA-256 before they
install anything:

```sh
curl -fsSL https://HOST/install-gs.sh | sh        # Linux, macOS
irm https://HOST/install-gs.ps1 | iex             # Windows
```

With an internal CA, give the script the CA fingerprint the administrator
published (`GS_CA_SHA256`, or `-CaSha256` on Windows), so that it trusts the
CA only after checking it.

## Sign in

```sh
gs auth login --hostname HOST     # asks for a personal token
gs auth status                    # instances, user, scopes, expiry
gs auth setup-git                 # let git use gs as credential helper
```

Agents and CI do not log in: set `GS_HOST` and `GS_TOKEN` and they take
precedence over everything. Inside a clone, `gs` finds the repository from the
`origin` remote; otherwise use `-R OWNER/REPO` and `--hostname`.

## Command groups

| Group | For |
|---|---|
| `gs repo` | create, list, view, clone, edit, archive, delete, restore |
| `gs issue` | create, list, view, edit, comment, close, reopen, lock |
| `gs label`, `gs milestone` | manage labels and milestones |
| `gs notification` | list, view, mark as read |
| `gs search` | search issues across the instance, or code in a repo |
| `gs browse`, `gs blame` | open in the browser, who changed each line |
| `gs api` | call any API operation directly |
| `gs skills` | install the skills for coding agents |

Most commands accept `--json` for scripts and agents.

## Skills for coding agents

Skills teach an agent the usual GitStack workflows. They are in the open
Agent Skills format and are served by the instance (`/downloads/gs-skills.zip`):

| Skill | When |
|---|---|
| `gitstack-setup` | install `gs`, sign in, choose the instance |
| `gitstack-issue-to-commit` | work on an issue up to the commit with `fixes #n` |
| `gitstack-issue-triage` | search, classify, assign and close issues |
| `gitstack-repo-admin` | create, edit, archive, delete and recover repositories |
| `gitstack-notifications` | notifications, then work, then reply |

```sh
gs skills install                  # agents found, in the project
gs skills install --user           # in your home
gs skills install --agent codex    # claude or codex
gs skills install --agents-md      # also a section in AGENTS.md
gs skills update                   # align with the instance
```

Claude Code gets `.claude/skills/` and Codex `.agents/skills/`. The zip is
verified before writing; a folder that `gs` did not install is not touched
without `--force`. The skills tell agents never to pass `--yes` on a delete or
an archive without an explicit instruction from a person.
