# The accumulation bucket: from "any app with processes" to a real signal

## First attempt (wrong)

Threshold was `PeakProcessCount >= 8`. On a normally loaded machine, one
attribution window produced:

```
zsh              0 MB growth   -> subprocess_accumulation (0.60)
Google Chrome   12 MB growth   -> subprocess_accumulation (0.60)
2.1.310         12 MB growth   -> subprocess_accumulation (0.60)
mdworker_shared  0 MB growth   -> subprocess_accumulation (0.60)
node           110 MB growth   -> subprocess_accumulation (0.60)
```

The bucket was answering "does this application have more than eight
processes", which is true of every long-lived app. A warning about zsh growing
0 MB is the kind of output that trains a user to ignore the tool.

## Second attempt (current)

Requires `PeakProcessCount >= 8` **and** `GrowthBytes >= 256 MB` **and** no CPU
correlation.

Measured after the change, same machine, same workload, ~30 minutes of windows:

```
workload_growth        22
cache_or_retrievable   11
unknown               284
subprocess_accumulation 0
advisories              0
```

and the labelled probe with 21 real children and 500 MB of growth still
classifies as accumulation (`TestRealAccumulationStillDetected`).

## The honest reading

256 MB is a guess that separates this machine's noise from the probe. It is not
fitted to anything: no false-positive corpus exists. What the change does
establish is the direction — a bucket that fires on 0 MB of growth is worse
than a bucket that stays quiet, because a warning nobody trusts is worse than
no warning.
