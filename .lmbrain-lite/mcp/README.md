# MCP registration

The repository-scoped `lmbrain-lite-mcp` server is registered **automatically** when the LMBrain Lite app opens a workspace.

- **Claude Code** and **Diorama**: `.mcp.json` at the workspace root gets a `lmbrain-lite` server entry pointing at the binary with `--root <workspace>`. Both hosts read the same file, in the same shape, so it is written once.
- **Antigravity**: the same entry is merged into its user-global `mcp_config.json` when an installation is detectable.

A session running in a git worktree gets its own `.mcp.json` there, with `--root` still pinned to the main tree. For a sub-agent that entry also declares `LMBRAIN_SUBAGENT`, which is how the one-level dispatch rule reaches a host that hands its MCP servers only what the entry says. The main tree's file never carries it.

The command resolves via `LMBRAIN_MCP_BIN`, then a binary next to the app executable, then `PATH`.

## Tools

| Tool | Does |
| --- | --- |
| `lite_digest` | Read first. Project summary: milestones, active milestone, open and blocked tasks, latest log, note, diagnostics. |
| `lite_roadmap` | Every milestone with its full task list. |
| `lite_log_read` | Newest log lines. |
| `lite_milestone_create` | New `proposed` milestone with tasks. |
| `lite_milestone_status` | Move a milestone. `approved` is operator-only. |
| `lite_milestones_reorder` | Set priorities. |
| `lite_task_add` | Add a task to a milestone. |
| `lite_task_set` | Set a task open, done, or blocked (with reason). |
| `lite_log` | Append one log line. |
| `lite_note_set` | Replace the Pulse note. |
| `lite_subagent_request` | Ask the operator to dispatch a sub-agent for one bounded job (task, brief, files, done-when, tier). The app picks the model from the tier. With `rework_of`, sends rework back to the sub-agent that did the work. |
| `lite_subagent_list` | Every sub-agent request with its state, the session, host and model that served it, the reason when declined, whether it awaits review, and for a rework the route taken and `context_reused`. |
| `dream_capture` | Capture a dream during an invited session. |
| `lmbrain_feedback_record` / `_report` / `_resolve` | Kit feedback report. |
| `harness_*` | Harness manifest intent, approval, plan, apply, drift. |

Every writing tool accepts `by`: `lead` (default), `sub:<name>`, or `operator`.

Example `.mcp.json` entry:

```json
{
  "mcpServers": {
    "lmbrain-lite": {
      "command": "lmbrain-lite-mcp",
      "args": ["--root", "."]
    }
  }
}
```
