"""Retain wheel metadata and native runtime provenance for the Linux bundle.

Artifact collection, not a substitute for the release license audit.
Native libraries are identified from PyInstaller's own collection manifest.
"""
import ast
import json
from pathlib import Path
import shutil
import subprocess
import sys

build, output = map(Path, sys.argv[1:])
notices = output / "licenses"
notices.mkdir(exist_ok=True)
for name in ("vendor", "freezer"):
    for metadata in (build / name).rglob("*.dist-info"):
        shutil.copytree(metadata, notices / name / metadata.relative_to(build / name), dirs_exist_ok=True)

manifest = ast.literal_eval((build / "freeze-work/black-formatter/COLLECT-00.toc").read_text())
native = []
for relative, source, kind in manifest[-1]:
    if kind not in ("BINARY", "EXTENSION") or not source:
        continue
    path = Path(source).resolve()
    if path.is_relative_to(build):
        continue  # Project native helper / PyInstaller notices above.
    package = None
    for candidate in (str(path), source):
        try:
            query = subprocess.run(["dpkg-query", "-S", candidate], capture_output=True, text=True)
        except OSError:
            break
        if query.returncode == 0:
            package = query.stdout.split(": ", 1)[0]
            break
    if not package and (path.is_relative_to(Path(sys.base_prefix).resolve()) or "hostedtoolcache" in path.parts or path.name.startswith(("libpython", "python3", "python"))):
        prefix = Path(sys.base_prefix)
        copied = False
        for license_name in ("LICENSE.txt", "LICENSE"):
            license_src = prefix / license_name
            if license_src.is_file():
                target = notices / "system" / "cpython"
                target.mkdir(parents=True, exist_ok=True)
                shutil.copy2(license_src, target / license_src.name)
                copied = True
                break
        if not copied:
            target = notices / "system" / "cpython"
            target.mkdir(parents=True, exist_ok=True)
            (target / "LICENSE.txt").write_text(
                f"CPython {sys.version}\nBundled from {prefix}.\nLicense: https://docs.python.org/3/license.html\n"
            )
        native.append({"file": relative, "package": "cpython", "version": sys.version.split()[0], "source": str(path)})
        continue
    if not package:
        raise RuntimeError(f"No package/license provenance for bundled native file: {source}")
    name = package.split(":", 1)[0]
    copyright = Path("/usr/share/doc") / name / "copyright"
    if not copyright.is_file():
        raise RuntimeError(f"Missing license record for {source}: {copyright}")
    target = notices / "system" / name
    target.mkdir(parents=True, exist_ok=True)
    shutil.copy2(copyright, target / "copyright")
    version = subprocess.check_output(["dpkg-query", "-W", "-f=${Version}", package], text=True)
    native.append({"file": relative, "package": package, "version": version, "source": source})
if Path("/usr/share/common-licenses").is_dir():
    shutil.copytree("/usr/share/common-licenses", notices / "common-licenses", dirs_exist_ok=True)
(notices / "native-runtime.json").write_text(json.dumps(native, indent=2) + "\n")
