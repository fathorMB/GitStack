---
id: mcp
title: MCP for AI assistants
---

GitStack ships a [Model Context Protocol](https://modelcontextprotocol.io)
server so that an assistant (Claude Code, Claude Desktop, Cursor, VS Code,
Codex) can work on repositories, issues and pull requests with tools instead
of shell commands.

The server is **`gs mcp`**, built into the `gs` command line. There is nothing
else to install and nothing new on the server: the assistant starts `gs mcp` on
your computer (over stdio) and it uses the sign-in, token and instances you
already have in `gs` (see [gs and coding agents](./gs-and-agents.md)). A remote
`/mcp` endpoint and OAuth login for MCP clients are not available.

## Set it up

Open **Downloads** on your instance: it shows the ready-to-copy configuration
for each assistant, with your host filled in. For example:

```sh
# Claude Code
claude mcp add gitstack -- gs mcp --host gitstack.example.com
```

```json
{
  "mcpServers": {
    "gitstack": {"command": "gs", "args": ["mcp", "--host", "gitstack.example.com"]}
  }
}
```

`--host` picks the instance when `gs` knows several. For an unattended agent set
`GS_HOST` and `GS_TOKEN` in the environment of the assistant instead.

**Versions.** If your `gs` is older than the instance but on the same major
version, the server starts with the tools it knows and warns you to update. With
a different major version it does not start and tells you to update `gs` from
`/downloads`.

## What it offers

- **Tools**, about thirty to forty, generated from the same OpenAPI contract as
  the public API: repositories and code, issues, pull requests and reviews,
  labels, milestones, search, notifications and commit statuses. Names read
  `object_action`, like the `gs` commands: `issue_create`, `pr_create`,
  `pr_ready`, `review_create`, `pr_merge`, `commit_status_create`.
- **`api_request`**, a generic tool for any other API operation, within the
  permissions of the token.
- **Prompts** for the usual flows, generated from the skills for coding agents:
  `issue_to_pr` (an issue up to a pull request), `issue_triage` and `pr_review`.
  There are no MCP resources.

## Safety

- The server uses your `gs` token and nothing else. Give an agent an account
  with the minimum permissions (the skills recommend it): the token is the limit.
- `--read-only` allows reads only, including through `api_request`; anything
  that writes is refused.
- Tools that close, merge, delete or hide are marked **destructive** in MCP, so
  an assistant can ask you first. The server asks nothing itself.
- There is no dedicated tool to delete or archive repositories, change
  settings, webhooks or tokens, or to administer users. Those are only possible
  through `api_request`, within the token's scopes.

## Example: from an issue to a merged pull request

A typical session, with the agent signed in as its own account:

1. `issue_create` (or an issue already assigned to the agent), then the agent
   pushes a branch such as `agent/12-crash` with `git`.
2. `pr_create` with `fixes #12` in the body and `draft: true`.
3. When the checks are green, `pr_ready`, asking the person who handed over the
   issue to review.
4. **A person** reviews and approves in the web UI or with `gs pr review`. An
   agent's own approval does not count.
5. `pr_merge`. The issue closes as *completed*.

See [Pull requests](./pull-requests.md) for the rules behind each step.
