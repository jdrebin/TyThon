"""JSON adapter for public Jedi APIs and Ruff's CLI; no Typed-Python semantics."""

import ast
import json
import os
from pathlib import Path
import subprocess
import sys


def utf16(text):
    return len(text.encode("utf-16-le")) // 2


def column_from_utf16(text, column):
    return len(text.encode("utf-16-le")[: column * 2].decode("utf-16-le"))


def point(source, location):
    lines = source.splitlines(keepends=True)
    row = location["row"] - 1
    # Ruff JSON columns are 1-based Unicode codepoints, not UTF-16.
    return (
        utf16("".join(lines[:row])) + utf16(lines[row][: location["column"] - 1])
        if row < len(lines)
        else utf16(source)
    )


def ruff(request, command, extra):
    result = subprocess.run(
        [
            sys.executable,
            "-m",
            "ruff",
            command,
            "--no-cache",
            *extra,
            "--stdin-filename",
            request["path"],
            "-",
        ],
        input=request["source"],
        text=True,
        capture_output=True,
        cwd=request["root"],
        timeout=8,
    )
    if result.returncode not in (0, 1) or (command == "format" and result.returncode):
        raise RuntimeError(result.stderr.strip() or "Ruff failed")
    return result.stdout


def handle(request):
    method = request["method"]
    if method == "declarations":
        from python_declarations import convert
        import tokenize

        if request.get("sourcePath"):
            if os.path.getsize(request["sourcePath"]) > 1_000_000:
                raise ValueError("Python declaration input exceeds 1 MB")
            with tokenize.open(request["sourcePath"]) as file:
                source = file.read(1_000_001)
        else:
            source = request["source"]
        if len(source) > 1_000_000:
            raise ValueError("Python declaration input exceeds 1 MB")
        return convert(source)
    source = request["source"]
    if method == "lint":
        if request.get("erased"):
            try:
                ast.parse(source)
            except SyntaxError:
                return []  # Projection artifacts are not Python syntax errors.
        args = ["--output-format", "json", "--no-fix"]
        if request.get("erased"):
            # Only expression-local rules unaffected by erased declarations.
            # In particular, don't report unused imports/variables used by types.
            args += ["--select", "F601,F602,F631,F632,F634,F701,F702,F704,F706,F707"]
        diagnostics = json.loads(ruff(request, "check", args))
        output = []
        for diagnostic in diagnostics:
            if request.get("erased") and diagnostic["code"] not in {
                "F601",
                "F602",
                "F631",
                "F632",
                "F634",
                "F701",
                "F702",
                "F704",
                "F706",
                "F707",
            }:
                continue
            fix = diagnostic.get("fix")
            edits = []
            if fix and fix.get("applicability") == "safe":
                edits = [
                    {
                        "start": point(source, edit["location"]),
                        "end": point(source, edit["end_location"]),
                        "text": edit["content"],
                    }
                    for edit in fix["edits"]
                ]
            output.append(
                {
                    "start": point(source, diagnostic["location"]),
                    "end": point(source, diagnostic["end_location"]),
                    "message": diagnostic["message"],
                    "code": diagnostic["code"],
                    "edits": edits,
                    "fixTitle": fix.get("message") if fix else None,
                }
            )
        return output

    import jedi

    if request.get("cachePath"):
        jedi.settings.cache_directory = request["cachePath"]
    else:
        jedi.settings.use_filesystem_cache = False

    project = jedi.Project(
        request["root"],
        environment_path=request.get("interpreter") or None,
        load_unsafe_extensions=False,
    )
    script = jedi.Script(source, path=request["path"], project=project)
    line = request["line"]
    lines = source.split("\n")
    column = column_from_utf16(lines[line], request["character"])
    if method == "complete":
        if request.get("moduleOnly"):
            dot = lines[line].rfind(".", 0, column)
            if dot < 0 or not any(
                name.type == "module" for name in script.infer(line + 1, dot)
            ):
                return []
        # Return only names supplied by an external module. Never compete with
        # the native checker's interpretation of erased local declarations.
        return [
            {
                "label": name.name,
                "kind": name.type,
                "prefixLength": utf16(name.name[: len(name.name) - len(name.complete)]),
            }
            for name in script.complete(line + 1, column)
            if name.module_path and Path(name.module_path) != Path(request["path"])
        ][:200]
    names = script.goto(
        line + 1, column, follow_imports=True, follow_builtin_imports=True
    )
    names = [
        name
        for name in names
        if name.module_path and Path(name.module_path) != Path(request["path"])
    ]
    if method == "docs":
        return "\n\n".join(dict.fromkeys(name.docstring(raw=True) for name in names))[
            :16000
        ]
    if method == "definition":
        result = []
        for name in names:
            if name.line is None or name.column is None:
                continue
            # Jedi reads Python's encoding cookie, so use the same public
            # tokenizer helper for external files (including non-UTF8 sources).
            import tokenize

            with tokenize.open(name.module_path) as file:
                external_line = file.read().splitlines()[name.line - 1]
            result.append(
                {
                    "path": str(name.module_path),
                    "line": name.line - 1,
                    "character": utf16(external_line[: name.column]),
                }
            )
        return result
    raise ValueError("Unknown Python provider method")


if __name__ == "__main__":
    try:
        # Bound each short-lived helper and its Ruff/Jedi children on Unix.
        # VS Code also bounds concurrency, output size and wall-clock lifetime.
        if os.name == "posix":
            import resource

            def tighten(limit, value):
                # macOS rejects RLIMIT_AS outright, and a hard ceiling below the
                # requested value raises ValueError. Keep the helper running.
                try:
                    _soft, hard = resource.getrlimit(limit)
                    ceiling = value if hard == resource.RLIM_INFINITY else min(value, hard)
                    if ceiling <= 0:
                        return
                    kept = hard if hard != resource.RLIM_INFINITY else ceiling
                    resource.setrlimit(limit, (ceiling, kept))
                except (ValueError, OSError):
                    return

            tighten(resource.RLIMIT_AS, 768 * 1024 * 1024)
            tighten(resource.RLIMIT_CPU, 10)
        print(json.dumps({"result": handle(json.load(sys.stdin))}))
    except Exception as error:
        print(json.dumps({"error": str(error)}))
