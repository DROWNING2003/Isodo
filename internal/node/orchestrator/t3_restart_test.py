import contextlib
import os
import pathlib
import signal
import subprocess
import sys
import tempfile
import unittest
from t3_restart import restart


@unittest.skipUnless(sys.platform == "linux" and hasattr(os, "pidfd_open"), "Linux pidfds required")
class T3RestartTest(unittest.TestCase):
    @contextlib.contextmanager
    def launcher(self, ignore=False):
        code = "import signal,time; " + ("signal.signal(signal.SIGTERM,signal.SIG_IGN); " if ignore else "") + "print('ready',flush=True); time.sleep(60)"
        child = subprocess.Popen([sys.executable, "-c", code, "bin.mjs", "serve"], stdout=subprocess.PIPE, text=True)
        with tempfile.TemporaryDirectory() as directory:
            pidfile = pathlib.Path(directory) / "t3.pid"
            pidfile.write_text(str(child.pid))
            try:
                self.assertEqual(child.stdout.readline().strip(), "ready")
                yield child, pidfile
            finally:
                if child.poll() is None:
                    child.kill()
                child.wait(timeout=5)
                child.stdout.close()

    def test_graceful_exit(self):
        with self.launcher() as (child, pidfile):
            restart(pidfile, sys.executable, .2)
            self.assertEqual(child.wait(timeout=5), -signal.SIGTERM)

    def test_hung_exit_escalates(self):
        with self.launcher(ignore=True) as (child, pidfile):
            restart(pidfile, sys.executable, .1)
            self.assertEqual(child.wait(timeout=5), -signal.SIGKILL)

    def test_foreign_process_is_not_signalled(self):
        with self.launcher() as (child, pidfile):
            with self.assertRaisesRegex(RuntimeError, "unrelated"):
                restart(pidfile, "/not-the-T3-binary", .1)
            self.assertIsNone(child.poll())

    def test_init_pid_is_rejected(self):
        with tempfile.TemporaryDirectory() as directory:
            pidfile = pathlib.Path(directory) / "t3.pid"
            pidfile.write_text("1")
            with self.assertRaisesRegex(RuntimeError, "invalid"):
                restart(pidfile, sys.executable, .1)


if __name__ == "__main__":
    unittest.main(verbosity=2)
