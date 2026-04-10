import importlib.util
import json
import pathlib
import sys
import tempfile
import unittest


REPO_ROOT = pathlib.Path(__file__).resolve().parents[1]
MODULE_PATH = REPO_ROOT / "scripts" / "archive_export_handoff.py"


def load_module():
    spec = importlib.util.spec_from_file_location("archive_export_handoff", MODULE_PATH)
    module = importlib.util.module_from_spec(spec)
    assert spec.loader is not None
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


class ArchiveExportHandoffTests(unittest.TestCase):
    def test_build_handoff_record_contains_source_and_export_cursors(self):
        mod = load_module()

        run_state = {
            "run_id": "session-archive-20260409T122346Z",
            "cutoff_at": "2026-04-08T12:23:46Z",
            "output_dir": "/Volumes/Storage/CLIProxyAPI-session-archives/runs/session-archive-20260409T122346Z",
            "completion_status": "completed_with_warnings",
            "counts": {"sessions": 2338, "requests": 22041},
            "vacuum": {"summary": "unknown"},
            "warnings": [{"code": "vacuum_unknown", "message": "VACUUM did not confirm success"}],
        }
        manifest = {
            "manifest_path": "/Volumes/Storage/session-trajectory-export-manifests/session-trajectory-export-20260409T130837Z.json",
            "export_root": "/Volumes/Storage/session-trajectory-export-after-20260407T114743Z",
            "filters": {
                "start_time": "2026-04-07T11:47:43Z",
                "end_time": "2026-04-07T15:29:42Z",
            },
            "exported_sessions": 470,
            "exported_files": 7873,
        }

        record = mod.build_handoff_record(
            run_state=run_state,
            manifest=manifest,
            target_pg_dsn="postgresql://postgres:root123@localhost:5433/cliproxy",
        )

        self.assertEqual(record["source_cursor"]["archive_run_id"], run_state["run_id"])
        self.assertEqual(record["source_cursor"]["archive_cutoff_at"], run_state["cutoff_at"])
        self.assertEqual(record["source_cursor"]["archive_completion_status"], "completed_with_warnings")
        self.assertEqual(record["source_cursor"]["archive_vacuum"]["summary"], "unknown")
        self.assertEqual(record["export_cursor"]["manifest_path"], manifest["manifest_path"])
        self.assertEqual(record["export_cursor"]["exported_files"], 7873)
        self.assertEqual(record["target_pg"]["dsn"], "postgresql://postgres:root123@localhost:5433/cliproxy")

    def test_write_handoff_record_creates_latest_and_per_run_files(self):
        mod = load_module()
        record = {
            "source_cursor": {"archive_run_id": "session-archive-20260409T122346Z"},
            "export_cursor": {"manifest_path": "/tmp/manifest.json"},
        }

        with tempfile.TemporaryDirectory() as tmpdir:
            handoff_dir = pathlib.Path(tmpdir) / "handoffs"
            latest_path, per_run_path = mod.write_handoff_record(handoff_dir=handoff_dir, record=record)

            self.assertTrue(latest_path.exists())
            self.assertTrue(per_run_path.exists())
            self.assertEqual(json.loads(latest_path.read_text(encoding="utf-8")), record)
            self.assertEqual(json.loads(per_run_path.read_text(encoding="utf-8")), record)

    def test_default_state_path_uses_run_id_under_handoff_dir(self):
        mod = load_module()
        with tempfile.TemporaryDirectory() as tmpdir:
            handoff_dir = pathlib.Path(tmpdir) / "handoffs"
            path = mod.default_state_path(handoff_dir=handoff_dir, run_id="session-archive-20260409T172744Z")
            self.assertEqual(path, handoff_dir / "session-archive-20260409T172744Z.state.json")

    def test_completed_handoff_record_path_uses_run_id_under_handoff_dir(self):
        mod = load_module()
        with tempfile.TemporaryDirectory() as tmpdir:
            handoff_dir = pathlib.Path(tmpdir) / "handoffs"
            path = mod.completed_handoff_record_path(
                handoff_dir=handoff_dir,
                run_id="session-archive-20260409T172744Z",
            )
            self.assertEqual(path, handoff_dir / "session-archive-20260409T172744Z.json")

    def test_update_state_writes_json_payload(self):
        mod = load_module()
        with tempfile.TemporaryDirectory() as tmpdir:
            state_path = pathlib.Path(tmpdir) / "handoff.state.json"
            state = {"run_id": "session-archive-20260409T172744Z", "status": "running"}
            mod.update_state(state, state_path, current_stage="import_running")

            payload = json.loads(state_path.read_text(encoding="utf-8"))
            self.assertEqual(payload["run_id"], "session-archive-20260409T172744Z")
            self.assertEqual(payload["status"], "running")
            self.assertEqual(payload["current_stage"], "import_running")
            self.assertIn("updated_at", payload)

    def test_is_completed_run_state_requires_completed_phase(self):
        mod = load_module()

        self.assertTrue(mod.is_completed_run_state({"phase": "completed"}))
        self.assertFalse(mod.is_completed_run_state({"phase": "exported"}))
        self.assertFalse(mod.is_completed_run_state({}))

    def test_resolve_completed_handoff_requires_matching_record_and_manifest(self):
        mod = load_module()

        with tempfile.TemporaryDirectory() as tmpdir:
            root = pathlib.Path(tmpdir)
            handoff_dir = root / "handoffs"
            manifest_path = root / "manifest.json"
            manifest_path.write_text("{}", encoding="utf-8")
            record_path = handoff_dir / "session-archive-20260409T172744Z.json"
            record_path.parent.mkdir(parents=True, exist_ok=True)
            record_path.write_text(
                json.dumps(
                    {
                        "source_cursor": {"archive_run_id": "session-archive-20260409T172744Z"},
                        "export_cursor": {"manifest_path": str(manifest_path)},
                    }
                ),
                encoding="utf-8",
            )

            resolved = mod.resolve_completed_handoff(
                handoff_dir=handoff_dir,
                run_id="session-archive-20260409T172744Z",
            )

            self.assertIsNotNone(resolved)
            assert resolved is not None
            self.assertEqual(resolved["record_path"], record_path)
            self.assertEqual(resolved["manifest_path"], manifest_path.resolve())

    def test_resolve_completed_handoff_returns_none_without_manifest(self):
        mod = load_module()

        with tempfile.TemporaryDirectory() as tmpdir:
            root = pathlib.Path(tmpdir)
            handoff_dir = root / "handoffs"
            record_path = handoff_dir / "session-archive-20260409T172744Z.json"
            record_path.parent.mkdir(parents=True, exist_ok=True)
            record_path.write_text(
                json.dumps(
                    {
                        "source_cursor": {"archive_run_id": "session-archive-20260409T172744Z"},
                        "export_cursor": {"manifest_path": str(root / 'missing.json')},
                    }
                ),
                encoding="utf-8",
            )

            resolved = mod.resolve_completed_handoff(
                handoff_dir=handoff_dir,
                run_id="session-archive-20260409T172744Z",
            )

            self.assertIsNone(resolved)


if __name__ == "__main__":
    unittest.main()
