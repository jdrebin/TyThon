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
        # The launcher is pure Python, but the payload is Linux x64 native code.
        # Do not claim manylinux compatibility without an auditwheel validation.
        return "py3", "none", "linux_x86_64"


info = json.loads((Path(__file__).parent / "tython_cli/build-info.json").read_text())
if info["target"] != "linux-x64":
    raise RuntimeError("Only the verified Linux x64 target can be packaged.")
setup(
    version=info["version"],
    distclass=BinaryDistribution,
    cmdclass={"bdist_wheel": BinaryWheel},
)
