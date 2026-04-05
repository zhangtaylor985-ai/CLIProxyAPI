# Setup

## What this project-local skill contains

- `scripts/claude_delegate_mcp.py`
  - stdio MCP server used by Codex to start and inspect Claude frontend tasks
- `scripts/claude_delegate_runner.py`
  - background runner that invokes Claude Code and stores structured artifacts

## Recommended task split

- Run Codex from `CLIProxyAPI-ori`
- Run Claude tasks with `cwd=/Users/taylor/code/tools/Cli-Proxy-API-Management-Center-ori`
- Let Claude change only the frontend write scope
- Let Codex perform final backend and integration review

## Register as a local MCP server

Example stdio MCP definition:

```json
{
  "mcpServers": {
    "claude-delegate": {
      "command": "python3",
      "args": [
        "/Users/taylor/code/tools/CLIProxyAPI-ori/.codex/skills/cli-proxyapi-orchestrator/scripts/claude_delegate_mcp.py"
      ]
    }
  }
}
```

## Typical frontend task contract

Use `start_frontend_task` with:

- `cwd=/Users/taylor/code/tools/Cli-Proxy-API-Management-Center-ori`
- `context_files` including:
  - `CLAUDE.md`
  - `.codex/skills/cli-proxy-management-frontend-specialist/SKILL.md`
- `read_files` for the current page, shared layout, and style entrypoints
- a narrow `write_scope`

## Artifacts

Artifacts persist under:

`/Users/taylor/code/tools/Cli-Proxy-API-Management-Center-ori/.codex/claude-runs/<run-id>/`

Important files:

- `status.json`
- `events.jsonl`
- `handoff.md`
- `changed_files.json`
- `result.json`

