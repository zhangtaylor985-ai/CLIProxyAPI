import importlib.util
import pathlib
import sys
import tempfile
import unittest


REPO_ROOT = pathlib.Path(__file__).resolve().parents[1]
MODULE_PATH = REPO_ROOT / "scripts" / "live_export_delete_loop.py"


def load_module():
    spec = importlib.util.spec_from_file_location("live_export_delete_loop", MODULE_PATH)
    module = importlib.util.module_from_spec(spec)
    assert spec.loader is not None
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


class LiveExportDeleteLoopTests(unittest.TestCase):
    def test_parse_export_progress_line_matches_matched_sessions(self):
        mod = load_module()

        payload = mod.parse_export_progress_line("2026/04/12 12:00:00 matched 1139 sessions for export")

        self.assertEqual(payload, {"kind": "matched", "matched_sessions": 1139})

    def test_parse_export_progress_line_matches_exported_sessions(self):
        mod = load_module()

        payload = mod.parse_export_progress_line("2026/04/12 12:00:01 exported 17/1139 sessions (221 files) session=abc")

        self.assertEqual(
            payload,
            {
                "kind": "exported",
                "done_sessions": 17,
                "total_sessions": 1139,
                "exported_files": 221,
            },
        )

    def test_rewrite_session_id_file_from_candidate_preserves_bounds(self):
        mod = load_module()

        with tempfile.TemporaryDirectory() as tmpdir:
            root = pathlib.Path(tmpdir)
            candidate_file = root / "candidate_sessions.csv"
            session_id_file = root / "session_ids.txt"
            candidate_file.write_text(
                "\n".join(
                    [
                        "session-a,2026-04-09T19:24:09Z",
                        "session-b,2026-04-10T09:49:02Z",
                    ]
                )
                + "\n",
                encoding="utf-8",
            )

            stats = mod.rewrite_session_id_file_from_candidate(
                candidate_file=candidate_file,
                session_id_file=session_id_file,
            )

            self.assertEqual(stats["candidate_sessions"], 2)
            self.assertEqual(stats["min_last_activity_at"], "2026-04-09T19:24:09Z")
            self.assertEqual(stats["max_last_activity_at"], "2026-04-10T09:49:02Z")
            self.assertEqual(session_id_file.read_text(encoding="utf-8"), "session-a\nsession-b\n")

    def test_cycle_progress_hint_uses_export_progress_ratio(self):
        mod = load_module()

        hint = mod.cycle_progress_hint(
            {
                "phase": "snapshot_materialized",
                "export_progress": {"done_sessions": 285, "total_sessions": 1139},
            }
        )

        self.assertEqual(hint, f"{round(285 / 1139 * 100, 1)}%")


if __name__ == "__main__":
    unittest.main()
