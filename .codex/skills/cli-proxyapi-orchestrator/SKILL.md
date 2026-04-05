---
name: cli-proxyapi-orchestrator
description: Use when Codex is the primary agent in CLIProxyAPI-ori and should keep backend ownership while delegating bounded UI work in the paired management-center frontend repo to Claude Code, then continue integration using structured handoff artifacts.
---

# CLIProxyAPI Orchestrator

Use this skill when working in `CLIProxyAPI-ori` and the task spans backend ownership plus a frontend slice in `/Users/taylor/code/tools/Cli-Proxy-API-Management-Center-ori`.

## Ownership split

- Codex owns backend analysis, API contracts, data-flow, regression review, and final integration.
- Claude owns bounded frontend implementation in the management-center repo.
- Codex remains the orchestrator. Claude is never the top-level planner.

## When to delegate

Delegate to Claude when the work is mainly:

- page or component implementation
- UI polish and interaction feel
- UX copy and layout hierarchy
- visual states for loading, empty, error, and success

Keep work in Codex when the task is mainly:

- backend handlers or proxy logic
- model compatibility and protocol safety
- persistence, schema, or migration changes
- state consistency across backend and frontend
- final validation and release readiness

## Paired repo assumption

The frontend repo is:

`/Users/taylor/code/tools/Cli-Proxy-API-Management-Center-ori`

Claude frontend tasks should usually run with:

- `cwd=/Users/taylor/code/tools/Cli-Proxy-API-Management-Center-ori`
- `context_files` including:
  - `CLAUDE.md`
  - `.codex/skills/cli-proxy-management-frontend-specialist/SKILL.md`

## Required workflow

1. Inspect the backend and identify the smallest frontend slice that can be delegated safely.
2. Write a tight task contract:
   - goal
   - frontend `cwd`
   - read files
   - write scope
   - visual direction
   - constraints
   - acceptance criteria
3. Start the local Claude delegation bridge from `scripts/claude_delegate_mcp.py`.
4. Poll `get_task_status` or `tail_task_events`.
5. Read `get_task_result`.
6. Review Claude's handoff, then continue backend and integration work in Codex.

## Shared context rule

There is no hidden shared memory between Codex and Claude. Treat these artifacts as the shared context:

- `task.json`
- `prompt.md`
- `status.json`
- `events.jsonl`
- `handoff.md`
- `changed_files.json`

## Artifact location

Artifacts are written under the delegated repo:

`<frontend-cwd>/.codex/claude-runs/<run-id>/`

## Setup

Read only what you need:

- setup and MCP registration: `references/setup.md`
- task contract examples: `references/contracts.md`

