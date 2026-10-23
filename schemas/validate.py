#!/usr/bin/env python3
"""Validate every schema against its valid/invalid fixtures.

Run:  python -I -B schemas/validate.py
Exit: 0 = all fixtures behaved as expected
      1 = at least one fixture behaved unexpectedly
      2 = validators could not run (missing dependency) -- NOT a pass
      3 = no fixtures were found -- NOT a pass

Deliberately dependency-free beyond `jsonschema`; no project code is imported.
Use `-I` (isolated mode) so a planted module in the CWD cannot be loaded.

A missing dependency or an empty fixture set must never exit 0. A check that
cannot run is not a check that passed; treating it as one is how a schema gate
silently stops gating.
"""
from __future__ import annotations

import json
import pathlib
import sys

try:
    from jsonschema import Draft202012Validator
    from referencing import Registry, Resource
except ImportError as exc:
    print(
        f"ERROR: cannot run schema validation -- {exc}\n"
        "       install the validator:  pip install jsonschema\n"
        "       this is NOT a pass; the schemas were not checked.",
        file=sys.stderr,
    )
    raise SystemExit(2)

ROOT = pathlib.Path(__file__).resolve().parent


def load(name: str) -> dict:
    return json.loads((ROOT / name).read_text(encoding="utf-8"))


def build_registry() -> Registry:
    resources = []
    for path in sorted(ROOT.glob("*.schema.json")):
        schema = json.loads(path.read_text(encoding="utf-8"))
        resources.append((schema["$id"], Resource.from_contents(schema)))
        # Also register by bare filename so `$ref: "common.schema.json#/..."` resolves.
        resources.append((path.name, Resource.from_contents(schema)))
    return Registry().with_resources(resources)


def main() -> int:
    registry = build_registry()
    failures: list[str] = []
    checked = 0

    for schema_path in sorted(ROOT.glob("*.schema.json")):
        name = schema_path.name.removesuffix(".schema.json")

        for sub in ("valid", "invalid"):
            fixture_dir = ROOT / "testdata" / sub
            for fixture in sorted(fixture_dir.glob(f"{name}*.json")):
                instance = json.loads(fixture.read_text(encoding="utf-8"))
                validator = Draft202012Validator(
                    load(schema_path.name), registry=registry
                )
                errors = sorted(validator.iter_errors(instance), key=lambda e: list(e.path))
                checked += 1

                if sub == "valid" and errors:
                    failures.append(
                        f"{fixture.name}: expected VALID but got {len(errors)} error(s): "
                        f"{errors[0].message} at {list(errors[0].path)}"
                    )
                elif sub == "invalid" and not errors:
                    failures.append(f"{fixture.name}: expected INVALID but schema accepted it")

    schema_count = len(list(ROOT.glob("*.schema.json")))
    print(f"checked {checked} fixture(s) across {schema_count} schema(s)")

    if checked == 0:
        print(
            "ERROR: no fixtures were checked. An empty run is NOT a pass.",
            file=sys.stderr,
        )
        return 3

    # Coverage check: a schema with no fixtures is an unverified contract.
    uncovered = [
        p.name.removesuffix(".schema.json")
        for p in sorted(ROOT.glob("*.schema.json"))
        if not any((ROOT / "testdata" / sub).glob(f"{p.stem.split('.')[0]}*.json")
                   for sub in ("valid", "invalid"))
        and p.name != "common.schema.json"
    ]
    if uncovered:
        print(f"WARNING: schemas with no fixtures: {', '.join(uncovered)}", file=sys.stderr)

    if failures:
        print(f"\nFAILURES ({len(failures)}):")
        for f in failures:
            print(f"  - {f}")
        return 1
    print("all fixtures behaved as expected")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
