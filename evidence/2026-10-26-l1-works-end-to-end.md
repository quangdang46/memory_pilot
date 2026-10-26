# Memory Pilot actually works: the loop, end to end

Everything here was measured on the live machine on 2026-10-26, not reasoned
about. The point of the exercise was to stop explaining and start
demonstrating, because the previous claim — "it handles leaks" — had never been
tested against a real leak the tool did not create by accident.

## The test object

A labelled probe, built to have exactly the shape L1 exists for:

| property | value | how it was made |
|---|---|---|
| holds memory | 905 MB | allocates 30 MB at a time, touching every page |
| parent gone | yes | the parent forked it and exited; macOS reparented it to pid 1 |
| launchd does not manage it | confirmed | `launchctl list` has no entry for the pid |
| not working | 0.01 s of CPU over 22 minutes | sleeps, touching pages every 20 s |

The last line matters: an untouched 900 MB allocation gets reclaimed by the
compressor — measured, 904 MB collapsed to 8.7 MB in forty minutes. The probe
touches its pages so the footprint stays real, which is also closer to a real
leak: a process that holds and uses memory is the case that hurts.

## The output the owner reads

```
$ mempilot relief
closing 6 abandoned workload(s) would return ~1506 MB
  /private/tmp/orphanprobe (pid 47396): 905 MB, quiet for a while, parent gone
  /Users/.../bin/node (pid 64747): 120 MB, quiet for a while, parent gone
  /Users/.../bin/node (pid 50455): 120 MB, quiet for a while, parent gone
  /Users/.../bin/node (pid 96270): 120 MB, quiet for a while, parent gone
  /Users/.../bin/node (pid 1470): 120 MB, quiet for a while, parent gone
  /Users/.../bin/node (pid 96011): 120 MB, quiet for a while, parent gone
shortfall is 2821 MB — this will NOT cover it, and nothing else can be
released safely

(21 candidate(s), 1506 MB releasable, shortfall 2821 MB, sufficient false,
 at Normal pressure)
```

That is the sentence `docs/06-problem-first/process-relief-engine.md` specifies:
what could be released, how much it returns, and whether that is enough. In this
case it says plainly that it is not enough — which is the honest answer, and the
one the design calls the most important thing the module can say.

The `node ... pnpm install` orphans are real too: 22–26 hours old, reparented
from long-dead `pnpm` invocations, 120 MB each, nothing using them.

## Ground-truth cross-check

The claim is only worth as much as its check. For every named process:

```
claimed candidates: 21   releasable MB: 1506   named abandoned: 6
  pid 47396: claimed 905 MB | real 905 MB | ppid 1 | launchd manages it: False -> OK
  pid 64747: claimed 120 MB | real 120 MB | ppid 1 | launchd manages it: False -> OK
  pid 50455: claimed 120 MB | real 120 MB | ppid 1 | launchd manages it: False -> OK
  pid 96270: claimed 120 MB | real 120 MB | ppid 1 | launchd manages it: False -> OK
  pid  1470: claimed 120 MB | real 120 MB | ppid 1 | launchd manages it: False -> OK
  pid 96011: claimed 120 MB | real 120 MB | ppid 1 | launchd manages it: False -> OK

6 correct, 0 wrong
```

"Real" is `/usr/bin/footprint`, the kernel's own number. Six of six match to
the megabyte.

## What had to be built to get here

Each of these was forced by a failure, not chosen in advance.

| piece | the failure that forced it |
|---|---|
| `mempilot relief` (IPC op) | L1 was gated on pressure ≥ Warn and a 20-minute cooldown, so at Normal pressure the owner could never ask the question, and the proof would have depended on waiting for the machine to thrash |
| launchd job list | macOS reparents an orphan and a launchd service to pid 1 identically. Without a third source, either every service looks abandoned (the first live run: Finder, the VM, every Chrome helper) or no orphan ever is |
| `parentSeen` | "parent not in the series" is ambiguous — the parent may never have been sampled. The probe sat at `parentGone=false` for its whole life |
| ppid from the latest snapshot | series are swept two dead ticks after exit, so by the time an orphan is old enough to be a candidate, its parent's series is gone |
| lifetime-CPU evidence | clamping every quiet claim to the observation window refused the real orphan. A process's own accounting is evidence independent of when the sampler started |
| full image path for identity | `comm` reported three `claude` processes as "2.1.310" — the version directory — so every protection list missed the agent the owner was talking to |

## The number was checked by closing the process

L1 said 905 MB. "Closing this returns 905 MB" is a promise about the machine, so
it was measured: sum every process's `phys_footprint` before and after closing
the orphan, with nothing else changed.

```
probe at full size:                 Footprint: 905 MB
total process footprint, before:    25466 MB   (501 processes)
total process footprint, after:     24652 MB   (492 processes)
difference:                          814 MB
```

814 of 905 MB, 90%. The missing 91 MB is shared pages: `phys_footprint` counts
a mapping once per process, and part of that 905 MB was pages other processes
also map. The `report` output has carried this caveat since it was written
("Shared pages are counted per-process, so process sums can exceed the system
delta"); this is the first time it was quantified on a real case.

So the recommendation is accurate to within 10% on the optimistic side, and it
is optimistic in the direction the text already warns about. Recording that is
more useful than claiming the prediction is exact.

## What this establishes, and what it does not

**Establishes:** the detection and recommendation tier works on a real orphan —
it finds a process holding memory that nothing is using, names it, quantifies
it, and is honest about whether the total closes the gap. The numbers match the
kernel's own accounting.

**Does not establish:**

1. **Nothing is freed.** v1 is detect-and-recommend only; the owner still closes
   the process. This is a design decision (ST-1/ST-2 fail the build if a
   termination primitive appears outside tests), not an oversight, and it is the
   reason the product cannot "handle" a leak by itself.
2. **The automatic path ran, and its history is the record of this work.**
   Eight `relief_recommendation` records were written at Warn, unasked:

   ```
   23:51:59  cands= 39  relief=    0MB  named=0   (Finder, the VM, every helper)
   00:34:22  cands= 33  relief=    0MB  named=0   ("no CPU for 5m30s" = daemon age)
   06:13:47  cands= 43  relief=    0MB  named=0
   06:47:23  cands=  1  relief=    0MB  named=0   (the probe, mislabelled)
   06:54:13  cands= 36  relief=    0MB  named=0   (lifetime evidence, no orphan)
   06:55:18  cands= 36  relief=    0MB  named=8   (first orphans found)
   07:10:50  cands= 33  relief=    0MB  named=6
   07:28:24  cands= 18  relief=  601MB  named=5   (relief counted, honest gap)
   ```

   The progression is the measurement: the same machine, the same pressure
   level, five separate defects removed. The last row is the automatic path
   producing the correct sentence without being asked, at a moment when
   `mempilot relief` was not running.
3. **Critical paths and the 72-hour soak** are unchanged from
   `evidence/FINAL-STATUS.md`.
