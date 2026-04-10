import importlib.util
import json
import pathlib
import sys
import tempfile
import unittest


REPO_ROOT = pathlib.Path(__file__).resolve().parents[1]
MODULE_PATH = REPO_ROOT / "scripts" / "managed_live_export.py"


def load_module():
    spec = importlib.util.spec_from_file_location("managed_live_export", MODULE_PATH)
    module = importlib.util.module_from_spec(spec)
    assert spec.loader is not None
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


class ManagedLiveExportTests(unittest.TestCase):
    def test_default_label_sanitizes_times(self):
        mod = load_module()
        label = mod.default_label(
            start_time="2026-04-08T17:27:44Z",
            end_time="2026-04-09T18:05:22Z",
        )
        self.assertIn("2026-04-08T17-27-44Z", label)
        self.assertIn("2026-04-09T18-05-22Z", label)
        self.assertTrue(label.startswith("com.codex.session_live_export."))

    def test_default_state_path_uses_manifest_dir_and_label(self):
        mod = load_module()
        path = mod.default_state_path(
            manifest_dir=pathlib.Path("/tmp/manifests"),
            label="com.codex.session_live_export.2026-04-08T17-27-44Z.to.2026-04-09T18-05-22Z",
        )
        self.assertEqual(
            path,
            pathlib.Path("/tmp/manifests/com.codex.session_live_export.2026-04-08T17-27-44Z.to.2026-04-09T18-05-22Z.state.json"),
        )

    def test_update_state_writes_json(self):
        mod = load_module()
        with tempfile.TemporaryDirectory() as tmpdir:
            state_path = pathlib.Path(tmpdir) / "state.json"
            state = {"label": "com.codex.test", "status": "submitted"}
            mod.update_state(state, state_path, current_stage="launchd_submitted")
            payload = json.loads(state_path.read_text(encoding="utf-8"))
            self.assertEqual(payload["label"], "com.codex.test")
            self.assertEqual(payload["status"], "submitted")
            self.assertEqual(payload["current_stage"], "launchd_submitted")
            self.assertIn("updated_at", payload)


if __name__ == "__main__":
    unittest.main()
