# Schema validation run r2 — ADR-014 rename + new actions/verdicts

**State:** Compiled/tested (Python validator over JSON Schema fixtures; platform-independent)
**Commit:** (recorded at commit time — ADR-014 narrow-intervention batch)
**Date/time:** 2026-10-23, Europe/Berlin (UTC+2)
**Host:** Windows 11 10.0.26200, Python from `.venv-schema`

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
checked 35 fixture(s) across 8 schema(s)
all fixtures behaved as expected
```

## What changed since r1 (30 fixtures)

- `RequestReclaimFromProvider` → `RequestCooperativeRelease` in `decision.schema.json`, `action-result.schema.json`, 4 fixtures, SCN-006 prose, `appendix/schemas.md`. One live-prose stale reference remains in ADR-006's historical paragraph — deliberate (it narrates what ADR-013 said at the time), and the retired-name note in `action-authorization.md` §1 guards it.
- `StopManagedWorkload` added to both action enums.
- `decision.schema.json`: new `no_action_reason` member `adapter_unsupported`.
- `action-result.schema.json`: new `refusal_reason` members `not_managed`, `not_preauthorized`, `adapter_missing`.
- 5 new fixtures: `decision-stop-managed` (valid), `action-result-refused-adapter-missing` (valid), `decision-suspend-action` / `decision-raw-signal-action` / `action-result-refused-wrong-reason` (invalid — each must be rejected).
- Design correction during this batch: `unsupported` was first added as a *verdict*, then moved to a *refusal reason* (`adapter_missing`) because a verdict describes a measurement outcome and an uncalled adapter produces no measurement. The wrong intermediate state never shipped.

## What this proves

The two enums, the three new refusal reasons, and the new no-action reason hold against all 35 fixtures, including the negative cases that prove `Suspend*` and raw `Kill`/`Signal` can never validate and that a free-text refusal reason cannot validate.

## What this does NOT prove

- That any Go code serializes to these shapes (no source tree exists — second half of G3).
- That ST-23/ST-24 pass (they are **Designed** in `safety-tests.md`; no harness exists).
- That T4/L2 do anything on macOS (no adapter, no executor, no machine run).
- That `Sample`/`Incident`/`Advisory`/`Episode` are contracted (still unschematised, G3 scope open).
