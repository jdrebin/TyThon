"""Create an editable copy of the examples shared with the VS Code extension."""

import shutil
from pathlib import Path


def create_demo(source: Path, destination: Path) -> Path:
    # copytree exclusively creates the destination, refusing even empty existing
    # directories and symlinks. Never merge into a user's project or overwrite it.
    shutil.copytree(source, destination)
    return destination.absolute()
