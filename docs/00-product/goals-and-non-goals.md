# MP-SPEC-002 — Goals & Non-Goals

**Status:** Draft
**Version:** 0.1.0
**Last updated:** 2026-10-23

Each goal has an ID (G-x), a measurable outcome, and the module that owns it. Each non-goal has an ID (NG-x) and the reason it is excluded — so that later "small feature requests" can be checked against a written decision instead of re-argued.

---

## 1. Goals

### G-1 — Measure memory state correctly on macOS
Collect physical memory, free/active/inactive/wired/speculative pages, compressed memory, swap total/used, the kernel's own memory-pressure level, per-process footprints, and disk headroom — with explicit provenance and quality flags per metric.

**Outcome:** every metric in `../03-algorithms/telemetry-model.md` is collected, unit-checked against a reference tool (`vm_stat`, `sysctl vm.swapusage`, `memory_pressure`, Activity Monitor) within stated tolerance.
**Owner:** `internal/platform/macos` + `internal/collector`.
**Reference:** sbh `crates/sbh_mach` + `src/platform/macos/{pal,sys}.rs` (verified working macOS implementation).

### G-2 — Turn state into a pressure model and a forecast with confidence
Produce a normalized pressure state (not a raw percentage), a trend, and a forecast of time-to-dangerous, with an explicit confidence and an explicit "unavailable" state.

**Outcome:** replay of recorded traces shows the forecast beats a naive threshold baseline on lead time and false-alarm rate; calibration (coverage of predicted bounds) is reported, not assumed.
**Owner:** `internal/pressure`, `internal/forecast`.
**Reference:** sbh `src/monitor/{ewma,predictive,conformal,guardrails}.rs` (adapt from bytes-of-disk to bytes-of-RAM-pressure).

### G-3 — Attribute pressure growth to processes and workload groups
Given "memory grew N MB in the last interval", answer which processes/groups contributed, with evidence and confidence — using footprint deltas over time, parent/child grouping, and correlated events (session start, tool run, browser helper spawn).

**Outcome:** on a recorded incident, the top-3 attributed contributors overlap the true contributors (established by Instruments run) in ≥ the acceptance rate defined in `../05-testing/replay-and-simulation.md`.
**Owner:** `internal/attribution`, `internal/workload`.
**Reference:** pt evidence model + sbh snapshot-diff thinking; **not** pt's kill-oriented labels.

### G-4 — Distinguish growth types
Classify each observed growth pattern as: workload-driven, cache/retrievable, subprocess accumulation, or leak-suspicion-with-confidence. Never output "leak" as a fact.

**Outcome:** classification output carries `confidence` + `evidence[]` + `alternative_explanations[]` in every case; no output path exists that emits a bare leak claim (enforced by schema, see `../03-algorithms/leak-suspicion.md`).
**Owner:** `internal/leaksuspect`.

### G-5 — Relieve pressure only through verified-safe mechanisms
Implement a closed allowlist of relief actions, each with preconditions, expected effect, measurement, and rollback where possible. v1 allowlist is deliberately small (see `../04-safety/action-authorization.md`).

**Outcome:** each action has a documented measured effect on a real Mac; actions with no measured effect are removed from the allowlist rather than kept "just in case".
**Owner:** `internal/providers`, `internal/executor`, `internal/verification`.

### G-6 — Protect the user's work, structurally
The daemon has no code path that signals another user process. Enforced by: absent syscall surface, allowlist type-safety, and tests that assert the absence (`../05-testing/safety-tests.md`).

**Outcome:** a grep-level and test-level guarantee that `kill(2)`, `SIGSTOP`, `SIGTERM`, `proc_terminate`, or `launchctl kill` cannot be reached from any action code path. See SI-1..SI-3.
**Owner:** `internal/safety` (whole-codebase concern).

### G-7 — Explain everything, afterwards
Maintain a queryable ledger of observations, forecasts, considerations, decisions, actions, and outcomes. `mempilot explain` reconstructs any interval.

**Outcome:** for any incident timestamp, `mempilot explain --at <ts>` returns a complete causal narrative with the same numbers that were live at the time.
**Owner:** `internal/ledger`, `internal/reporting`.

### G-8 — Be a good citizen on a 16 GB machine
Self-monitor and self-limit: RSS budget, CPU duty-cycle budget, I/O budget, log-size budget, and graceful degradation when the host is under pressure.

**Outcome:** idle and loaded budgets met on Mac mini M4 16 GB (`../02-architecture/performance-budget.md`), including *during* a pressure event — the daemon must get quieter, not busier, when the host is stressed.
**Owner:** `internal/selfmonitor`, `internal/daemon`.

### G-9 — Be operable
LaunchAgent install/uninstall, `status`, `report`, `explain`, `doctor`, `policy`, log rotation, crash recovery, upgrade path.

**Outcome:** install → running in one command; `doctor` diagnoses the common failure modes (permissions, stale socket, stale state, version drift).
**Owner:** `internal/service`, `cmd/mempilot`.

### G-10 — Leave the door open for other platforms
All OS-specific code behind interfaces with build tags; no OS assumptions leaked into the pressure model, policy engine, ledger, or reporting.

**Outcome:** a Linux collector could be added without modifying `internal/{pressure,forecast,policy,ledger,reporting}`. Verified by a compile-time check that those packages import no `platform` package.
**Owner:** architecture constraint.

---

## 2. Non-Goals

| ID | Non-goal | Reason |
|---|---|---|
| NG-1 | Killing, pausing, freezing, suspending, or restarting user processes | Destroys work; contradicts the product's reason to exist (P2, SI-1) |
| NG-2 | "Free RAM" / cache purging to improve a displayed number | Does not reduce pressure; can hurt performance; misleading |
| NG-3 | Suppressing or hiding the macOS out-of-memory dialog | No supported API; hiding symptoms without fixing causes is dishonest (L3) |
| NG-4 | Transparently migrating another app's heap to SSD | Unsafe in user space; would require runtime cooperation (ADR-011) |
| NG-5 | Disk-ballast-style preallocation as a RAM strategy | A file on SSD is not RAM; sbh's ballast solves a different problem |
| NG-6 | Replacing or tuning macOS VM/compression/swap policy | Not permitted to third parties; unsupported and brittle |
| NG-7 | Acting as root / installing a privileged helper in v1 | Least privilege; user-scope LaunchAgent is sufficient for observation and for v1 relief actions |
| NG-8 | Auto-optimizing the user's workload (killing idle agents, throttling builds) | Adjacent to NG-1; deciding "this agent is idle" is exactly the judgment we refuse to make automatically |
| NG-9 | A GUI/TUI in v1 | CLI + JSONL first; a TUI is a later layer over a proven data model |
| NG-10 | Machine-learning model for control decisions in v1 | Control must be fast, predictable, auditable. Simple estimators + calibration first; prove a gain before adding complexity (ADR-005) |
| NG-11 | Cross-platform support in v1 | Focus; the seam exists (G-10) but Linux/Windows is out of scope |
| NG-12 | Telemetry upload / cloud analytics | Privacy; all data stays local. Opt-in export of a redacted incident bundle is a possible later feature (pt's bundle pattern) |
| NG-13 | Being a general system monitor (CPU/thermal/battery dashboards) | Scope creep; memory pressure only |

---

## 3. Priority order when goals conflict

When two goals pull against each other, resolve in this order:

1. **G-6 (protect work)** — never traded away.
2. **G-8 (own footprint)** — the tool may not become the problem.
3. **G-1/G-2 (measure & forecast correctly)** — wrong diagnosis is worse than no action.
4. **G-5 (relief)** — only after 1–3 are satisfied.
5. **G-7 (explain)** — always, but a decision may be deferred rather than act blind.
6. **G-9/G-10 (operability, portability)** — cheap to honor, never a reason to weaken 1–3.

A conflict that cannot be resolved by this order becomes an ADR.
