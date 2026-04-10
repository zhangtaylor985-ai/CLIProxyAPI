import gzip
import importlib.util
import json
import pathlib
import sys
import tempfile
import unittest


REPO_ROOT = pathlib.Path(__file__).resolve().parents[1]
MODULE_PATH = REPO_ROOT / "scripts" / "import_session_trajectory_archive.py"


def load_module():
    spec = importlib.util.spec_from_file_location("import_session_trajectory_archive", MODULE_PATH)
    module = importlib.util.module_from_spec(spec)
    assert spec.loader is not None
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


def write_jsonl_gz(path: pathlib.Path, rows: list[dict]) -> None:
    with gzip.open(path, "wt", encoding="utf-8", newline="\n") as handle:
        for row in rows:
            handle.write(json.dumps(row))
            handle.write("\n")


def write_copy_escaped_jsonl_gz(path: pathlib.Path, rows: list[dict]) -> None:
    with gzip.open(path, "wt", encoding="utf-8", newline="\n") as handle:
        for row in rows:
            # session_trajectory_archive.py currently writes COPY text output,
            # which doubles backslashes inside the JSON payload.
            handle.write(json.dumps(row).replace("\\", "\\\\"))
            handle.write("\n")


class ImportSessionTrajectoryArchiveTests(unittest.TestCase):
    def test_select_candidate_sessions_with_time_window(self):
        mod = load_module()
        with tempfile.TemporaryDirectory() as tmpdir:
            tmp = pathlib.Path(tmpdir)
            sessions_file = tmp / "session_trajectory_sessions.jsonl.gz"
            candidate_file = tmp / "candidate_sessions.csv"
            write_jsonl_gz(
                sessions_file,
                [
                    {"id": "00000000-0000-0000-0000-000000000001", "last_activity_at": "2026-04-07T11:47:42Z"},
                    {"id": "00000000-0000-0000-0000-000000000002", "last_activity_at": "2026-04-07T12:00:00Z"},
                    {"id": "00000000-0000-0000-0000-000000000003", "last_activity_at": "2026-04-07T15:29:42Z"},
                    {"id": "00000000-0000-0000-0000-000000000004", "last_activity_at": "2026-04-07T15:29:43Z"},
                ],
            )
            start = mod.parse_dt("2026-04-07T11:47:43Z")
            end = mod.parse_dt("2026-04-07T15:29:42Z")

            selected, count = mod.select_candidate_sessions(
                sessions_file=sessions_file,
                candidate_file=candidate_file,
                start_time=start,
                end_time=end,
            )

            self.assertEqual(count, 2)
            self.assertEqual(
                selected,
                {
                    "00000000-0000-0000-0000-000000000002",
                    "00000000-0000-0000-0000-000000000003",
                },
            )
            self.assertEqual(
                candidate_file.read_text(encoding="utf-8").splitlines(),
                [
                    "00000000-0000-0000-0000-000000000002",
                    "00000000-0000-0000-0000-000000000003",
                ],
            )

    def test_resolve_import_tables_respects_skip_request_exports(self):
        mod = load_module()

        self.assertEqual(
            mod.resolve_import_tables(skip_request_exports=False),
            [
                "session_trajectory_sessions",
                "session_trajectory_requests",
                "session_trajectory_session_aliases",
                "session_trajectory_request_exports",
            ],
        )
        self.assertEqual(
            mod.resolve_import_tables(skip_request_exports=True),
            [
                "session_trajectory_sessions",
                "session_trajectory_requests",
                "session_trajectory_session_aliases",
            ],
        )

    def test_validate_time_window_raises_when_start_after_end(self):
        mod = load_module()

        with self.assertRaises(SystemExit):
            mod.validate_time_window(
                start_time=mod.parse_dt("2026-04-08T00:00:00Z"),
                end_time=mod.parse_dt("2026-04-07T00:00:00Z"),
            )

    def test_read_jsonl_gz_accepts_copy_escaped_rows(self):
        mod = load_module()

        with tempfile.TemporaryDirectory() as tmpdir:
            tmp = pathlib.Path(tmpdir)
            path = tmp / "session_trajectory_requests.jsonl.gz"
            rows = [
                {
                    "id": "0fccfb68-ff7d-4fb0-96a0-3cc02c80c773",
                    "request_json": {
                        "tools": [
                            {
                                "description": 'before running \\"mkdir foo/bar\\", first use `ls foo`',
                            }
                        ]
                    },
                }
            ]
            write_copy_escaped_jsonl_gz(path, rows)

            loaded = list(mod.read_jsonl_gz(path))

            self.assertEqual(loaded, rows)

    def test_build_storage_workflow_sql_mentions_volumes_storage_and_tables(self):
        mod = load_module()

        sql = mod.build_storage_workflow_sql(
            schema="public",
            database_name="cliproxy",
            tablespace_name="cliproxy_ts",
            tablespace_location="/Volumes/Storage/postgres_tablespaces/cliproxy_ts",
        )

        self.assertIn("/Volumes/Storage/postgres_tablespaces/cliproxy_ts", sql)
        self.assertIn('ALTER DATABASE "cliproxy" SET default_tablespace = \'cliproxy_ts\'', sql)
        self.assertIn('"public"."session_trajectory_sessions"', sql)
        self.assertIn('"public"."session_trajectory_requests"', sql)

    def test_build_copy_csv_script_uses_single_backslash_null_marker(self):
        mod = load_module()

        script = mod.build_copy_csv_script(
            schema="public",
            table_name="session_trajectory_sessions",
            csv_file=pathlib.Path("/tmp/session_trajectory_sessions.csv"),
        )

        self.assertIn("WITH (FORMAT csv, NULL '\\N')", script)
        self.assertNotIn("NULL '\\\\N'", script)


if __name__ == "__main__":
    unittest.main()
