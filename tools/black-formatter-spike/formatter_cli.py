"""Single-request formatter entrypoint. No user modules or plugins are loaded."""

import argparse
import os
import sys
import tempfile


def main():
    parser = argparse.ArgumentParser(description="tython's Black-based formatter")
    parser.add_argument("--version", action="store_true")
    parser.add_argument("--stdin-filename")
    parser.add_argument("input", nargs="?", choices=["-"], default="-")
    args = parser.parse_args()
    if args.version:
        print("tython formatter (Black 26.5.1)")
        return 0
    # No writes to the extension install, project, or global Black cache.
    with tempfile.TemporaryDirectory(prefix="tython-format-") as cache:
        os.environ["BLACK_CACHE_DIR"] = cache
        from adapter import black, format_source
        from black.files import find_pyproject_toml, parse_pyproject_toml

        try:
            raw = sys.stdin.buffer.read(1_000_001)
            if len(raw) > 1_000_000:
                raise ValueError("Formatting input exceeds 1 MB")
            # Editor transport is always UTF-8, regardless of a Python coding
            # cookie; preserve a BOM if present without mojibake.
            encoding = "utf-8-sig" if raw.startswith(b"\xef\xbb\xbf") else "utf-8"
            source, encoding, newline = black.decode_bytes(raw, mode=black.Mode(), encoding_overwrite=encoding)
            config_path = find_pyproject_toml((args.stdin_filename,)) if args.stdin_filename else None
            config = parse_pyproject_toml(config_path) if config_path else {}
            width = config.get("line_length", 88)
            if type(width) is not int or not 1 <= width <= 10000:
                raise ValueError("Black line-length must be an integer between 1 and 10000")
            skip_magic = config.get("skip_magic_trailing_comma", False)
            if type(skip_magic) is not bool:
                raise ValueError("Black skip-magic-trailing-comma must be a boolean")
            result = format_source(source, width, magic_trailing_comma=not skip_magic)
            # Print only after native type/runtime and Black stability checks.
            sys.stdout.buffer.write(result.replace("\n", newline).encode(encoding))
            return 0
        except Exception as error:
            print(f"Formatting refused; no edits applied: {error}", file=sys.stderr)
            return 1


if __name__ == "__main__":
    raise SystemExit(main())
