# Session Trajectory Archive Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a resumable archive-and-prune tool for cold session trajectory data, then execute one safe production archive run to local storage.

**Architecture:** A Python operations script materializes an immutable candidate session set, exports all dependent rows to local compressed CSV files, then deletes those rows in dependency order using small batches. Each run writes a state file with its run id, cutoff, counts, and deletion progress so the same run can be resumed safely.

**Tech Stack:** Python 3 standard library, `psql`, PostgreSQL, compressed CSV artifacts

---

### Task 1: Add stateful archive script tests

**Files:**
- Create: `test/test_session_trajectory_archive.py`

- [ ] **Step 1: Write the failing test**

```python
def test_quote_ident_escapes_double_quotes(self):
    mod = load_module()
    self.assertEqual(mod.quote_ident('public"test'), '"public""test"')
```

- [ ] **Step 2: Run test to verify it fails**

Run: `python3 -m unittest discover -s test -p 'test_session_trajectory_archive.py'`
Expected: FAIL because `scripts/session_trajectory_archive.py` does not exist yet.

- [ ] **Step 3: Add round-trip state coverage**

```python
state = mod.RunState(
    run_id="session-archive-20260408T153000Z",
    schema="public",
    inactive_hours=24,
    cutoff_at=now,
    output_dir=pathlib.Path(tmpdir) / "archive",
    phase="exported",
    cursor={"session_count": 12, "request_count": 345},
)
```

- [ ] **Step 4: Re-run the tests**

Run: `python3 -m unittest discover -s test -p 'test_session_trajectory_archive.py'`
Expected: FAIL because the production module still does not exist.

### Task 2: Implement the resumable archive script

**Files:**
- Create: `scripts/session_trajectory_archive.py`

- [ ] **Step 1: Add minimal helpers required by the tests**

```python
def quote_ident(value: str) -> str:
    return '"' + value.replace('"', '""') + '"'
```

- [ ] **Step 2: Add state serialization**

```python
@dataclass
class RunState:
    run_id: str
    schema: str
    inactive_hours: int
    cutoff_at: datetime
    output_dir: pathlib.Path
```

- [ ] **Step 3: Implement CSV counting and gzip helpers**

```python
def count_csv_rows(path: pathlib.Path) -> int:
    with path.open("r", encoding="utf-8", newline="") as handle:
        reader = csv.reader(handle)
        next(reader)
        return sum(1 for _ in reader)
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `python3 -m unittest discover -s test -p 'test_session_trajectory_archive.py'`
Expected: PASS

- [ ] **Step 5: Implement export, verification, deletion, and vacuum flow**

```python
if not phase_at_least(state.phase, "exported"):
    exported_counts = {
        "requests": export_table_to_csv(...),
    }
```

- [ ] **Step 6: Smoke test argument parsing**

Run: `python3 scripts/session_trajectory_archive.py --help`
Expected: exit 0 and prints CLI usage.

### Task 3: Document the archive design

**Files:**
- Create: `docs/superpowers/specs/2026-04-08-session-trajectory-archive-design.md`
- Create: `docs/superpowers/plans/2026-04-08-session-trajectory-archive.md`

- [ ] **Step 1: Write the design doc**

```markdown
- 归档粒度使用“整会话”，不按 request 单日硬切
- 主游标：run_id
```

- [ ] **Step 2: Save the implementation plan**

```markdown
**Goal:** Add a resumable archive-and-prune tool for cold session trajectory data
```

- [ ] **Step 3: Sanity check docs exist**

Run: `ls docs/superpowers/specs docs/superpowers/plans`
Expected: both directories contain the new Markdown files.

### Task 4: Execute the first production archive run

**Files:**
- Modify: `/Volumes/Storage/CLIProxyAPI-session-archives/runs/<run-id>/run-state.json`
- Create: `/Volumes/Storage/CLIProxyAPI-session-archives/runs/<run-id>/*.csv.gz`

- [ ] **Step 1: Export the DSN in the shell**

```bash
export APIKEY_POLICY_PG_DSN='postgres://.../cliproxy?sslmode=require'
```

- [ ] **Step 2: Run the archive script**

```bash
python3 scripts/session_trajectory_archive.py \
  --output-root /Volumes/Storage/CLIProxyAPI-session-archives \
  --inactive-hours 24
```

- [ ] **Step 3: Verify the state file is completed**

Run: `python3 - <<'PY' ...`
Expected: `phase` is `completed`

- [ ] **Step 4: Inspect the latest completed cursor**

Run: `cat /Volumes/Storage/CLIProxyAPI-session-archives/latest_completed.json`
Expected: contains `run_id`, `cutoff_at`, counts, and output directory.
