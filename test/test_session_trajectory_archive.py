import importlib.util
import json
import pathlib
import sys
import tempfile
import unittest
from datetime import datetime, timezone


REPO_ROOT = pathlib.Path(__file__).resolve().parents[1]
MODULE_PATH = REPO_ROOT / "scripts" / "session_trajectory_archive.py"


def load_module():
    spec = importlib.util.spec_from_file_location("session_trajectory_archive", MODULE_PATH)
    module = importlib.util.module_from_spec(spec)
    assert spec.loader is not None
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


class SessionTrajectoryArchiveTests(unittest.TestCase):
    def test_quote_ident_escapes_double_quotes(self):
        mod = load_module()

        self.assertEqual(mod.quote_ident('public'), '"public"')
        self.assertEqual(mod.quote_ident('public"test'), '"public""test"')

    def test_run_state_round_trip_preserves_cursor(self):
        mod = load_module()
        with tempfile.TemporaryDirectory() as tmpdir:
            state_path = pathlib.Path(tmpdir) / "run-state.json"
            now = datetime(2026, 4, 8, 15, 30, tzinfo=timezone.utc)
            state = mod.RunState(
                run_id="session-archive-20260408T153000Z",
                schema="public",
                inactive_hours=24,
                cutoff_at=now,
                output_dir=pathlib.Path(tmpdir) / "archive",
                phase="exported",
                cursor={"session_count": 12, "request_count": 345},
            )

            mod.write_run_state(state_path, state)
            loaded = mod.read_run_state(state_path)

            self.assertEqual(loaded.run_id, state.run_id)
            self.assertEqual(loaded.schema, "public")
            self.assertEqual(loaded.inactive_hours, 24)
            self.assertEqual(loaded.cutoff_at, now)
            self.assertEqual(loaded.phase, "exported")
            self.assertEqual(loaded.cursor["request_count"], 345)
            self.assertEqual(loaded.output_dir, pathlib.Path(tmpdir) / "archive")

    def test_count_csv_rows_excludes_header(self):
        mod = load_module()
        with tempfile.TemporaryDirectory() as tmpdir:
            csv_path = pathlib.Path(tmpdir) / "rows.csv"
            csv_path.write_text("id,name\n1,a\n2,b\n", encoding="utf-8")

            self.assertEqual(mod.count_csv_rows(csv_path), 2)


if __name__ == "__main__":
    unittest.main()
