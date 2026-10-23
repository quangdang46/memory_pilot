# SCN-004 acceptance — The macOS dialog appears anyway

Trace: `real-incident-*` (a recorded OOM event), or a synthetic `ramp-fast` with a forced critical tail.

This scenario tests the product's honesty after it failed to prevent the event.

## Setup

- Pressure reaches critical; macOS posts the dialog; several apps show `(paused)`.
- The daemon was running the whole time.

## Assertions

| # | Assertion |
|---|---|
| D1 | The ledger contains an `incident` record with `window_start` (first Prevent), `window_peak`, `detected_at` |
| D2 | `mempilot explain --last` reconstructs: state timeline, forecast at T-10/T-5/T-1 min with confidence, top contributors, advisories emitted, decisions taken/refused |
| D3 | If the forecast predicted critical and gave a time, D2 shows whether it was early, late, or wrong |
| D4 | If the forecast did **not** predict it, the calibration ledger records a miss and `forecast_degraded` is triggered if the miss rate exceeds the threshold |
| D5 | The report explicitly states what the product could not do (e.g. "no safe relief available"), and does not claim it prevented anything |
| D6 | The full-resolution window (±15 min) is preserved regardless of retention policy |
| D7 | No signal was sent to any paused app (process audit) |

## Failure conditions

- Any retroactive claim of success.
- Missing confidence on any forecast quoted in `explain`.
- Loss of the high-resolution window due to rotation.
