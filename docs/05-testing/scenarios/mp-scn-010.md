# SCN-010 — Relief engine: three numbers, never one score

Trace: `relief-candidates` — a 15 MB abandoned helper, a 4 GB busy process, and a 900 MB verified orphan.

## Assertions

| # | Assertion |
|---|---|
| K1 | Each candidate reports **abandonment confidence**, **interruption risk**, and **expected relief** as three separate fields |
| K2 | The 15 MB abandoned helper is high-confidence/low-risk but ranks **low for priority** (tiny relief) |
| K3 | The 4 GB busy process is never L2-eligible, regardless of relief — interruption risk gates it |
| K4 | The 900 MB verified orphan is the top *actionable* candidate and carries the full evidence set |
| K5 | If the gap is 2 GB and the best available relief is 900 MB, the report **states that it is insufficient** |
| K6 | No code path merges the three numbers into a single sort key that grants permission |
| K7 | Every candidate's `basis` and `alternative_explanations` are present (schema-enforced) |

## Failure conditions

- A single composite score deciding eligibility.
- Ranking the busy 4 GB process as "the obvious target".
- Implying a 900 MB action closes a 2 GB gap.
