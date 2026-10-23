# SCN-003 acceptance — Swap thrashing on a small SSD

Trace: `swap-cliff`.

## Setup

- 16 GB RAM, 21 GB free disk at start.
- t=0–20 min: swap 0 → 4 GB; disk free 21 GB → 15 GB.
- Free% 8% → 3%; compressor 12% → 16%.
- Two minutes of high disk I/O after t=15 min.

## Assertions

| # | Assertion |
|---|---|
| C1 | `disk_free` is an input to the pressure state whenever `swap_rate > 0` |
| C2 | State reaches `Prevent` and emits a `swap_rate` advisory within 2 samples of the cliff |
| C3 | The advisory states swap rate (MB/min) and remaining disk headroom |
| C4 | The daemon reduces its own I/O: system cadence tightens but ledger detail drops (`degradation ≥ 1`) |
| C5 | Attribution does not name the highest-RSS process as the cause; if growth is unattributable, a `system/compressor` line appears |
| C6 | A `disk_headroom` advisory fires when free disk < 15 GB while swap is growing |
| C7 | Forecast for swap is separate from the headroom forecast and both are present |

## Failure conditions

- Treating `swap_total` as capacity.
- Reporting disk growth as a per-process finding.
- Failing to reduce own I/O.
