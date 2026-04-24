---
name: cliproxyapi-mainline-release-flow
description: Use when CLIProxyAPI backend and/or Management Center frontend work from feature worktrees must be merged back to main, pushed to remote main, synced into source worktrees, or prepared for production deployment. Trigger for requests like “合并到 main”, “推主线”, “从 worktree 回主分支”, “上线”, “发布”, or when coordinating backend/frontend release flow.
---

# CLIProxyAPI Mainline Release Flow

## Core Rule

Treat the backend and frontend as one release unit:

- Backend source repo: `/Users/taylor/code/tools/CLIProxyAPI-ori`
- Frontend source repo: `/Users/taylor/code/tools/Cli-Proxy-API-Management-Center-ori`
- Backend feature worktrees usually live under `/Users/taylor/code/tools/CLIProxyAPI-*`
- Frontend feature worktrees usually live under `/Users/taylor/code/tools/Cli-Proxy-API-Management-Center-*`

When the user asks to merge/push/release main, check both repos. If a task changed both, merge and push both before calling the work done. If only one repo changed, still confirm the other repo is clean and not behind, then say clearly that only one side had code to publish.

## Fast Path

Use this path when the feature worktree is already clean, tested, and based on current `origin/main`.

1. Check worktree state for both repos.

```bash
git status --short --branch
git fetch origin main
git rev-list --left-right --count HEAD...origin/main
```

Interpret `git rev-list --left-right --count HEAD...origin/main`:

- `N 0`: feature branch is ahead and can be pushed to main if tests pass.
- `0 0`: already matches main.
- `N M` with `M > 0`: rebase or merge `origin/main` first, then rerun relevant tests.

2. Prefer pushing the verified feature HEAD directly to main when `origin/main` is an ancestor of `HEAD`.

```bash
git merge-base --is-ancestor origin/main HEAD
git push origin HEAD:main
```

This avoids creating an extra temporary main worktree just to fast-forward. Use a temporary main worktree only when the feature worktree is unsuitable, dirty in unrelated ways, or you need an isolated final verification tree.

3. Verify remote main after pushing.

```bash
git ls-remote origin refs/heads/main
```

4. Sync the source worktrees after remote main moves.

```bash
git status --short --branch
git fetch origin main
git pull --ff-only
```

If the source worktree has local changes, stash them first with a descriptive message. Do not drop the stash unless you have compared and confirmed it is safe.

## Verification Scope

Use the smallest safe verification set, then widen when risk requires it.

- Backend-only docs/skill changes: `python3 <skill-creator>/scripts/quick_validate.py <skill-folder>` plus `git diff --check`.
- Backend code changes: targeted Go tests for touched packages; use `go test ./...` before pushing main when shared runtime, routing, billing, auth, Claude/GPT compatibility, or public API behavior changed.
- Frontend changes: `npm run build` and `npm run lint`.
- Backend plus frontend changes: run both sides. Run backend and frontend checks in parallel when they do not share files or ports.

Do not rerun a costly full suite only because a detached temporary main worktree fast-forwarded to the exact same commit already tested. If final safety matters, prefer a quick final smoke/diff check and record that the commit was already tested.

## Stop Conditions

Stop and report instead of pushing main when:

- `origin/main` is not an ancestor and rebase/merge has conflicts.
- Required tests fail.
- Either source repo has local changes that cannot be safely stashed.
- The user asked for manual PR review instead of direct main push.
- Production deployment would restart service during an unsafe window.

## Deployment Handoff

Pushing main is not the same as production deployment.

When the user asks to “上线 / deploy / 重启线上” after main is pushed:

- Use `cliproxyapi-systemd-deploy` for backend production deployment.
- If frontend changed, complete the frontend release/deploy path before saying the full release is live. If the frontend deployment mechanism is unclear, say that main is pushed but frontend production has not been redeployed yet.
- Report backend commit, frontend commit, tests, whether binaries/assets were rebuilt, and whether service status checks passed.

## Reporting

Keep the final report short and explicit:

- backend main commit
- frontend main commit
- tests run
- whether source worktrees were synced
- whether production was deployed or only main was pushed
