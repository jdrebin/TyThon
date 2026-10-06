"""Platform tag for the bundled native executable; no compiler runs at install."""

import json
from pathlib import Path

from setuptools import Distribution, setup
from wheel.bdist_wheel import bdist_wheel


class BinaryDistribution(Distribution):
    def has_ext_modules(self):
        # Install the package in platlib: it contains an architecture-specific
        # executable, even though it does not import a CPython extension module.
        return True


class BinaryWheel(bdist_wheel):
    def finalize_options(self):
        super().finalize_options()
        self.root_is_pure = False

    def get_tag(self):
        # The launcher is pure Python. The payload is a native compiler.
        # Do not claim manylinux compatibility without an auditwheel validation.
        tags = {
            "linux-x64": "linux_x86_64",
            "linux-arm64": "linux_aarch64",
            "darwin-x64": "macosx_10_15_x86_64",
            "darwin-arm64": "macosx_11_0_arm64",
            "win32-x64": "win_amd64",
        }
        platform = tags.get(info["target"])
        if platform is None:
            raise RuntimeError(f"No wheel tag for target {info['target']}")
        return "py3", "none", platform


info = json.loads((Path(__file__).parent / "tython_cli/build-info.json").read_text())
setup(
    version=info["version"],
    distclass=BinaryDistribution,
    cmdclass={"bdist_wheel": BinaryWheel},
)
