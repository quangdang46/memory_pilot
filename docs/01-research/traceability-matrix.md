# Traceability Matrix

**Status:** Draft — rows are added as requirements are implemented; a requirement absent here is unowned.
**Version:** 0.1.0

Three matrices: (A) requirement → module → test; (B) reference idea → classification → destination; (C) conversation claim → verification status.

---

## A. Requirements → module → test

| Req | Module | Test(s) | Status |
|---|---|---|---|
| MP-REQ-001 VM stats | `platform/macos`, `collector` | IT-1, T-PE-1 | proposed |
| MP-REQ-002 swap | `platform/macos` | IT-1 | proposed |
| MP-REQ-003 kernel level decode | `platform/macos` | unit (1/2/4/other), IT-3 | proposed |
| MP-REQ-004 dispatch events | `platform/macos` (cgo bridge) | IT-2 | proposed |
| MP-REQ-005 polling fallback | `platform/macos` | FI-12, IT-3 | proposed |
| MP-REQ-006 process inventory | `platform/macos` | IT-4 | proposed |
| MP-REQ-007 disk headroom | `platform/macos` | unit + IT-1 | proposed |
| MP-REQ-008 process I/O (opt) | `platform/macos` | IT-4 | proposed |
| MP-REQ-009 metric quality | `model`, `collector` | schema tests | proposed |
| MP-REQ-010 adaptive cadence | `collector` | ADR-004 tests, soak | proposed |
| MP-REQ-011 no defaults | all collectors | FI-1 | proposed |
| MP-REQ-020 pressure state precedence | `pressure` | T-PE-1..4 | proposed |
| MP-REQ-021 EWMA rate | `forecast` | T-FC-1..2 | proposed |
| MP-REQ-022 ETA + confidence | `forecast` | T-FC-1..6 | proposed |
| MP-REQ-023 burst detection | `forecast` | T-FC-7 | proposed |
| MP-REQ-024 conformal calibration | `forecast` | T-FC-8..9 | proposed |
| MP-REQ-025 no bare forecasts | `model` schema | ST-16 | proposed |
| MP-REQ-026 drift guardrail | `forecast` | T-FC-9 | proposed |
| MP-REQ-030 delta ranking | `attribution` | T-AT-2 | proposed |
| MP-REQ-031 grouping | `workload` | T-AT-5 | proposed |
| MP-REQ-032 coverage | `attribution` | T-AT-6 | proposed |
| MP-REQ-033 classification buckets | `leaksuspect` | T-LK-1..5 | proposed |
| MP-REQ-034 alternatives | `leaksuspect` | ST-16, T-LK-6 | proposed |
| MP-REQ-035 repeat episodes | `leaksuspect` | T-LK-8 | proposed |
| MP-REQ-036 group attribution | `workload` | SCN-002 B3 | proposed |
| MP-REQ-040 no signaling | whole binary | ST-1, ST-22 | proposed |
| MP-REQ-041 closed action type | `action` | ST-2 | proposed |
| MP-REQ-042 provider contract | `providers` | providertest | proposed |
| MP-REQ-043 verification | `verification` | ST-10 | proposed |
| MP-REQ-044 rate limits | `policy` | ST-8, T-AA-4..5 | proposed |
| MP-REQ-045 policy modes | `policy` | T-AA-1..7 | proposed |
| MP-REQ-046 auto-demotion | `policy`, `supervisor` | ST-9, FI-2 | proposed |
| MP-REQ-047 user policy | `config`, `policy` | T-PP-1..5 | proposed |
| MP-REQ-048 critical-with-no-action | `policy` | SCN-002 B6 | proposed |
| MP-REQ-049 single instance | `daemon` | ST-21 | proposed |
| MP-REQ-050 baseline harness | `reporting` | Phase 5 protocol | proposed |
| MP-REQ-060 ledger schema | `ledger` | schema tests | proposed |
| MP-REQ-061 retention | `ledger` | FI-4, retention test | proposed |
| MP-REQ-062 status | `cmd/mempilot` | snapshot | proposed |
| MP-REQ-063 report | `reporting` | SCN-005 | proposed |
| MP-REQ-064 explain | `reporting` | SCN-004 D2 | proposed |
| MP-REQ-065 doctor | `cmd/mempilot` | doctor matrix | proposed |
| MP-REQ-066 policy CLI | `cmd/mempilot` | round-trip | proposed |
| MP-REQ-067 forecast CLI | `cmd/mempilot` | schema | proposed |
| MP-REQ-068 JSON everywhere | `cmd/*` | schema suite | proposed |
| MP-REQ-069 IPC security | `ipc` | IT-7 | proposed |
| MP-REQ-070..072 budgets | `selfmonitor` | perf bench | proposed |
| MP-REQ-073 startup | `daemon` | timing test | proposed |
| MP-REQ-074 soak | — | 72 h soak | proposed |
| MP-REQ-075 crash-only | `daemon`, `ledger` | FI-3, FI-14 | proposed |
| MP-REQ-076 no network | build gate | ST-4 | proposed |
| MP-REQ-077 config | `config` | FI-7 | proposed |
| MP-REQ-078 migration | `ledger` | FI-8 | proposed |
| MP-REQ-079 build | build | inspection | proposed |
| MP-REQ-080 no root | `service` | IT-6 | proposed |

---

## B. Reference ideas → classification

| Source | Idea | Class | Destination |
|---|---|---|---|
| sbh | kernel pressure level via sysctl, masks 1/2/4 | **Adopt** | `macos-integration` §1.1, ADR-001 |
| sbh | dispatch source subscription + re-read on event | **Adopt** | `macos-integration` §1.2–1.3 |
| sbh | polling fallback with liveness handle | **Adopt** | ADR-001 |
| sbh | bounded-slice sleep to drain events | **Adopt** | `runtime-and-concurrency` |
| sbh | memory pressure as a matrix axis | **Adapt** | `pressure-estimator` §4, policy |
| sbh | derived pressure fallback thresholds (2/5/10) | **Adapt (do not inherit values)** | `pressure-estimator` §4.2, Q-3 |
| sbh | `host_statistics64` + explicit struct/count | **Adopt** | `macos-integration` §2 |
| sbh | `vm_stat` text parser as cross-check | **Adopt** | telemetry §2 |
| sbh | swap string parse | **Adopt** | telemetry §2 |
| sbh | process inventory via `PROC_PIDTASKALLINFO` | **Adopt** | telemetry §3 |
| sbh | silent filter of unreadable PIDs | **Reject** | replaced by coverage counting |
| sbh | EWMA with burst damping + smoothed acceleration | **Adopt** | forecasting §2.1–2.2 |
| sbh | quadratic projection with stable conjugate form | **Adopt** | forecasting §2.3 |
| sbh | confidence from sample/residual/jitter | **Adopt** | forecasting §2.4 |
| sbh | burst via median/MAD | **Adapt (time-based history)** | forecasting §3 |
| sbh | conformal one-sided lower bound (mechanism) | **Verified · Adopted** | forecasting §4.1 |
| sbh | conformal bound applied to RAM headroom (data behaviour) | **Investigate** | forecasting §4.1; ADR-005 replay gate |
| sbh | e-process drift guard | **Verified · Adopted** | forecasting §4.2 |
| sbh | PredictionScorecard (Intervened ≠ FalseAlarm) | **Verified · Adopted** | forecasting §4.2b |
| sbh | e-process drift alarm (verified update rule) | **Adopt (adapt ratio metric)** | forecasting §4.2 |
| sbh | PID controller | **Reject** | no continuous actuator on macOS |
| sbh | VOI scheduler | **Investigate** | sampling strategy analogue |
| sbh | ballast files | **Reject** | disk-only (ADR-011) |
| sbh | quarantine/undo | **Reject** | file deletion out of scope |
| sbh | dual SQLite+JSONL logger | **Adapt** | JSONL only v1 (ADR-003) |
| sbh | evidence ledger + explain (`--why-not`, `--replay`) | **Adopt** | CLI §1, persistence §2 |
| sbh | observe/canary/enforce with persisted mode | **Adopt** | action-authorization §5 |
| sbh | fail-conservative + library-only transparency | **Adopt** | charter P6 |
| sbh | generated constants table + staleness test | **Adopt** | later: docs tooling |
| sbh | launchd plist shape (KeepAlive Crashed) | **Adopt** | `macos-integration` §6 |
| sbh | empty entitlements, user scope | **Adopt** | §7 |
| pt | evidence over single-signal inference | **Adopt** | attribution, leak-suspicion |
| pt | target identity tuple + start-time anchor | **Adapt** | attribution §2 |
| pt | revalidation before execution | **Adopt** | action-authorization §4 |
| pt | protection before scoring | **Adapt** | process-protection |
| pt | expected-loss action selection | **Adapt (no kill actions)** | planner |
| pt | shadow mode + post-hoc accuracy | **Adopt** | action-authorization §5 |
| pt | lock file + TTL + holder identity | **Adopt** | failure-recovery, IPC |
| pt | redaction policy for exported data | **Adopt** | telemetry §6 |
| pt | versioned JSON schemas | **Adopt** | all output |
| pt | kill / suspend / freeze actions | **Reject** | ADR-012 |
| pt | Bayesian classifier as the decision core | **Reject for v1** | ADR-005; simpler confidence first |
| pt | Parquet/Arrow telemetry | **Reject for v1** | ADR-003 |

---

## C. Conversation claims → verification

| Claim | Status | Where |
|---|---|---|
| Claude Code issues #22427/#24658/#22188 | unverified | synthesis §5 |
| FrankenTerm / WezTerm 20-agent stall | unverified | synthesis §5 |
| Boost uses `memory_pressure` | unverified | synthesis §5; ADR-006 |
| Reference licenses GPL-2.0/GPL-3.0/Apache-2.0 | unverified | synthesis §5 |
| masks 1/2/4 for pressure level | **verified (sbh source)** | ADR-001 |
| `GOMEMLIMIT` bounds total memory | **partially wrong** | ADR-008 |
| PID controller needed | rejected | ADR-005 |
| dialog cannot be disabled by 3rd party | likely true, unverified | charter L3 |
| purge has a beneficial effect | unproven | ADR-006 |
