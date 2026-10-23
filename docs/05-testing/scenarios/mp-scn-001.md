# SCN-001 acceptance — Long agent session, pressure climbs slowly

Requirement: `../00-product/scenarios.md` MP-SCN-001
Traces: `ramp-slow`, plus a recorded real session where available.

## Setup

- 3 synthetic sessions (anchor + 14 children each), one with a growing MCP-like child.
- Growth: session A's child +40 MB/min; sessions B/C flat.
- Duration: 3 h simulated, 5 s samples.
- System: free% declines from 38% → 9%; compressor rises 6% → 14%; swap stays 0.

## Assertions

| # | Assertion |
|---|---|
| A1 | State reaches `Watch` no later than when free% crosses 20%, and not before the trend is established (≥ 5 samples) |
| A2 | By minute 30 the forecast reports `calibration.state == "calibrated"`, a non-null `eta_lower_bound_s`, and `actionable == true`; the bound is ≤ `eta_point_s` and confidence ≥ 0.5. Before 30 resolved outcomes it must report `state == "warming"` with `actionable == false` even though a point estimate is shown |
| A3 | Attribution names session A's child group as the top grower in ≥ 80% of windows after minute 20 |
| A4 | Zero advisories mention a protected group as a target |
| A5 | Zero actions (observe mode, and no provider would apply anyway) |
| A6 | No classification other than `workload_growth` for session A (its growth correlates with its CPU) |
| A7 | Cadence: system sampling steps 5 s → 3 s at Watch; process sampling 30 s → 20 s |
| A8 | Ledger volume for the 3 h window ≤ 2 MB |

## Failure conditions

- Any `leak_suspicion` for a group whose growth correlates with activity.
- Any forecast without confidence/interval.
- Any state transition Normal → Prevent without passing through Watch for ≥ 3 samples.
