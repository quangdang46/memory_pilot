# Execution record — validation tooling for open calendar-bound beads (2026-10-24)

Built the remaining tooling the open beads (qh9, 2w1, jh0.1) need to close on
real multi-day data. The calendar runs themselves remain open — nothing here
fabricates their results.

- **qh9 (soak/budget)**: `bench/budget/analyze.py` — p50/p95/p99 of self RSS
  and CPU, least-squares RSS slope (MB/h leak detection), ledger continuity
  (max gap vs 5-min cadence floor), crash-tail tolerant JSONL parse,
  pass/fail vs MP-REQ-070/071 budgets (idle 60MB/0.5%, loaded 200MB/3%).
  Validated with a synthetic 500-record ledger + snapshots (all pass).
  `bench/budget/run_idle.sh` samples `mempilot status` every 60 s for 1 h
  and feeds the analyzer. `run_loaded.sh` / `run_pressure.sh` follow the
  same contract for the 8h/30min runs.
- **jh0.1 (G-P6 pre-release replay)**: `internal/replay/scenarios_test.go` —
  MP-SCN-001/002/003/004/006/011 acceptance checks over the corpus:
  SCN-001 gradual escalation (Watch before Prevent — required wiring the
  trend-risk signal into replay: 1 pp/min free-% drop ⇒ risk 1.0),
  SCN-002 burst detection, SCN-003/004 escalation+recovery, SCN-006
  not-always-NoAction, SCN-011 degraded evidence never fabricates recovery.
  SCN-005/007..010 are CLI-level scenarios already covered by the Phase 5
  golden/reporting tests.
- **Bug found and fixed during SCN-002**: sbh's burst-skip rule
  (`median < 1`) is unit-mismatched for byte/s headroom rates on an idle
  machine (median 0 disables detection permanently). Adapted: flat baseline
  (median < 1 KB/s) uses a 1 MB/s absolute floor. Documented in code.

All tests green; ST-1..4 gates pass.
