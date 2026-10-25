# Final status — 2026-10-25

## The original report

> "repeated leak waves but mp had no effect"

Correct. The daemon had been running for 12 hours, writing 17 406 system
samples and 8 413 decisions, performing zero actions and producing zero
attributions — while reporting a pressure state that was false (Critical
latched for 45 minutes after the kernel returned to Normal).

## What was wrong, and the evidence for each

| # | Defect | Evidence | Status |
|---|---|---|---|
| 1 | action layer never constructed; attribution never fed | 12 h ledger: 0 attributions, 0 action results | fixed |
| 2 | `resident_size` collapses under pressure | holding 400 MB: rss 71→8 MB, footprint 428 MB; on an idle 200 MB process rss spreads 217 MB, footprint 10 MB | fixed |
| 3 | `cache_or_retrievable` and `subprocess_accumulation` unreachable | `PlateauS` never computed; the subprocess bucket was never assigned anywhere | fixed |
| 4 | only the top 5 groups by bytes were judged | run 3: 5 labelled probes, 40 min, 0/5 classified | fixed |
| 5 | release volume measured, stored, ignored | Chrome: 2.9 GB released vs 1.9 GB grown, labelled leak 4 windows running | fixed |
| 6 | `NoAction` left true in the Warn branch | 50 Prevent samples recorded `policy_no_action` with `candidates=[emit_advisory]` | fixed |
| 7 | Attributor had no lock | live crash: `fatal error: concurrent map read and map write` | fixed |
| 8 | accumulation bucket fired on any multi-process app | zsh 0 MB, Chrome 12 MB, node 110 MB flagged in one window | fixed |
| 9 | coverage counted other users' processes as denied | 228/657 "broken" on an idle desktop | fixed |

## What now works, measured

**Labelled verdicts** (run 8, five synthetic shapes with ground truth fixed
before the run):

```
probe-children  subprocess_accumulation  0.60  correct   "process count reached 21"
probe-leak      leak_suspicion           0.65  correct   "sustained 1499s >= 1200s"
probe-plateau   cache_or_retrievable     0.70  correct   "plateau 1005s >= 900s"
probe-workload  workload_growth          0.90  correct   "corr 1.00"
probe-sawtooth  leak_suspicion                 the LABEL was wrong (Go keeps the heap)
```

Same shapes scored 0/5 in run 3 and 1/5 in run 1.

**Full chain under real pressure** (kernel level 1 → 2):

```
policy_candidates  candidates=['emit_advisory']   x8
action_result      emit_advisory  executed        x8
verification       resolved=1  score_after=0.031
```

The first `action_result` rows in this project's history. Each carries
`changed:false` — an advisory changes nothing, and saying otherwise is what
SI-5 forbids.

**On a normally loaded machine** (the state the user actually sees):

```
$ mempilot status
mempilotd pid 44037 | uptime 10m | mode canary | pressure Normal (0.32, kernel)
  +796 MB  wezterm-gui   19 procs
  +441 MB  node          20 procs
  +403 MB  Docker Desktop 3 procs

$ mempilot report
... coverage: 490 seen, 151 outside this user, 0 denied
... classification: growth consistent with caching behavior
      (released 687 MB while growing 553 MB — cycling, not accumulating)
```

Zero advisories. The warning list the owner complained about — Chrome and node
every window — is gone, because those verdicts are now correct and not worth
interrupting anyone for.

## What is still NOT proven

1. **Critical pressure.** Reached level 2, not 4. Reaching 4 on this machine
   requires more memory than it could spare; deliberately hanging the host was
   not an acceptable test cost. So the Critical-only paths — T1 self-relief
   and L2 managed stop — have never executed on a live daemon. They are
   covered by tests against real processes (the L2 test asserts the process
   leaves the process table), not by field evidence.
2. **Thresholds are chosen, not fitted.** 1200 s evidence, 900 s plateau,
   256 MB accumulation, 8 processes. No false-positive corpus exists to fit
   them against. The spec requires one before promoting beyond canary.
3. **Advisories help?** Proven delivered and recorded. Not proven useful.
4. **72-hour behaviour.** The daemon has run continuously for hours and holds
   ~12 MB / 7 goroutines, but the long soak in the plan is still calendar-bound.

## Where the evidence lives

- `2026-10-25-metric-vanishes/` — why the measurement was wrong, with repro scripts
- `2026-10-25-labelled-run/` — first labelled run (1/3)
- `2026-10-25-run3/` — 0/5, the top-N defect
- `2026-10-25-run8/` — 4/5, the fix working
- `2026-10-25-live-pressure/` — first live action results under pressure
- `2026-10-25-fix-log.md` — every fix mapped to its commit and test
- `2026-10-25-run12-NOTES.md` — a discarded run: the harness let the probes
  inherit the terminal's group, so it measured nothing (not a product defect;
  the grouping was correct)
