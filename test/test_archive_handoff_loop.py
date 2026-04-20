import importlib.util
import pathlib
import tempfile
import unittest


REPO_ROOT = pathlib.Path(__file__).resolve().parents[1]
MODULE_PATH = REPO_ROOT / "scripts" / "archive_handoff_loop.py"


def load_module():
    spec = importlib.util.spec_from_file_location("archive_handoff_loop", MODULE_PATH)
    module = importlib.util.module_from_spec(spec)
    assert spec.loader is not None
    spec.loader.exec_module(module)
    return module


class ArchiveHandoffLoopTests(unittest.TestCase):
    def test_archive_phase_progress_hint(self):
        mod = load_module()
        self.assertEqual(mod.archive_phase_progress_hint(phase="initialized", request_file_size=0), "0%")
        self.assertEqual(mod.archive_phase_progress_hint(phase="candidates_materialized", request_file_size=0), "5-10%")
        self.assertEqual(mod.archive_phase_progress_hint(phase="candidates_materialized", request_file_size=123), "10-50%")
        self.assertEqual(mod.archive_phase_progress_hint(phase="completed", request_file_size=0), "100%")

    def test_extract_cursor_candidate_for_task3_delete(self):
        mod = load_module()
        record = {
            "task": "task3_live_export_window_delete",
            "window": {
                "start_time": "2026-04-08T17:27:44Z",
                "end_time": "2026-04-09T18:05:22Z",
            },
            "export": {
                "manifest_path": "/tmp/task3.json",
                "export_root": "/tmp/export-root",
                "exported_sessions": 1459,
                "exported_files": 37059,
            },
            "remote_delete": {
                "remote_min_last_activity_after_delete": "2026-04-09T19:24:09Z",
            },
        }
        candidate = mod.extract_cursor_candidate(record, pathlib.Path("/tmp/task3-record.json"))
        self.assertEqual(candidate["cursor_type"], "task3_live_export_delete")
        self.assertEqual(candidate["processed_end_time"], "2026-04-09T18:05:22Z")
        self.assertEqual(candidate["remote_min_last_activity_after_delete"], "2026-04-09T19:24:09Z")

    def test_choose_latest_cursor_prefers_newer_processed_end(self):
        mod = load_module()
        older = {"processed_end_time": "2026-04-08T17:27:44Z", "cursor_type": "archive_handoff"}
        newer = {"processed_end_time": "2026-04-09T18:05:22Z", "cursor_type": "task3_live_export_delete"}
        chosen = mod.choose_latest_cursor([older, newer])
        self.assertEqual(chosen["cursor_type"], "task3_live_export_delete")

    def test_infer_resume_run_id_from_interrupted_chain(self):
        mod = load_module()
        chain_state = {
            "archive_run_id": "session-archive-20260411T094922Z",
            "current_stage": "interrupted",
            "status": "interrupted",
        }
        current_cursor = {
            "cursor_type": "task3_live_export_delete",
            "processed_end_time": "2026-04-09T18:05:22Z",
        }
        self.assertEqual(
            mod.infer_resume_run_id(chain_state=chain_state, current_cursor=current_cursor),
            "session-archive-20260411T094922Z",
        )

    def test_recover_completed_chain_cursor_uses_handoff_record(self):
        mod = load_module()
        with tempfile.TemporaryDirectory() as tmpdir:
            handoff_dir = pathlib.Path(tmpdir)
            record_path = handoff_dir / "session-archive-20260411T094922Z.json"
            record_path.write_text(
                """{
  "source_cursor": {
    "archive_run_id": "session-archive-20260411T094922Z",
    "archive_cutoff_at": "2026-04-10T09:49:22Z",
    "archive_min_last_activity_at": "2026-04-09T19:24:09Z",
    "archive_max_last_activity_at": "2026-04-10T09:49:02Z"
  },
  "export_cursor": {
    "manifest_path": "/tmp/manifest.json",
    "export_root": "/tmp/export-root",
    "exported_sessions": 1141,
    "exported_files": 16094
  }
}""",
                encoding="utf-8",
            )
            chain_state = {
                "archive_run_id": "session-archive-20260411T094922Z",
                "current_stage": "completed",
            }
            recovered = mod.recover_completed_chain_cursor(chain_state=chain_state, handoff_dir=handoff_dir)
            self.assertIsNotNone(recovered)
            assert recovered is not None
            self.assertEqual(recovered["archive_run_id"], "session-archive-20260411T094922Z")
            self.assertEqual(recovered["processed_end_time"], "2026-04-10T09:49:02Z")

    def test_collect_summary_reads_run_state_and_request_file(self):
        mod = load_module()
        with tempfile.TemporaryDirectory() as tmpdir:
            root = pathlib.Path(tmpdir)
            archive_root = root / "archives"
            run_dir = archive_root / "runs" / "session-archive-20260411T094922Z"
            run_dir.mkdir(parents=True)
            (run_dir / "run-state.json").write_text(
                """{
  "phase": "candidates_materialized",
  "completion_status": "in_progress",
  "counts": {"sessions": 1141, "requests": 16094},
  "cursor": {"candidate_sessions": 1141},
  "updated_at": "2026-04-11T12:35:53Z"
}""",
                encoding="utf-8",
            )
            (run_dir / "session_trajectory_requests.jsonl.gz").write_bytes(b"x" * 1234)
            state_path = root / "state.json"
            cursor_path = root / "cursor.json"
            chain_state_path = root / "chain.json"
            summary_path = root / "summary.json"
            state_path.write_text(
                '{"status":"running","current_stage":"cycle_running","child_pid":123,"remote_stats":{"eligible_sessions":1141}}',
                encoding="utf-8",
            )
            cursor_path.write_text(
                '{"cursor_type":"task3_live_export_delete","processed_end_time":"2026-04-09T18:05:22Z"}',
                encoding="utf-8",
            )
            chain_state_path.write_text(
                '{"archive_run_id":"session-archive-20260411T094922Z","current_stage":"archive_running","status":"running","archive_cutoff_at":"2026-04-10T09:49:22Z","archive_output_dir":"%s"}'
                % str(run_dir).replace("\\", "\\\\"),
                encoding="utf-8",
            )
            summary = mod.collect_summary(
                archive_root=archive_root,
                state_path=state_path,
                cursor_path=cursor_path,
                chain_state_path=chain_state_path,
                summary_path=summary_path,
            )
            self.assertEqual(summary["run_id"], "session-archive-20260411T094922Z")
            self.assertEqual(summary["request_file_size_bytes"], 1234)
            self.assertEqual(summary["progress_hint"], "10-50%")


if __name__ == "__main__":
    unittest.main()
