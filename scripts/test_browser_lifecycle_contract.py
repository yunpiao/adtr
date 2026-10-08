"""Display/Chromium selection contracts; no browser or database is launched."""
import os
import unittest
from unittest.mock import patch

import test_auth_e2e


class BrowserLifecycleContract(unittest.TestCase):
    def test_lifecycle_suites_use_headed_chromium_with_owned_xvfb_display(self):
        with patch.object(test_auth_e2e.sys, "platform", "linux"), \
             patch.dict(os.environ, {}, clear=True), \
             patch.object(test_auth_e2e.shutil, "which", return_value="/usr/bin/xvfb-run"):
            for suite in ["session-invalidation", "directory-readers"]:
                with self.subTest(suite=suite):
                    self.assertEqual(test_auth_e2e.browser_command(suite), [
                        "xvfb-run", "-a", "npm", "run", "test:e2e", "--prefix",
                        "web", "--", f"e2e/{suite}.spec.ts", "--headed",
                    ])

    def test_existing_display_is_used_without_requiring_xvfb(self):
        with patch.object(test_auth_e2e.sys, "platform", "linux"), \
             patch.dict(os.environ, {"DISPLAY": ":99"}, clear=True), \
             patch.object(test_auth_e2e.shutil, "which") as lookup:
            command = test_auth_e2e.browser_command("session-invalidation")
        self.assertEqual(command[0], "npm")
        self.assertEqual(command[-1], "--headed")
        lookup.assert_not_called()

    def test_native_non_linux_display_does_not_require_xvfb(self):
        for platform in ["darwin", "win32"]:
            with self.subTest(platform=platform), \
                 patch.object(test_auth_e2e.sys, "platform", platform), \
                 patch.dict(os.environ, {}, clear=True), \
                 patch.object(test_auth_e2e.shutil, "which") as lookup:
                command = test_auth_e2e.browser_command("directory-readers")
                self.assertEqual(command[0], "npm")
                self.assertEqual(command[-1], "--headed")
                lookup.assert_not_called()

    def test_missing_display_fails_before_any_database_or_fixture_starts(self):
        with patch.object(test_auth_e2e.sys, "platform", "linux"), \
             patch.dict(os.environ, {"DISPLAY": ""}, clear=True), \
             patch.object(test_auth_e2e.shutil, "which", return_value=None), \
             patch("sys.argv", ["test_auth_e2e.py", "--suite", "session-invalidation"]), \
             patch.object(test_auth_e2e.subprocess, "run") as run, \
             patch.object(test_auth_e2e.subprocess, "Popen") as start:
            with self.assertRaisesRegex(RuntimeError, "requires headed Chromium and a display"):
                test_auth_e2e.main()
        run.assert_not_called()
        start.assert_not_called()

    def test_ordinary_suites_keep_headless_default_without_display_dependency(self):
        with patch.object(test_auth_e2e.sys, "platform", "linux"), \
             patch.dict(os.environ, {}, clear=True), \
             patch.object(test_auth_e2e.shutil, "which") as lookup:
            for suite in ["auth", "profile", "directory", "directory-controls"]:
                with self.subTest(suite=suite):
                    self.assertEqual(test_auth_e2e.browser_command(suite), [
                        "npm", "run", "test:e2e", "--prefix", "web", "--",
                        f"e2e/{suite}.spec.ts",
                    ])
        lookup.assert_not_called()


if __name__ == "__main__":
    unittest.main()
