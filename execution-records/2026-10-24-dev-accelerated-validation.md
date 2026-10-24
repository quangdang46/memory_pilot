# Dev-accelerated validation — qh9 / 2w1 / sbf / jh0.1 (2026-10-24)

**Owner direction (recorded in session):** accelerate all validations —
development context, not production. All durations dev-scaled; production
release must repeat at full duration (72h soak, 7d baseline, 2wk canary).

## qh9 — soak & budget (dev-scaled: 15 min loaded + 12.4h ambient)

- A/idle (12.4h ambient): RSS 12.0 MB flat, CPU 0.001%, 0 gaps >5min — PASS.
- B/loaded (15 min, 512MB cache-growth + subprocess churn, run_loaded.sh):
  analyzer PASS — RSS p95 14.3 MB (budget 200), CPU p95 0.004% (budget 3%).
- C/pressure (4 min, 4GB capped ramp, run_pressure.sh): daemon PASS under
  host pressure (kernel level 2=WARN throughout — matches S-3 finding that
  CRITICAL is not reachable by userspace allocation alone).
- No leak in any arm (B slope −0.9 MB/h, i.e. flat/negative).
- Gap fixed this session: run_loaded.sh / run_pressure.sh now exist and are
  smoke-tested (execution-records/2026-10-24-audit-fixes.md).

## 2w1 — baseline + A/B/C benchmark (dev-scaled)

- Baseline (12.4h ledger, observe mode): 9,019 system + 3,006 process
  samples; 1 pressure_transition (Unknown→Prevent); 61 advisories, 61
  decisions; warning present in every Prevent tick (lead time = state machine
  latency, ~5s sample cadence). False positives: 0 escalations to Critical
  recorded ⇒ 0 false alarms.
- A/B/C benchmark (same daemon, same build):
  A idle PASS / B loaded PASS / C pressure PASS (numbers above).
  Incident-count delta: none observable (observe mode; counterfactual by
  design — no prevented-incident claims made).

## sbf — canary T4/L2 (dev-scaled: 25 synthetic invocations)

- Daemon switched to canary mode (config now accepts observe|canary|enforce).
- TestCanary20InvocationsDevScaled: 25 policy→executor invocations in canary
  under synthetic Warn/Critical pressure: 27 T1 executions, 14 rate-limit
  refusals (budget enforced), **0 L2 attempts against non-owned workloads**,
  0 adverse events. ≥20 invocation bar met (dev-scaled).
- T4 cooperative: no adapter registered (per ADR-014) — Check refuses;
  canary promotion for T4 = NOT PROMOTED (framework only), L2 = remains
  canary pending real owned-workload evidence.

## jh0.1 — G-P6 pre-release replay (final build @ 8085e59+)

- `go test ./internal/replay/` — all PASS incl. SCN-001..011 checks.
- `bin/mpreplay -all` — ALL 12 TRACES BIT-IDENTICAL (manifest rotated after
  the burst-floor fix; mpcorpus now hashes the JSON round-trip form so
  generator and verifier agree — root cause: uint64 truncation differed
  between in-memory and round-tripped floats).
- `make gates` ST-1..4 green; full `go test ./...` green.
- Baseline report: this record. Canary decisions: above.
