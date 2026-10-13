"""Exercise demo generation without a compiler; optionally check/build/run it too.

TYTHON_TEST_COMPILER=/absolute/path/to/tsc python3 packages/tython-python/test_demo.py
"""

import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[2]
PREVIEW = ROOT / "packages/vscode-tython/preview"


def files_under(root):
    return {
        path.relative_to(root): path.read_bytes()
        for path in root.rglob("*")
        if path.is_file()
    }


class DemoTests(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory(prefix="tython-demo-test-")
        self.addCleanup(self.temporary.cleanup)
        self.root = Path(self.temporary.name).resolve()
        self.package = self.root / "installed/tython_cli"
        shutil.copytree(
            ROOT / "packages/tython-python/tython_cli",
            self.package,
            ignore=shutil.ignore_patterns("__pycache__", "*.pyc"),
        )
        shutil.copytree(PREVIEW, self.package / "preview")
        (self.package / "build-info.json").write_text(
            json.dumps({"version": "0.0.0", "buildID": "demo-test"})
        )
        self.env = {
            **os.environ,
            "PYTHONPATH": str(self.package.parent),
            "PYTHONDONTWRITEBYTECODE": "1",
        }

    def run_cli(self, *args, expected=0):
        result = subprocess.run(
            [sys.executable, "-m", "tython_cli", *args],
            cwd=self.root,
            env=self.env,
            text=True,
            capture_output=True,
            timeout=120,
        )
        self.assertEqual(result.returncode, expected, result.stdout + result.stderr)
        return result

    def test_default_and_custom_destinations_without_compiler(self):
        self.assertIn("demo", self.run_cli("--help").stdout)
        self.assertIn(
            "must not already exist",
            " ".join(self.run_cli("demo", "--help").stdout.split()),
        )
        for args, relative in [
            ((), "tython-demo"),
            (("nested/a playground",), "nested/a playground"),
            (("--", "--demo"), "--demo"),
        ]:
            with self.subTest(destination=relative):
                self.run_cli("demo", *args)
                self.assertEqual(
                    files_under(self.root / relative), files_under(PREVIEW)
                )
                self.assertFalse((self.root / relative / "dist").exists())

    def test_existing_destinations_are_untouched(self):
        for kind in ("file", "empty", "project"):
            destination = self.root / kind
            if kind == "file":
                destination.write_text("user data")
            else:
                destination.mkdir()
                if kind == "project":
                    (destination / "01_shapes.ty").write_text("user edits")
            before = (
                files_under(destination)
                if destination.is_dir()
                else destination.read_bytes()
            )
            result = self.run_cli("demo", str(destination), expected=2)
            self.assertIn("could not create demo", result.stderr)
            after = (
                files_under(destination)
                if destination.is_dir()
                else destination.read_bytes()
            )
            self.assertEqual(after, before)

    def test_symlink_destination_is_untouched(self):
        target = self.root / "target"
        target.mkdir()
        (target / "notes.txt").write_text("keep")
        link = self.root / "link"
        try:
            link.symlink_to(target, target_is_directory=True)
        except OSError:
            self.skipTest("Creating symlinks is unavailable")
        self.run_cli("demo", str(link), expected=2)
        self.assertEqual(files_under(target), {Path("notes.txt"): b"keep"})
        self.assertTrue(link.is_symlink())

    @unittest.skipUnless(
        os.environ.get("TYTHON_TEST_COMPILER"),
        "Set TYTHON_TEST_COMPILER to run the tour",
    )
    def test_generated_tour_checks_builds_and_runs(self):
        binary = self.package / "bin" / ("tython.exe" if os.name == "nt" else "tython")
        binary.parent.mkdir()
        shutil.copy2(os.environ["TYTHON_TEST_COMPILER"], binary)
        self.run_cli("demo", "playground")
        demo = self.root / "playground"
        for args in [("--check",), ()]:
            result = subprocess.run(
                [sys.executable, str(demo / "tour.py"), *args],
                cwd=self.root,
                env=self.env,
                text=True,
                capture_output=True,
                timeout=120,
            )
            self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
            if args:
                self.assertFalse((demo / "dist").exists())
            else:
                for lesson in demo.glob("[0-9][0-9]_*.ty"):
                    self.assertIn(f"--- {lesson.name} ---", result.stdout)
                self.assertIn("workshop visitor: Kate", result.stdout)
                self.assertIn("Visitors Ada", result.stdout)
                self.assertIn("ADA Not text HELLO here 1 2", result.stdout)
        self.run_cli("check", str(demo / "negative/expected_errors.ty"), expected=1)
        self.assertEqual(
            files_under(PREVIEW),
            {
                name: data
                for name, data in files_under(demo).items()
                if name.parts[0] != "dist"
            },
        )


if __name__ == "__main__":
    unittest.main()
