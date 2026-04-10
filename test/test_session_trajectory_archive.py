import importlib.util
import pathlib
import subprocess
import sys
import unittest
from unittest import mock


REPO_ROOT = pathlib.Path(__file__).resolve().parents[1]
MODULE_PATH = REPO_ROOT / "scripts" / "session_trajectory_archive.py"


def load_module():
    spec = importlib.util.spec_from_file_location("session_trajectory_archive", MODULE_PATH)
    module = importlib.util.module_from_spec(spec)
    assert spec.loader is not None
    sys.modules[spec.name] = module
    spec.loader.exec_module(module)
    return module


class FakeProc:
    def __init__(self, *, returncode=0, stdout="", stderr="", timeout=False):
        self.returncode = returncode
        self._stdout = stdout
        self._stderr = stderr
        self._timeout = timeout
        self._communicate_calls = 0
        self.killed = False

    def communicate(self, timeout=None):
        self._communicate_calls += 1
        if self._timeout and self._communicate_calls == 1:
            raise subprocess.TimeoutExpired(cmd="psql", timeout=timeout)
        return self._stdout, self._stderr

    def kill(self):
        self.killed = True


class SessionTrajectoryArchiveTests(unittest.TestCase):
    def test_vacuum_table_returns_ok_on_success(self):
        mod = load_module()
        proc = FakeProc(returncode=0)
        with mock.patch.object(mod.subprocess, "Popen", return_value=proc):
            result = mod.vacuum_table("postgres://example", "public", "session_trajectory_requests", timeout_seconds=30)
        self.assertEqual(result["status"], "ok")
        self.assertEqual(result["table"], "session_trajectory_requests")

    def test_vacuum_table_returns_failed_on_nonzero_exit(self):
        mod = load_module()
        proc = FakeProc(returncode=1, stderr="permission denied")
        with mock.patch.object(mod.subprocess, "Popen", return_value=proc):
            result = mod.vacuum_table("postgres://example", "public", "session_trajectory_requests", timeout_seconds=30)
        self.assertEqual(result["status"], "failed")
        self.assertIn("permission denied", result["detail"])

    def test_vacuum_table_kills_process_on_timeout(self):
        mod = load_module()
        proc = FakeProc(returncode=0, timeout=True)
        with mock.patch.object(mod.subprocess, "Popen", return_value=proc):
            result = mod.vacuum_table("postgres://example", "public", "session_trajectory_requests", timeout_seconds=1)
        self.assertEqual(result["status"], "timed_out")
        self.assertTrue(proc.killed)

    def test_summarize_vacuum_results_distinguishes_unknown_from_failed(self):
        mod = load_module()

        self.assertEqual(mod.summarize_vacuum_results([]), "skipped")
        self.assertEqual(mod.summarize_vacuum_results([{"status": "ok"}]), "ok")
        self.assertEqual(mod.summarize_vacuum_results([{"status": "failed"}]), "failed")
        self.assertEqual(mod.summarize_vacuum_results([{"status": "timed_out"}]), "unknown")

    def test_finalize_completion_marks_warnings_as_completed_with_warnings(self):
        mod = load_module()
        state = mod.RunState(
            run_id="session-archive-20260409T172744Z",
            schema="public",
            inactive_hours=24,
            cutoff_at=mod.utc_now(),
            output_dir=pathlib.Path("/tmp/archive"),
        )
        state.vacuum = {"summary": "unknown"}
        state.warnings = [{"code": "vacuum_unknown", "message": "VACUUM did not confirm success"}]

        mod.finalize_completion(state)

        self.assertEqual(state.completion_status, "completed_with_warnings")


if __name__ == "__main__":
    unittest.main()
