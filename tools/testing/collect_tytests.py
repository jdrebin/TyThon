"""Execute transpiled test modules and serialize their collected assertions."""
import json
import inspect
import pathlib
import runpy
import sys

output, helper, *modules = sys.argv[1:]
sys.path.insert(0, str(pathlib.Path(helper).parent))
from tytest import TyTest, _cases

# Emission preserves lines, so failures can navigate to the original assertion.
# Keep collection metadata out of the typed authoring API.
original_expect = TyTest._expect


def record_expectation(self, *args, **kwargs):
    result = original_expect(self, *args, **kwargs)
    frame = inspect.currentframe().f_back
    try:
        while frame and frame.f_code.co_filename == helper:
            frame = frame.f_back
        if frame:
            self._case["assertions"][-1]["location"] = {
                "file": frame.f_code.co_filename, "line": frame.f_lineno,
            }
    finally:
        del frame
    return result


TyTest._expect = record_expectation

collected = []
for module in modules:
    _cases.clear()
    sys.path.insert(0, str(pathlib.Path(module).parent))
    try:
        runpy.run_path(module, run_name="__tytest__")
    finally:
        sys.path.pop(0)
    if not _cases:
        raise ValueError(f"{module}: no tytest cases collected")
    for case in _cases:
        if case.get("extended") and not case["assertions"]:
            continue
        case["origin"] = module
        collected.append(case)
pathlib.Path(output).write_text(json.dumps(collected), encoding="utf-8")
