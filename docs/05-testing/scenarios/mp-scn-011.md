# SCN-011 — Escalation ladder and same-tick response

Trace: `escalation-full` — pressure climbs normal → watch → prevent → critical over 40 min while several sessions grow, and the collector degrades mid-way.

## Assertions

| # | Assertion |
|---|---|
| L1 | Every severity transition is recorded with the inputs that caused it |
| L2 | At L2, admission control becomes active and defers a launch that would not fit |
| L3 | At L3, orphan candidates are listed and the report states plainly if nothing safe is reclaimable |
| L4 | At L4, the dominant grower is named with the three relief numbers |
| L5 | At L5 with no safe path, the output is an explicit decision point — **zero automatic terminations** |
| L6 | **Same-tick response:** every tick at L2+ produces either an action or a recorded `no_action_reason`; no tick is silent |
| L7 | When the collector degrades mid-way, the severity **does not de-escalate** on the missing data (SI-4) |
| L8 | De-escalation honors the dwell times (5/5/5/15 min) and does not flap |
| L9 | Cause classification may be `undetermined` while the severity response still fires (diagnosis never gates the alarm) |

## Failure conditions

- A tick with neither an action nor a recorded reason.
- De-escalating because a metric went `unavailable`.
- Waiting for cause classification before responding to a critical trend.
