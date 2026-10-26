# The advisory loop, observed live end to end

On 2026-10-26 a labelled leak was built and left running on the real machine,
under real Warn pressure, with the real daemon. This is the first time the
advisory path has been observed doing both halves of its job in one run.

## The probe

`/tmp/leakprobe2`, launched detached so its ancestry ends at launchd:

| property | value |
|---|---|
| behaviour | allocates 6 MB every 20 s, forever, touching every page |
| CPU | negligible — it never works, it only holds |
| parent | exited immediately; reparented to pid 1 |
| size at verdict | 383 MB held |

## What the daemon did

```
12:27:08  unknown          dur=329s   net=363MB
          basis: net growth 363 MB exceeds twice the lifetime growth 112 MB
                 quiet period 329s < 600s, window 329s < 1200s — too short

12:42:18  leak_suspicion   dur=1244s  net=361MB  quiet=1244s
          basis: sustained 1244s ≥ 1200s with no plateau
                 grew 361 MB in total
                 activity flat for 1244s ≥ 600s (uncorrelated)
                 not in cache-role table
                 coverage 1.00 adequate
```

At 12:42:18 the advisory was written:

```
ADVISORY 12:42:18 | leakprobe2 | level 2
  group leakprobe2 grew by 361 MB — unexplained sustained growth (suspicion)
  (confidence 0.65)

EMITTED 12:42:18 {'bucket': 'leak_suspicion', 'group': 'leakprobe2', 'growth_mb': 361}
```

Five minutes later, the same verdict was held back:

```
12:47:18  emitted=0  suppressed=1
          why: already advised 5m0s ago (limit 1h0m0s)
```

## Why this matters more than a unit test

The request was that the warning board stop appearing. Every previous claim
rested on absence — "no advisories for N minutes" — which is the same
observation as a broken path. Four defects in this session proved that:

- a dedup that held back an escalation to Critical;
- a rate limiter that refused at Critical (193 of 261 attempts);
- an executor reporting "advisory recorded" 40 times while zero advisories were
  written;
- a policy branch offering an advisory it never recorded.

All four looked exactly like a quiet window. What distinguishes this run is that
the advisory **appeared** and then **stopped**, both recorded, in the same
process, on a real leak the daemon found by itself.

## What it still does not show

- **One leak, one run.** The behaviour is correct here; that is not a promise
  about a month.
- **The probe is synthetic.** It leaks in the simplest possible way — steady
  allocation, no plateau, no CPU. A real application's leak, entangled with
  cache behaviour and work, is harder, and the classifier's thresholds were not
  fitted against one.
- **Nothing was freed.** The probe was killed by hand at the end, as the product
  requires: `ADR-012` fixes detect-and-recommend as the whole of v1, with
  "Revisit triggers: None."
