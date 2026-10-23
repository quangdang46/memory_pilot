# Execution record — Phase 2 algorithms (2026-10-23)

- **Estimator (memory_pilot-dya)**: `internal/pressure` — kernel-primary with
  precedence (trend elevates Normal→Watch only), fallback with provenance=
  derived, Unknown fail-safe, hysteresis + 15s dwell (Critical immediate),
  no-recovery-on-degraded (SI-4), monotone-in-incident, weighted score
  (0.50/0.20/0.15/0.10/0.05). Tests cover T-PE-1..7.
- **Rate estimator (memory_pilot-daq)**: `internal/forecast` — EWMA with
  adaptive alpha (base 0.30, burst damping 1/(2b+1), clamped 0.10..0.75),
  acceleration from smoothed rate (varies-cadence-safe).
- **Forecast (memory_pilot-us1)**: quadratic TTE with all sbh guard rails
  (d≤0⇒0, v≤0⇒none, disc<0⇒none, conjugate form for a<0, unphysical
  rejection) — unit-tested incl. the near-cancellation precision case.
- **Conformal (memory_pilot-0kv)**: ONE-SIDED lower bound, k>n⇒q=1.0 fail-safe
  (lo=0), warmup returns point but marked untrustworthy, censored resolution
  at horizon, discard-on-recovery hook, ACI alpha update, only-the-bound-acts.
- **Guardrail (memory_pilot-a0b)**: directional e-process (only under-
  prediction penalized; burst outliers are not miscalibration), clamps
  -5.0/+3.5 for responsiveness, threshold e=20; PredictionScorecard with
  FAR excluding Intervened.
- **Replay harness (memory_pilot-nve)**: `internal/replay` + `bin/mpreplay -all`
  — no wall clock, deterministic ordering, sha256 per trace; regeneration
  via `bin/mpcorpus` verified BIT-IDENTICAL (diff of two runs, plus manifest
  hash match).
- **Trace corpus (memory_pilot-aq9)**: traces/ 12 traces v1 incl.
  escalation-full, all-missing, flap-synthetic, quiet-idle; manifest.json
  with hashes.
- **Forecast output contract (memory_pilot-78q)**: actionable =
  !fallback ∧ !guardAlarm ∧ (calibrated ⇒ bound>0) ∧ conf≥0.5 (schema rule:
  actionable ⇒ bound ∧ calibrated ∧ !fallback — enforced in Out.Actionable).

All tests green: go test ./... (pressure, forecast, ledger, model, store).
