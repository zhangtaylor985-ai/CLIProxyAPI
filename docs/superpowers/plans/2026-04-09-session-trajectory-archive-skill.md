# Session Trajectory Archive Skill Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a repo-local skill that standardizes monitoring, resuming, and completing CLIProxyAPI session trajectory archive runs.

**Architecture:** Place a single orchestrating skill under `.codex/skills/session-trajectory-archive-ops` with a concise trigger-focused `SKILL.md` plus a command reference file. Keep the workflow centered on `run_id`, `run-state.json`, the archive output root on `/Volumes/Storage`, and the correct repo root `/Users/taylor/code/tools/CLIProxyAPI-ori`.

**Tech Stack:** Markdown skill docs, repo-local `.codex/skills` layout, shell commands

---

### Task 1: Create the skill entry

**Files:**
- Create: `.codex/skills/session-trajectory-archive-ops/SKILL.md`

- [ ] **Step 1: Define the trigger-focused frontmatter**

```markdown
---
name: session-trajectory-archive-ops
description: Use when CLIProxyAPI session trajectory data is consuming too much PostgreSQL storage...
---
```

- [ ] **Step 2: Document the standard workflow**

```markdown
4. 若要新开一轮：
python3 scripts/session_trajectory_archive.py ...
```

- [ ] **Step 3: Document completion conditions**

```markdown
- run-state.json 的 phase 为 completed
- deleted 中四类删除计数均存在
```

### Task 2: Add command reference

**Files:**
- Create: `.codex/skills/session-trajectory-archive-ops/references/commands.md`

- [ ] **Step 1: Add new-run and resume commands**

```bash
python3 scripts/session_trajectory_archive.py \
  --output-root /Volumes/Storage/CLIProxyAPI-session-archives \
  --run-id <run-id>
```

- [ ] **Step 2: Add monitoring and verification commands**

```bash
ps -Ao pid,command | rg 'session_trajectory_archive.py'
```

### Task 3: Verify repo-local skill layout

**Files:**
- Test: `.codex/skills/session-trajectory-archive-ops/SKILL.md`

- [ ] **Step 1: List the skill files**

Run: `find .codex/skills/session-trajectory-archive-ops -maxdepth 2 -type f | sort`
Expected: `SKILL.md` and `references/commands.md`

- [ ] **Step 2: Read the frontmatter for sanity**

Run: `sed -n '1,80p' .codex/skills/session-trajectory-archive-ops/SKILL.md`
Expected: valid `name` and `description`, followed by the skill body.
