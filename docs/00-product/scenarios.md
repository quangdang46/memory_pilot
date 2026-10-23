# MP-SPEC-004 — User & Failure Scenarios

**Status:** Draft
**Version:** 0.1.0

Scenarios are the bridge between requirements and tests. Each has: trigger, what the system observes, what it must do, what it must NOT do, and the acceptance test that encodes it.

Format: **MP-SCN-xxx**

---

## Part A — User scenarios

### MP-SCN-001 — Long agent session, pressure climbs slowly

**Situation.** Three coding-agent sessions have been open for 5 hours. RAM has crept from 9 GB to 14.5 GB used on a 16 GB machine. Compression is growing; swap is minimal. The user is actively typing in session B.

**Observations available:** pressure = normal→warn; per-process footprints rising in session A's MCP server and browser helpers; session B's agent RSS is high but flat.

**Must:**
- Report `trend: rising`, `time_to_critical: ~N minutes` with confidence, and name the MCP server group as the leading contributor.
- Stay in `WATCH`; increase sampling; do not act.
- If a whitelisted relief action applies (e.g. a provider-owned cache), propose it in the report — not execute silently.

**Must not:**
- Touch any session process.
- Claim a leak.
- Lower sampling because RAM is "fine some other way".

**Acceptance:** `../05-testing/scenarios/mp-scn-001.md`.

---

### MP-SCN-002 — One app balloons in minutes

**Situation.** A browser (or an agent subprocess) goes from 400 MB to 6 GB in 8 minutes while the user is idle.

**Must:**
- Detect step-change growth (not a slow trend) using the burst detector; distinguish "growth rate anomaly" from "sustained leak".
- Attribute to the group (parent + helpers), with per-PID deltas.
- Escalate to `PREVENT`; run approved relief; report.
- If no relief is available, escalate to `CRITICAL` with a clear recommendation and rationale — and *not* act.

**Must not:** kill the browser. Ever, at any pressure level.

**Acceptance:** `../05-testing/scenarios/mp-scn-002.md`.

---

### MP-SCN-003 — Swap thrashing on the 256 GB SSD

**Situation.** Swap grows 4 GB in 20 minutes; free disk falls below 15 GB; system UI latency is degrading.

**Must:**
- Treat disk headroom as a first-class pressure input (swap needs space).
- Report the *coincidence* of swap growth and disk decline as a distinct risk signal.
- Reduce its own I/O and log verbosity (self-monitor degrade path).
- Raise a `disk-headroom` advisory with numbers.

**Must not:** assume swap growth is caused by the process with the highest RSS.

**Acceptance:** `../05-testing/scenarios/mp-scn-003.md`.

---

### MP-SCN-004 — The dialog appears anyway

**Situation.** macOS posts the out-of-memory dialog; several apps show `(paused)`.

**Must:**
- In `explain --last`, show the pre-incident timeline: when risk rose, what the forecast said, what contributors were named, what actions were taken or refused and why.
- Record the incident as a first-class ledger event (detected via pressure→critical transition + post-hoc reconciliation), preserving the evidence window for review.

**Must not:**
- Retroactively claim it predicted correctly if it did not. Calibration records the miss (see `../03-algorithms/forecasting.md` §4).

**Acceptance:** `../05-testing/scenarios/mp-scn-004.md`.

---

### MP-SCN-005 — User asks "what is eating my RAM?"

**Situation.** No incident; user runs `mempilot report --last 2h`.

**Must:** return top contributors by *growth* over the window (not absolute RSS), grouped by workload, with a one-line explanation per group, plus a footnote listing processes that are large but flat (correctly not blamed).

**Must not:** sort by RSS and call it "blame" (the classic wrong answer; sbh's `blame` and pt's scoring both avoid this and we follow that).

**Acceptance:** `../05-testing/scenarios/mp-scn-005.md`.

---

### MP-SCN-005b — Planner must act when a valid action exists

**Situation.** Pressure is `Prevent` with a calibrated `actionable` forecast; one proven provider is registered, all preconditions satisfy, and its measured effect clearly exceeds its cost.

**Must:** choose the provider — not `NoAction`. And when the same fixture is replayed with zero expected benefit, or with mode `observe`, choose `NoAction` with the correct reason.

**Why it is a scenario, not a unit test:** `NoAction` is always available and always free, so a cost-minimizing planner silently degenerates to "never act". A suite that only tests refusal passes while the product does nothing. Only the *pair* of positive and negative cases proves the planner reasons about benefit.

**Acceptance:** `../05-testing/scenarios/mp-scn-006.md`.

---

### MP-SCN-006 — Daemon restarted mid-incident

**Must:** on startup, detect that the previous run ended without clean shutdown; load the tail of the ledger; report a gap explicitly (`observation_gap`); never treat the gap as calm.

**Acceptance:** `../05-testing/fault-injection.md` FI-3.

---

## Part B — Failure scenarios

### MP-SCN-101 — Sampling fails

**Trigger:** `host_statistics64` or `vm_stat` parse fails; swap read fails; process enumeration returns partial data.

**Must:** mark the metric `unavailable` with the error; keep the rest of the pipeline running; never substitute defaults; never clear an existing warning because a source went dark; write one diagnostic event per source per N minutes (rate-limited).

### MP-SCN-102 — Process info incomplete

**Trigger:** `proc_pidinfo` denied for some PIDs (other users / SIP-protected); some processes missing from the snapshot.

**Must:** attribute only over visible processes; report `coverage: partial` with the count and estimated missing share; widen uncertainty in attribution confidence.

### MP-SCN-103 — Clock jumps / sleeps

**Trigger:** laptop sleeps, or NTP steps the clock.

**Must:** use a monotonic clock for rate computations; on a detected backward or large forward gap, reset the rate estimator (mark `warmup`) rather than produce a nonsensical rate.

### MP-SCN-104 — Disk full while logging

**Trigger:** ledger write fails with ENOSPC.

**Must:** switch to stderr, cap fallback log, enter degraded mode; do **not** delete old ledger segments to make room without an explicit policy (retention is bounded and rotation-based, never "delete whatever to survive").

### MP-SCN-105 — No safe relief exists at CRITICAL

**Trigger:** pressure critical; every relief provider either fails preconditions or is unproven.

**Must:** emit a CRITICAL advisory naming contributors and options (including "close X yourself"), leave an evidence bundle marker; stay in observe mode.

**Must not:** invent an action to look useful.

### MP-SCN-106 — Daemon's own RSS exceeds budget

**Trigger:** self-monitor sees RSS > warning threshold (default 200 MB warn / 400 MB hard for this product — see performance-budget.md).

**Must:** shed load (drop detail sampling, flush+truncate in-memory buffers, reduce retention), record why; at the hard limit, exit cleanly with a documented reason and let launchd restart it (KeepAlive), never grow unbounded.

### MP-SCN-107 — Forecast is wrong

**Trigger:** predicted imminent critical; nothing happened (false positive) — or predicted calm; dialog appeared (false negative).

**Must:** record the outcome against the forecast in the calibration ledger; surface the error statistic; if false-positive rate exceeds the configured ceiling, automatically damp aggressiveness (raise the confidence required to act) and emit a `forecast_degraded` advisory.

### MP-SCN-108 — Relief action has no effect

**Trigger:** action executed successfully, but post-action measurement shows no pressure reduction within the verification window.

**Must:** mark the action `ineffective` in the ledger, apply an exponential backoff to that action, and raise its cost so the planner prefers other actions. After repeated ineffectiveness, quarantine the action and emit an advisory. Never retry in a tight loop.

### MP-SCN-109 — User has strong opinions about a process

**Trigger:** user marks a workload group as `protected` (e.g. "never suggest anything about my IDE").

**Must:** honor it in attribution reporting (still report, but exclude from action targeting) and in policy; persist across restarts; `mempilot policy status` shows it.

### MP-SCN-110 — Two daemons running

**Trigger:** manual `mempilotd` started while the LaunchAgent instance is live.

**Must:** detect via lock file + socket; the second instance refuses to run and explains (never two writers to one ledger; never two controllers racing).

---

## Part C — Anti-scenarios (things that must never appear)

| ID | Forbidden behavior | Why recorded here |
|---|---|---|
| MP-ASCN-001 | Any code path sending a signal to a non-owned PID | SI-1 |
| MP-ASCN-002 | Any log line or JSON field asserting "X is leaking" without confidence+evidence | G-4 |
| MP-ASCN-003 | A "cleanup" that deletes user files | Out of scope entirely (disk cleanup is SBS's problem, not ours) |
| MP-ASCN-004 | Acting while telemetry is `unavailable` | P4 |
| MP-ASCN-005 | Retrying a failed relief action in a loop | MP-SCN-108 |
| MP-ASCN-006 | Unbounded log/ledger growth | MP-SCN-106 |
| MP-ASCN-007 | Reporting a forecast without its confidence | P7 |
