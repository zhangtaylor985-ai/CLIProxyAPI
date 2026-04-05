# Contract Example

```json
{
  "cwd": "/Users/taylor/code/tools/Cli-Proxy-API-Management-Center-ori",
  "task": "Refine the session trajectories page so large tables, filters, and empty states feel clearer and more deliberate.",
  "context_summary": "Backend API shape already exists in CLIProxyAPI-ori. Do not change any API contracts in the frontend task.",
  "design_direction": "Operational dashboard, strong information hierarchy, calm neutral palette, sharper empty/loading states, avoid generic card soup.",
  "context_files": [
    "CLAUDE.md",
    ".codex/skills/cli-proxy-management-frontend-specialist/SKILL.md"
  ],
  "read_files": [
    "src/pages/SessionTrajectoriesPage.tsx",
    "src/pages/SessionTrajectoriesPage.module.scss",
    "src/styles/variables.scss",
    "src/styles/layout.scss"
  ],
  "write_scope": [
    "src/pages/SessionTrajectoriesPage.tsx",
    "src/pages/SessionTrajectoriesPage.module.scss"
  ],
  "constraints": [
    "Do not change API request shapes.",
    "Do not touch unrelated routes.",
    "Keep mobile and desktop both usable."
  ],
  "acceptance_criteria": [
    "The page has clearer hierarchy and more intentional states.",
    "The final handoff lists changed files, tests run, and risks.",
    "Codex can continue integration without rereading the whole frontend repo."
  ]
}
```

