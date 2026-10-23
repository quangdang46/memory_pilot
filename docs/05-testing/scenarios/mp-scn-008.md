# SCN-008 — Session lifecycle recommendation

Trace: `session-idle` — one agent quiet for 50 min (no CPU, no I/O), shell still open; one agent actively working; one session whose agent already exited.

## Assertions

| # | Assertion |
|---|---|
| I1 | The exited session is reported as ended, not as idle — nothing to recommend |
| I2 | The quiet session gets a recommendation with `idle_strength: medium`, the evidence list, and alternatives (network wait, long tool call) |
| I3 | The recommendation has **no** resume claim without an adapter |
| I4 | With an adapter that has verified `claude --resume <id>`, the recommendation includes the exact resume command |
| I5 | The working session is never recommended |
| I6 | The largest session is not recommended first merely for being large |
| I7 | Only one recommendation message per severity transition (rate-limited) |

## Failure conditions

- Recommending closure of the exited session ("close what's already gone").
- Implying resumability without adapter evidence.
- Ranking by size rather than `footprint × idle_strength`.
