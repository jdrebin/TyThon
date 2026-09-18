"""Thin launcher for the same compiler shipped in the tython VS Code extension."""

import argparse
import json
import os
from pathlib import Path


def main():
    package = Path(__file__).resolve().parent
    info = json.loads((package / "build-info.json").read_text(encoding="utf-8"))
    parser = argparse.ArgumentParser(
        prog="tython", description="Check typed Python or erase it to ordinary Python."
    )
    parser.add_argument(
        "--version",
        action="version",
        version=f"tython {info['version']} ({info['buildID']})",
    )
    commands = parser.add_subparsers(dest="command", required=True)
    for name, help_text in [
        ("check", "Type-check files without writing output."),
        (
            "build",
            "Check and emit Python into dist/; existing generated outputs are overwritten.",
        ),
    ]:
        command = commands.add_parser(name, help=help_text, description=help_text)
        command.add_argument(
            "--root-dir",
            help="Source/import root (default: inferred common source directory)",
        )
        if name == "build":
            command.add_argument(
                "--out-dir",
                default="dist",
                help="Separate output directory (default: dist)",
            )
        command.add_argument(
            "files", nargs="+", metavar="FILE", help=".ty or .d.ty input file"
        )
    args = parser.parse_args()
    executable = package / "bin/typed-python"
    # Absolute paths also prevent a filename beginning with '-' from being
    # interpreted as a compiler flag. Keep cwd intact for import resolution.
    files = [str(Path(file).absolute()) for file in args.files]
    options = [f"--root-dir={Path(args.root_dir).absolute()}"] if args.root_dir else []
    if args.command == "build":
        options.extend(["--emit", f"--out-dir={Path(args.out_dir).absolute()}"])
    try:
        os.execv(str(executable), [str(executable), "--python", *options, *files])
    except OSError as error:
        parser.exit(2, f"tython: could not start the bundled compiler: {error}\n")
