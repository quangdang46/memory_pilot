# First live run of the full chain under real pressure

Date: 2026-10-25 10:47–10:52. Target: Mac mini M4, 16 GB, darwin 25.4.

Until this run, no action had ever executed on a live daemon: every prior run
stayed at Normal/Warn, and before the policy fix the Warn branch discarded its
own candidate.

## How pressure was reached

Swap was already 7.9 GB of 9.2 GB. An allocator (incompressible 50 MB blocks,
0.3 s apart) brought `kern.memorystatus_vm_pressure_level` from 1 to **2**
(Warn) at ~2.4 GB held. It was released with SIGTERM immediately after the
measurement; free pages returned from ~4 k to ~320 k within seconds.

## What the daemon did

```
10:50:13  policy_candidates  watch/prevent: advisory only  candidates=['emit_advisory']
10:50:18  policy_candidates  watch/prevent: advisory only  candidates=['emit_advisory']
10:50:23  policy_candidates  watch/prevent: advisory only  candidates=['emit_advisory']
10:50:28  policy_candidates  watch/prevent: advisory only  candidates=['emit_advisory']
```

and for the first time in this project's history, action results:

```
10:49:53  action_result  emit_advisory  executed
10:50:03  action_result  emit_advisory  executed
...
10:50:28  action_result  emit_advisory  executed      (8 in total)
```

with evidence recorded per action:

```json
{"action":"emit_advisory","action_id":"emit_advisory-1792900193-0",
 "evidence":{"changed":"false","effect":"advisory recorded by caller","effect_bytes":"0"},
 "state":"executed","verified":"pending"}
```

`changed:false` is correct and important: an advisory changes nothing in the
user's memory. Recording it as a success with a byte effect would be the
dishonesty SI-5 forbids.

Then verification resolved:

```
10:51:58  verification  resolved=1  score_after=0.031
```

## The defect this exposed

The Warn branch in `policy.Evaluate` returned early without setting
`NoAction`, leaving it at the Go zero value `true`. The pipeline reads
`NoAction` as "nothing to do" and never executed the candidate it had just
planned. Field evidence from the preceding 40-minute run: **50 Prevent
samples, all recorded as `policy_no_action`, zero `action_result` rows**.

Fixed in `aefa693`; tests `TestWarnAdvisoryIsNotNoAction` and
`TestPreventCandidatesAreNotNoAction` fail on the old code.

## What this establishes

The chain measurement → pressure → policy → planner → executor → ledger →
verification runs end to end on a live daemon, and every step records what it
did. Before today no `action_result` had ever existed in a real ledger.

## What it does not establish

- **Critical** pressure was not reached. Reaching it needs ~4 GB more than
  this machine could spare given existing swap; deliberately hanging the host
  was not an acceptable cost for a test.
- Consequently the **Critical-only** path — T1 self-relief (`trim_own_buffers`,
  `rotate_own_ledger`) and L2 managed stop — has still never executed against
  a live daemon. It is covered by tests against real processes
  (`TestL2StopsRealOwnedProcessOnDevice` asserts the process leaves the
  process table).
- Whether an advisory at Warn is *useful* to a user. This run proves it is
  delivered and recorded, not that it helps.
