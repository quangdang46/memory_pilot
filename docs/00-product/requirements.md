# MP-SPEC-003 — Requirements

**Status:** Draft
**Version:** 0.1.0
**Legend:** Prio = M (must, v1) / S (should, v1 if cheap) / C (could, later) / W (won't, v1).
**Verification:** `T` = automated test, `M` = manual measurement on Mac mini M4, `I` = inspection (code/schema), `R` = replay of recorded trace.

Every requirement must be falsifiable. If it cannot be tested, it is not a requirement — it is an aspiration, and belongs in the charter.

---

## 1. Functional — Collection

| ID | Prio | Requirement | Acceptance | Ver |
|---|---|---|---|---|
| MP-REQ-001 | M | Collect system VM statistics: total RAM, page size, free/active/inactive/wired/speculative pages, compressor pages, throttled pages. | Values match `vm_stat`/`host_statistics64` for the same instant within 1 page. | T, M |
| MP-REQ-002 | M | Collect swap: total, used, free, via `sysctl vm.swapusage`. | Matches `sysctl vm.swapusage` string parse to the reported precision. | T |
| MP-REQ-003 | M | Collect the kernel memory-pressure level via `sysctl kern.memorystatus_vm_pressure_level`, decoding XNU dispatch masks 1/2/4 → normal/warn/critical. | Unit test on recorded values 0,1,2,4,other. | T |
| MP-REQ-004 | M | Subscribe to `DISPATCH_SOURCE_TYPE_MEMORYPRESSURE` events (normal/warn/critical) so state changes are event-driven, not poll-only. | Event arrives < 500 ms after kernel level change in a synthetic test; fallback to polling is logged. | M |
| MP-REQ-005 | M | If the native event source is unavailable, fall back to polling without loss of correctness (no false "normal"). | Subscription failure path exercised by fault injection. | T |
| MP-REQ-006 | M | Enumerate processes with: pid, ppid, name, executable path, start time (seconds+micros from `pbi_start_tvsec/tvusec`), virtual size, resident size, user/system CPU. | Populated for the invoking user's processes; documented as `denied` for others. | T, M |
| MP-REQ-007 | M | Collect disk headroom for the boot volume (free bytes, total bytes). | Matches `df`/`statfs` for the same instant. | T |
| MP-REQ-008 | S | Collect per-process disk I/O via `proc_pid_rusage` (RUSAGE_INFO_V4). | Available for own-user processes; absent otherwise. | T |
| MP-REQ-009 | M | Every metric carries: value, unit, timestamp (monotonic + wall), source (api/command/derived), and quality (`ok`/`degraded`/`unavailable` + reason). | Schema validation on every emitted sample. | T, I |
| MP-REQ-010 | M | Sample cadence adapts to pressure: base interval when normal, shortened when warn, shortest at critical; each cadence has a floor to prevent self-induced load. | Recorded sampling intervals match the schedule within ±10%. | T, R |
| MP-REQ-011 | M | Missing metrics are reported as `unavailable`, never defaulted, never interpolated to `normal`. | Fault injection for each source. | T |

## 2. Functional — Pressure model & forecasting

| ID | Prio | Requirement | Acceptance | Ver |
|---|---|---|---|---|
| MP-REQ-020 | M | Compute a normalized pressure state from {kernel level, free pages %, compressor %, swap rate, disk headroom} with an explicit precedence rule: kernel level wins when available; VM-signal inference is the documented fallback only. | Table test covering precedence combinations. | T |
| MP-REQ-021 | M | Compute memory-consumption rate (bytes/s) with an adaptive EWMA, plus acceleration. | On synthetic ramps, estimated rate within 10% after warmup; documented warmup length. | T, R |
| MP-REQ-022 | M | Forecast time-to-critical with a confidence value and an explicit `insufficient_data` state. | On replay traces, MAE and coverage reported; no forecast emitted before `min_samples`. | T, R |
| MP-REQ-023 | M | Burst detection: distinguish transient step-changes from sustained trends, using a robust median/MAD statistic over rate history. | Synthetic burst vs. ramp traces classified correctly ≥ 90%. | T, R |
| MP-REQ-024 | M | Calibrate forecast bounds (conformal-style) and publish empirical coverage; automatically widen bounds when observed coverage falls below target. | Coverage reported per rolling window; widening triggered in a synthetic degraded scenario. | T, R |
| MP-REQ-025 | M | Never emit a forecast without (a) confidence, (b) sample count, (c) the horizon it applies to. | Schema-required fields; test asserts rejection when absent. | T, I |
| MP-REQ-026 | S | Conformal guardrail: detect that the rate estimator has drifted and raise a `forecast_degraded` signal rather than acting on a stale model. | Synthetic drift trace triggers the signal within N windows. | T, R |

## 3. Functional — Attribution

| ID | Prio | Requirement | Acceptance | Ver |
|---|---|---|---|---|
| MP-REQ-030 | M | Compute per-PID footprint deltas over configurable windows and rank contributors by *delta*, with absolute value shown secondarily. | Ranking is delta-ordered; a large flat process never outranks a small grower. | T |
| MP-REQ-031 | M | Group processes into workloads by parent/child lineage plus executable/name heuristics; group lineage is explained in output. | Grouping stable across restarts; grouping rationale present per group. | T, I |
| MP-REQ-032 | M | Report coverage: how many processes were visible vs. estimated total, and what confidence the attribution carries as a result. | `coverage` field present in every attribution result. | T, I |
| MP-REQ-033 | M | Bucket growth into: `workload_growth`, `cache_or_retrievable`, `subprocess_accumulation`, `leak_suspicion`, `unknown` — each with evidence and confidence. | Schema forbids emitting an unqualified "leak". | T, I |
| MP-REQ-034 | M | Provide alternative explanations for any `leak_suspicion` (at minimum: workload change, cache warming, incomplete visibility). | Present in output for every leak-suspicion result. | T, I |
| MP-REQ-035 | S | Detect repeated growth patterns across sessions (same workload regrows after the same action) to strengthen or weaken suspicion. | Repeat-pattern signal recorded; changes confidence monotonically. | T, R |
| MP-REQ-036 | M | Attribute to groups, not just PIDs, so a browser/agent helper swarm is not reported as unrelated processes. | Scenario MP-SCN-002 replay shows helpers grouped. | R |

## 4. Functional — Policy, safety, relief

| ID | Prio | Requirement | Acceptance | Ver |
|---|---|---|---|---|
| MP-REQ-040 | M | The product contains **no** capability to signal, suspend, freeze, or terminate a process it did not spawn. | Static check: no forbidden syscalls/bindings reachable from action packages; safety tests assert absence. | T, I |
| MP-REQ-041 | M | Relief actions come from a closed allowlist; the executor accepts only allowlisted action types (type-level, not stringly-typed). | Compile-time closed enum; unknown action impossible by construction. | I, T |
| MP-REQ-042 | M | Each action has: preconditions, expected effect range, verification window, and an invalidation rule. Registered declaratively. | Every registered provider passes a contract test with all fields required. | T |
| MP-REQ-043 | M | Every executed action is verified by measurement; unverified actions are recorded as `unverified`, not `success`. | Verification test with a no-op provider yields `ineffective`. | T |
| MP-REQ-044 | M | Action rate limits: per-action min interval, per-hour cap, and exponential backoff after ineffectiveness; a circuit breaker stops a class after repeated failures. | Rate-limit tests; breaker test trips at threshold. | T |
| MP-REQ-045 | M | Policy modes: `observe` (default), `canary` (limited actions), `enforce` (all allowlisted). Mode transitions require explicit promotion and persist. | State-machine tests for all legal/illegal transitions. | T |
| MP-REQ-046 | M | Automatic demotion to a safe mode on: repeated action failure, forecast degradation, internal fault, or ledger write failure. | Fault injection triggers demotion; demotion is logged with reason. | T |
| MP-REQ-047 | M | User policy: protect a workload group, disable an action, set conservatism level. Persisted, validated, and shown by `policy status`. | Config round-trip + rejection of invalid values. | T |
| MP-REQ-048 | M | When no safe action exists at CRITICAL, emit an advisory with contributors and options; do not act. | Scenario MP-SCN-105 test. | T |
| MP-REQ-049 | M | Single-writer guarantee: a second daemon instance refuses to start (lock + socket check), with an actionable message. | Concurrency test. | T |
| MP-REQ-050 | M | Baseline measurement harness: run the daemon in observe mode over a real 7-day workload and emit a report with incident count, warning lead time, and false-positive rate. | Report produced; numbers compared to the pre-install baseline period. | M |
| MP-REQ-051 | M | The v1 action set is exactly `v1-scope-contract.md` §2 (T1 yes, T2 `DeferLaunch` yes, T3 detect+recommend yes / act no code path, T4 empty, T5 advisories yes); `decision.schema.json`'s action enum matches this table and no other process-targeting action exists in the schema. | Inspection: enum members ⊆ contract table; any process-targeting addition fails review until the contract's §5 widening procedure (ADR amendment + safety suite + canary evidence) is complete. | I |

## 5. Functional — Ledger, reporting, CLI

| ID | Prio | Requirement | Acceptance | Ver |
|---|---|---|---|---|
| MP-REQ-060 | M | Append-only ledger with: samples (aggregated, not raw), events, forecasts, decisions, actions, outcomes. Versioned schema (`mempilot.ledger.v1`). | Schema validation; version field present; forward-compat reader rejects unknown major. | T, I |
| MP-REQ-061 | M | Retention: bounded by size and age, rotated, never unbounded; rotation never deletes the current segment. | Rotation test at small thresholds. | T |
| MP-REQ-062 | M | `mempilot status` — current pressure, trend, top contributors, mode, daemon health, budget usage. Human and `--json`. | Snapshot test both formats. | T |
| MP-REQ-063 | M | `mempilot report --last <dur>` — growth attribution over a window, grouped, with explanations. | Scenario MP-SCN-005 test. | T |
| MP-REQ-064 | M | `mempilot explain --last|--at <ts>|--id <id>` — full causal narrative for a decision or incident. | Golden output tests on recorded ledger. | T |
| MP-REQ-065 | M | `mempilot doctor` — checks: LaunchAgent installed & loaded, socket live, ledger writable, clock sane, permissions adequate, version match; actionable output. | Test harness with each condition broken. | T |
| MP-REQ-066 | M | `mempilot policy status|set` — inspect and modify policy safely. | Round-trip and validation tests. | T |
| MP-REQ-067 | S | `mempilot forecast` — standalone forecast with confidence & horizon. | Output schema test. | T |
| MP-REQ-068 | S | Structured output everywhere: `--json` on all read commands; stable field names; documented exit codes. | Exit-code and schema tests. | T, I |
| MP-REQ-069 | M | CLI-daemon IPC over a user-owned Unix domain socket with peer-credential checks; no TCP port. | Socket permission test; connection from wrong UID rejected. | T |

## 6. Non-functional

| ID | Prio | Requirement | Acceptance | Ver |
|---|---|---|---|---|
| MP-REQ-070 | M | Idle footprint: RSS ≤ 60 MB, CPU ≤ 0.5% of one core, ≤ 1 write/s average. | Measured over 1 hour idle. | M |
| MP-REQ-071 | M | Loaded footprint: RSS ≤ 200 MB, CPU ≤ 3% average, log ≤ 50 MB/day at default cadence. | Measured over 8 hours with real workload. | M |
| MP-REQ-072 | M | Under host pressure the daemon *reduces* work: longer intervals, less detail, capped logging. | Measured during a synthetic pressure event; counters show reduction. | T, M |
| MP-REQ-073 | M | Cold start to first usable `status` in < 2 s; daemon ready (subscribed + first sample) in < 5 s. | Startup timing test. | T |
| MP-REQ-074 | M | Survive 72 h continuous operation without RSS growth trend (slope indistinguishable from zero at 95%). | Soak test with regression analysis. | M |
| MP-REQ-075 | M | Crash-only design: any crash is recoverable by restart; no state corruption in ledger; partial line tolerated on read. | Kill -9 at random points during writes; ledger reads cleanly with one truncated record dropped. | T |
| MP-REQ-076 | M | No network I/O. Ever, in v1. | No `net` dial/listen in binary; inspection + test. | I, T |
| MP-REQ-077 | M | Config: TOML, strict (unknown keys reported), validated with actionable errors; defaults documented and versioned. | Config validation tests incl. unknown-key rejection. | T |
| MP-REQ-078 | M | Update safety: version change detected on start → schema migration or refuse with explanation; never silently drop ledger data. | Migration test old→new; refusal test for incompatible major. | T |
| MP-REQ-079 | S | Reproducible builds; single static binary; no cgo unless a required API forces it (documented per use). | Build inspection. | I |
| MP-REQ-080 | M | Operates as a user LaunchAgent only; no root requirement for any v1 feature. | Install/run as non-admin user; all features functional. | M |

## 7. Requirements that are explicitly *not* in v1

| ID | Won't | Rationale |
|---|---|---|
| MP-REQ-W01 | Any action on another app's memory | NG-1, NG-4 |
| MP-REQ-W02 | Suppressing the OOM dialog | NG-3 |
| MP-REQ-W03 | Root helper / privileged actions | NG-7 |
| MP-REQ-W04 | Network telemetry | NG-12 |
| MP-REQ-W05 | Linux/Windows collectors | NG-11 |
| MP-REQ-W06 | GUI | NG-9 |

---

## 8. Traceability

Every requirement above must appear in `../01-research/traceability-matrix.md` with: its origin (user need / reference project / Apple doc / design decision), the module that implements it, and the test(s) that verify it. A requirement without a test is a bug in the spec.
