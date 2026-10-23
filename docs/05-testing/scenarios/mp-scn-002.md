# SCN-002 acceptance — One app balloons in minutes

Traces: `burst-single`, `subprocess-storm`.

## Setup

- Browser-like anchor with 22 helpers.
- At t=20 min, helpers allocate 5.6 GB across 8 new processes over 8 minutes.
- CPU rises modestly (allocation work), I/O flat.
- Free% 34% → 4%; compressor 5% → 18%; swap begins at t=26 min.

## Assertions

| # | Assertion |
|---|---|
| B1 | `burst_probability` > 0.6 within 3 samples of the first large allocation |
| B2 | Classification is `workload_growth` or `unknown` — never `leak_suspicion` (the growth is a step, not sustained) |
| B3 | Attribution groups all 22+8 helpers under one workload with the parent-child rationale stated |
| B4 | Escalation reaches `Critical` only after the swap signal appears (or the kernel says critical) — a burst alone does not mean critical |
| B5 | Advisory names the group and states the growth in bytes and the time window; it contains no imperative to quit the app |
| B6 | At Critical with no safe provider available: `decision = NoAction{reason: no_safe_relief}` recorded, advisory emitted |
| B7 | The burst does not distort the EWMA into a long-lived elevated rate (rate returns to baseline within 3 samples after the step) |
| B8 | The unified report shows the step's start time (±1 sample) |

## Failure conditions

- Claiming a leak.
- Escalating to Critical purely on rate magnitude.
- Losing the group structure (reporting 30 unrelated processes).
