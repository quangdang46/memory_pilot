# Defect 10 — a new group inherits a dead group's evidence

## Observed, not inferred

run15 started at 11:40:14. At 11:45:20 (306 s later) the daemon reported:

```
probe-leak      unknown   window 884s < 1200s — too short
probe-children  unknown   window 1049s < 1200s — too short
```

Neither group had existed for more than 306 s. The claimed windows came from
`probe-*` groups recorded by **run12 at 11:30**, which had been dead for an
hour.

## Cause

`Attributor.history` is keyed by anchor *name* (`probe-leak`, `node`, …). A
group's long-horizon record was created on first sighting and only removed in
one circumstance: when `len(history) > 4096` (`sweepDeadSeries`). A group that
exited kept its record forever, so the next process with the same name
inherited `firstSeen`, and therefore `DurationS`:

```go
span := a.now.Sub(h.firstSeen).Seconds()
st.DurationS = int64(span)
```

`DurationS` is the gate into `leak_suspicion` (`classify.go:145`):

```go
if s.QuietS >= QuietWindowS && s.DurationS >= MinSuspectWindowS && ...
```

So a process that had just started could satisfy the 20-minute evidence
requirement on its first appearance and be called a leak immediately — the
false positive the owner asked never to see again.

## Why it matters beyond the lab

Group names are reused constantly in real use: `node`, `Code Helper`,
`Google Chrome Helper`, any shell. Each new login session would inherit the
previous session's evidence window. The error is not cosmetic; it removes the
waiting period the whole design depends on.

## Fix

`retireGoneGroups` deletes a group's history once no member has been alive for
`goneGraceS = 120` s. The grace period is longer than a plausible sampler stall
and far shorter than any evidence threshold, so a hidden group keeps its
evidence and a replaced group does not.

Test: `TestHistoryRetiredWhenGroupDisappears` — fails before the fix with
`firstSeen 09:51 is before the new process started 10:31`, passes after.

## Run8 was not affected

Checked rather than assumed. In `run8-ledger.jsonl` the durations advance with
the run itself (284 → 584 → 899 → 1199 → 1499 s), so run8's groups started with
it. The defect only bites when a previous run's groups are still in memory —
true for run15, not for run8.

## harness gate added

`verify_inputs.py` now fails a run when (a) a probe has no group of its own
name after the first window closes, or (b) the daemon claims more seconds of
evidence than the run has existed. Both checks are written against failures
that were actually observed, and both are cheap enough to run before spending
the window.
