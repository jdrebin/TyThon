"""Check, build, and run the positive tour with the installed tython CLI."""

import argparse
from pathlib import Path
import shutil
import subprocess
import sys


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--check", action="store_true", help="Check without building or running"
    )
    args = parser.parse_args()
    root = Path(__file__).resolve().parent
    lessons = sorted(root.glob("[0-9][0-9]_*.ty"))
    command = "check" if args.check else "build"
    subprocess.run(
        [
            sys.executable,
            "-m",
            "tython_cli",
            command,
            "--root-dir",
            ".",
            *[lesson.name for lesson in lessons],
        ],
        cwd=root,
        check=True,
    )
    if args.check:
        return
    # TyThon emits typed imports, but does not copy ordinary Python dependencies.
    # Supply the two local dependencies used by the imports and validation labs.
    for dependency in ("catalog.py", "guards.py"):
        shutil.copyfile(root / dependency, root / "dist" / dependency)
    for lesson in lessons:
        print(f"\n--- {lesson.name} ---", flush=True)
        subprocess.run(
            [sys.executable, str(root / "dist" / lesson.with_suffix(".py").name)],
            check=True,
        )


if __name__ == "__main__":
    main()
