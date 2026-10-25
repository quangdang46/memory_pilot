# Defect 27 — net size summed from members instead of measured

## The number that exposed it

Thirty minutes of steady node, measured every two minutes:

```
REAL node:  3177 3180 3184 3177 3239 3183 3185 3242 3438 3183 3624 3185 3185 3185 3186 MB
DAEMON net:  682  682  682  507  507  507  528  528  510  510  507  507  507  507  507 MB
```

The group moved 9 MB. The daemon reported 507 MB — at one point 682.
Every 5-minute window raised an advisory, each saying the group had grown by
roughly 500 MB, for a group that never moved.

## Why

`netGrowthBytes += d` summed every member's per-tick delta. That equals the
group's change in total size only when every member is visible in every sample.

Coverage on this machine is 75%: 201 of 791 processes are unreadable each
sample, so members flicker. Summing per-member deltas at partial coverage
counts a member's drop on a tick it is missing but not the matching rise when
it returns — small errors, accumulated over thousands of ticks and 35 members,
becoming hundreds of megabytes of growth that never happened.

## The fix

`netGrowthBytes = total size now − total size when the group was first seen`,
recomputed from `tickBytes` each tick. It cannot drift, because it is a
difference of two measurements rather than a running sum of many.

Measured after: **6 MB** against a real change of 2 MB, and no advisory.

## Two tests, because the obvious one is not enough

- `TestGroupWobbleDoesNotAccumulate` — 35 members wobbling independently around
  a steady total. Reads 0 MB where it read 507.
- `TestRealGroupGrowthIsReported` — 10 members each genuinely adding 20 MB per
  tick. Still reports 7.8 GB. Without this one, "return zero" would pass.

## The pattern across 23, 24, 25, 27

Four defects in the same accumulator, each found by comparing a claimed number
against the process tree at the same moment:

| # | What was summed that should not have been |
|---|---|
| 23 | every upward step, used as the group's size |
| 24 | arrivals, with no departure on the other side |
| 25 | coverage gaps, as both arrival and departure |
| 27 | per-member deltas, at partial coverage |

None of them was visible in a unit test written at lab scale with full
visibility. All four are the same mistake in different clothes: treating a sum
of many small observations as one measurement.
