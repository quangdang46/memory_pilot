# ADR-004 — Adaptive sampling schedule and its floors

**Status:** Proposed
**Date:** 2026-10-23
**Related:** MP-REQ-010, `../05-testing/performance-budget.md` §2

---

## Context

Sampling is the daemon's main cost, and cost matters most exactly when the machine is in trouble. Two independent loops are needed because the two workloads have different prices:

- **System loop** (`host_statistics64`, sysctl pressure level, swap): ~30 µs total. Cheap enough to run often.
- **Process loop** (`proc_pidinfo` per PID): 30–80 µs × N (400–800) ⇒ 15–60 ms. Expensive.

sbh has a single disk-poll loop (default 5 s) divided by pressure level (`base/2`, `/4`, `/8`, down to 100 ms at Critical) — but sbh's per-tick cost is a handful of `statfs` calls, not per-process enumeration. Its schedule does not transfer.

---

## Decision

Two cadences, both pressure-adaptive, both floored:

| State | System loop | Process loop | FP (footprint) loop |
|---|---|---|---|
| Normal | 5 s | 30 s | every 4th process sample |
| Watch | 3 s | 20 s | every 3rd |
| Prevent | 2 s | 15 s | every 2nd |
| Critical | 1 s | 10 s | every sample |
| Unknown | 3 s | 20 s | every 3rd |

**Floors are hard:** system ≥ 1 s, process ≥ 10 s. Rationale: below 1 s the system loop's own syscall load becomes measurable on a 16 GB M4; below 10 s the process loop exceeds the CPU budget outright (0.6 % per tick observed in the cost model).

**Event override:** a dispatch pressure event triggers an immediate system sample regardless of schedule, but never a process sample (too expensive to do on every event; events can cluster).

**Backoff on failure:** a source that fails backs off exponentially (2× per failure, cap 5 min) and is retried on schedule otherwise. A denied PID is not retried for 60 s.

**Deterministic scheduling:** the schedule is a pure function of `(state, last_failure, config)` — testable without sleeping, by injecting a clock.

---

## Evidence

None yet. Required:

- **E1:** measured per-tick cost of each loop at N≈600 on the target machine.
- **E2:** whether 1 s system polling is visible in Activity Monitor's energy impact (it should be nil; prove it).
- **E3:** does a 10 s process cadence still resolve the growth events we care about? (Test with the `burst-single` and `ramp-fast` traces at 10 s.)
- **E4:** does the dispatch event stream cluster during critical pressure (would make per-event sampling wasteful)?

## Consequences

**+** Cost scales with need; the daemon gets *faster* when pressure matters and *cheaper* when it does not.
**−** Two loops to reason about; two sets of gaps to record; interpolation between process samples is forbidden (deltas must use actual elapsed time, not nominal cadence).

## Revisit triggers

E3 shows missed events at 10 s, or E1 shows the process loop is cheaper than assumed (then simplify to one loop).
