"""Install the actual wheel outside the checkout; use no source-tree imports."""

import base64
import csv
import hashlib
import io
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import zipfile


def digest(data):
    return hashlib.sha256(data).hexdigest()


def run(args, *, cwd, env, expected=0):
    result = subprocess.run(
        args, cwd=cwd, env=env, text=True, capture_output=True, timeout=120
    )
    assert result.returncode == expected, (
        args,
        result.returncode,
        result.stdout,
        result.stderr,
    )
    return result.stdout + result.stderr


def main():
    wheel, vsix = map(lambda p: Path(p).resolve(), sys.argv[1:])
    with zipfile.ZipFile(vsix) as archive:
        editor = json.loads(archive.read("extension/build-info.json"))
    with zipfile.ZipFile(wheel) as archive:
        names = archive.namelist()
        metadata = next(n for n in names if n.endswith(".dist-info/WHEEL"))
        assert "Root-Is-Purelib: false" in archive.read(metadata).decode()
        assert "Tag: py3-none-linux_x86_64" in archive.read(metadata).decode()
        package_metadata = archive.read(
            metadata.removesuffix("WHEEL") + "METADATA"
        ).decode()
        assert "Name: tython-lang\n" in package_metadata
        assert f"Version: {editor['version']}\n" in package_metadata
        assert (
            "Requires-Dist:" not in package_metadata
        ), "The CLI must have no Python runtime dependencies"
        records = next(n for n in names if n.endswith(".dist-info/RECORD"))
        rows = list(csv.reader(io.StringIO(archive.read(records).decode())))
        assert {r[0] for r in rows} == set(names)
        for name, checksum, size in rows:
            if name == records:
                continue
            data = archive.read(name)
            assert int(size) == len(data)
            actual = (
                base64.urlsafe_b64encode(hashlib.sha256(data).digest())
                .rstrip(b"=")
                .decode()
            )
            assert checksum == "sha256=" + actual, name
        assert (
            digest(archive.read("tython_cli/bin/typed-python"))
            == editor["compilerSHA256"]
        )
        assert (
            digest(archive.read("tython_cli/library/builtins.d.ty"))
            == editor["librarySHA256"]
        )
        assert "tython_cli/licenses/go-toolchain/LICENSE" in names
        assert "tython_cli/licenses/fswatch/LICENSE" in names
        assert "tython_cli/NOTICE.txt" in names
        assert all("__pycache__" not in n for n in names)

    env = {
        k: v
        for k, v in os.environ.items()
        if k not in ("PYTHONPATH", "PYTHONHOME", "VIRTUAL_ENV")
    }
    env.update(GOMAXPROCS="2", GOMEMLIMIT="1024MiB", PIP_DISABLE_PIP_VERSION_CHECK="1")
    with tempfile.TemporaryDirectory(prefix="tython-wheel-") as folder:
        root = Path(folder)
        venv = root / "venv"
        run(
            [sys.executable, "-m", "venv", "--without-pip", str(venv)],
            cwd=root,
            env=env,
        )
        python = venv / "bin/python"
        run(
            [
                sys.executable,
                "-m",
                "pip",
                "--python",
                str(python),
                "install",
                "--no-index",
                "--no-deps",
                str(wheel),
            ],
            cwd=root,
            env=env,
        )
        env["PATH"] = str(venv / "bin") + ":/usr/bin:/bin"
        env["VIRTUAL_ENV"] = str(venv)
        cli = str(venv / "bin/tython")
        assert editor["buildID"] in run([cli, "--version"], cwd=root, env=env)
        assert "check" in run([cli, "--help"], cwd=root, env=env)
        assert "overwritten" in run([cli, "build", "--help"], cwd=root, env=env)
        assert editor["version"] in run(
            [str(python), "-m", "tython_cli", "--version"], cwd=root, env=env
        )
        location = run(
            [str(python), "-c", "import tython_cli; print(tython_cli.__file__)"],
            cwd=root,
            env=env,
        ).strip()
        assert Path(location).is_relative_to(venv)
        installed = Path(location).parent
        assert (
            digest((installed / "bin/typed-python").read_bytes())
            == editor["compilerSHA256"]
        )
        assert (
            digest((installed / "library/builtins.d.ty").read_bytes())
            == editor["librarySHA256"]
        )

        source = 'type User = {"name": str}\nuser: User = {"name": "Ada"}\nprint(user["name"])\n'
        (root / "app.ty").write_text(source)
        assert "no errors" in run([cli, "check", "app.ty"], cwd=root, env=env)
        assert not (root / "app.py").exists()
        assert "Emitted" in run([cli, "build", "app.ty"], cwd=root, env=env)
        assert not (root / "app.py").exists()
        assert run([str(python), "dist/app.py"], cwd=root, env=env).strip() == "Ada"
        run([cli, "check", "app.ty"], cwd=root, env=env)
        run([cli, "build", "app.ty"], cwd=root, env=env)
        output = (root / "dist/app.py").read_bytes()
        (root / "app.ty").write_text("value: str = 12\n")
        error = run([cli, "check", "app.ty"], cwd=root, env=env, expected=1)
        assert "str" in error and ("int" in error or "12" in error), error
        assert "cannot contain both" not in error, error
        run([cli, "build", "app.ty"], cwd=root, env=env, expected=1)
        assert (
            root / "dist/app.py"
        ).read_bytes() == output, "Failed checks must not overwrite output"
        (root / "app.ty").write_text("type Broken = {\n")
        run([cli, "check", "app.ty"], cwd=root, env=env, expected=1)
        run([cli, "check", "missing.ty"], cwd=root, env=env, expected=2)
        run([cli, "check"], cwd=root, env=env, expected=2)
        run([cli, "check", "--emit", "app.ty"], cwd=root, env=env, expected=2)

        (root / "helper.ty").write_text("def greet(name: str):\n    return name\n")
        (root / "app.ty").write_text('from helper import greet\nprint(greet("Ada"))\n')
        run([cli, "check", "app.ty"], cwd=root, env=env)
        run([cli, "build", "app.ty"], cwd=root, env=env)
        assert (root / "dist/helper.py").exists()
        assert not (root / "helper.py").exists()
        assert run([str(python), "dist/app.py"], cwd=root, env=env).strip() == "Ada"
        # Existing Python dependencies can carry adjacent declaration contracts.
        (root / "catalog.py").write_text(
            'def load_user(user_id):\n    return {"id": user_id, "name": "Ada"}\n'
        )
        (root / "catalog.d.ty").write_text(
            'declare def load_user(user_id: int) -> {"id": int, "name": str}: ...\n'
        )
        (root / "app.ty").write_text(
            'from catalog import load_user\nuser = load_user(1)\nprint(user["name"])\n'
        )
        run([cli, "check", "app.ty"], cwd=root, env=env)
        run([cli, "build", "app.ty"], cwd=root, env=env)
        assert run([str(python), "dist/app.py"], cwd=root, env=env).strip() == "Ada"
        assert (root / "dist/catalog.py").read_bytes() == (
            root / "catalog.py"
        ).read_bytes()
        assert not (root / "dist/catalog.d.ty").exists()
        # Real package execution from a different output root, with relative imports.
        (root / "src/pkg").mkdir(parents=True)
        (root / "src/pkg/__init__.py").write_text('print("init")\n')
        (root / "src/pkg/main.ty").write_text(
            'from .helper import greet\nprint(greet("Ada"))\n'
        )
        (root / "src/pkg/helper.ty").write_text(
            "def greet(name: str):\n    return name\n"
        )
        for _ in range(2):
            run(
                [
                    cli,
                    "build",
                    "--root-dir",
                    "src",
                    "--out-dir",
                    "output",
                    "src/pkg/main.ty",
                ],
                cwd=root,
                env=env,
            )
            run(
                [cli, "check", "--root-dir", "src", "src/pkg/main.ty"],
                cwd=root,
                env=env,
            )
            assert (
                run(
                    [str(python), "-m", "pkg.main"], cwd=root / "output", env=env
                ).strip()
                == "init\nAda"
            )
        run([cli, "build", "--out-dir", ".", "app.ty"], cwd=root, env=env, expected=1)
        assert not (root / "app.py").exists()
        for name in ("a file.ty", "--emit.ty"):
            (root / name).write_text("value: int = 1\n")
            run([cli, "check", "--", name], cwd=root, env=env)
            assert not (root / Path(name).with_suffix(".py")).exists()
    print(
        "Wheel passed: RECORDs/platform, fresh offline venv install, compiler/library identity, CLI checking/emission/errors/imports, executable Python output."
    )


if __name__ == "__main__":
    main()
