# Defect 25 — a coverage gap accounted as an arrival and a departure

Found the same way as 23 and 24: by reading the live process tree beside what
the daemon claimed, not by reading code.

## The observation

```
REAL node: 36 processes, 3187 MB, steady
DAEMON:    net=+1993 MB, gross=19391 MB, subprocess_accumulation
```

The machine sees 75 % of processes per tick — 201 of 791 are unreadable each
sample. A group's members therefore flicker in and out of the snapshot. Two
accounting errors followed from that:

1. A process returning after its series had been swept was counted as a fresh
   arrival, because `start > firstSnapAt` stays true for the rest of the
   daemon's life. Every flicker re-added its whole footprint.
2. The sweep had already charged that footprint as release, and nothing gave it
   back when the process reappeared.

Together these moved net by the process's size on every flicker, in whichever
direction the flicker happened to fall.

## Measured before the fix

One steady 300 MB process hidden and revealed five times:

```
net = -1500 MB      (a process that never changed size)
```

And on the live machine, node's net over six windows while holding 3.2 GB
steady:

```
-111  -117  +46  +2878  +32  +1993   MB
```

## The fix

- `seenIdentities` counts an arrival once per identity, ever, not once per
  series.
- `lastBytes` remembers what a swept series held, and returns it on
  reappearance — the sweep was wrong, so its charge is undone.

Measured after: the same flickering process reads **0 MB**, and node on the
live machine reads **-32 MB** with bucket `cache_or_retrievable` and no
advisory.

## Why the test suite missed it

Every test ran at lab scale with full visibility: a snapshot that contains all
its processes every tick. The defect only exists when coverage is partial, and
partial coverage is the normal state of the machine this product runs on.
`TestCoverageGapsDoNotChangeNetSize` now reproduces the gap explicitly.
