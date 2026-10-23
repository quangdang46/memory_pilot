# Schemas — the machine-checkable contract

JSON Schema Draft 2020-12. This directory, not `docs/appendix/schemas.md`, is the normative contract for the objects listed below. If prose and schema disagree, the schema wins and the prose is a bug.

## Files

| Schema | Object | Enforces |
|---|---|---|
| `common.schema.json` | shared `$defs` | Metric quality rule (SI-4), identity tuple, enums |
| `forecast.schema.json` | `Forecast` | **`actionable ⇒ bound exists, calibrated, not fallback`**; warming is never actionable |
| `pressure-state.schema.json` | `PressureState` | `unknown ⇒ !reliable ∧ reasons non-empty` |
| `attribution.schema.json` | `Attribution` | every group carries evidence; identity never a bare PID |
| `classification.schema.json` | `Classification` | `leak_suspicion ⇒ alternatives`; non-unknown ⇒ falsifiable |
| `decision.schema.json` | `Decision` | exactly one of `chosen`/`no_action_reason`; **closed action enum** (no raw Signal/Kill; see invariant 4) |
| `action-result.schema.json` | `ActionResult` | `effective ⇒ measured`; `refused ⇒ reason`; crash-window ⇒ unverified |
| `state-file.schema.json` | `StateFile` | heartbeat shape for `status`/`doctor` |

## The invariants that live here

The reason this directory exists is that several safety claims are only real if a machine rejects the violating value:

1. **A forecast can never be `actionable` without a calibrated lower bound.** Not policy, not a runtime check that can be forgotten — a schema violation.
2. **A warming bound is not a bound.** `calibration.state == "warming"` forces `actionable: false` even though a point estimate is displayed.
3. **A classification can never be a bare claim.** `leak_suspicion` without `alternative_explanations` does not validate.
4. **An action can never name an arbitrary process control operation.** The `action` enum is closed; raw `"Signal"` / `"Kill"` / `"Restart"` fail validation — the only process-targeting members are the named, gated `RequestGracefulQuit` and `TerminateVerifiedOrphan` (ADR-013). This is the schema-level expression of SI-1: a closed set the schema can validate, not an open capability. **Representable in the schema is not the same as reachable in the binary.** v1's executor has no code path for those two actions at all — ST-1/ST-2 (`safety-invariants.md` §4) scan the build for termination primitives and fail it if one appears. The schema lets the policy layer be designed and tested now; turning either action on requires writing executor code for the first time, under a separate ADR amendment with measured false-positive data (ADR-013 §Revisit triggers), not a flag flip on code that already exists.
5. **An action cannot report itself successful without a measurement.** `verdict: effective` requires `window_s` and `observed_effect_bytes`.
6. **Unreadable telemetry must say why.** A `Metric` with `quality != ok` and a null `reason` is invalid.
7. **A decision cannot be both an action and a no-action**, nor neither.

## Running the validator

```bash
# once
python -m venv .venv-schema && .venv-schema/Scripts/python.exe -m pip install jsonschema

# every time
.venv-schema/Scripts/python.exe -I -B schemas/validate.py
```

`-I` (isolated) prevents a planted module in the working directory from being imported; `-B` avoids writing bytecode. The validator exits nonzero on any fixture behaving unexpectedly, and prints *which* assertion caught it.

Current: **30 fixtures across 8 schemas, all behaving as expected.**

## Fixture naming

```
schemas/testdata/valid/<schema>-<case>.json
schemas/testdata/invalid/<schema>-<what-is-wrong>.json
```

An invalid fixture's filename states the rule it violates, so a failure message reads like a specification:
`forecast-warming-but-actionable.json` → caught by the warming invariant.

## Adding a schema

1. Write the schema with `$id` under `https://mempilot.local/schemas/`.
2. Add at least one valid and one invalid fixture per hard invariant.
3. Run the validator.
4. Add a Go serializer test against the same fixtures once code exists.

## Not yet schematised

`Sample` (system telemetry), `Incident`, `Advisory`, `Episode`. Deliberately deferred: their shapes depend on algorithms that are still `Proposed`, and a schema written now would be plausible-looking fiction that the implementation then diverges from. They are tracked in `docs/spec-completion-checklist.md` gate G3.
