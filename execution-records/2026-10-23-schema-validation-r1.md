# Schema validation run r1 — 30 fixtures, Draft 2020-12

**State:** Compiled/tested (Python validator over JSON Schema fixtures; platform-independent)
**Commit:** 6e82cdf (batch closing P0 scope review: ADR-013 v1-scope acceptance, schema README sync, resume/shell scoping)
**Date/time:** 2026-10-23, Europe/Berlin (UTC+2)
**Host:** Windows 11 Pro 10.0.26200, Python from `.venv-schema`

## Command

```
.venv-schema/Scripts/python.exe -I -B schemas/validate.py
```

## Exit code

```
0
```

## Output

```
checked 30 fixture(s) across 8 schema(s)
all fixtures behaved as expected
```

## What this proves

The 8 machine-checkable contracts (forecast actionability, pressure-state, attribution, classification, decision closed enum, action-result, state-file, common) hold against all 30 valid+invalid fixtures, including the fail-closed cases (missing dependency → exit 2, zero fixtures → exit 3).

## What this does NOT prove

- That any Go serializer matches these schemas (second half of G3 — needs the Go source tree, which does not exist yet).
- That SCN-007..011 or IT-11 pass (they are **Designed**, not run — no harness or implementation exists).
- Anything about macOS pressure behavior (this run is host-independent; platform verification is a separate gate).
- That `Sample`/`Incident`/`Advisory`/`Episode` are contracted (still unschematised, G3 scope open).
