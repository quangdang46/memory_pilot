# Run 8 — first results with all nine fixes running

Status at 10:31 (run started 10:07, still running). Verdicts observed so far,
read directly from the ledger. Not a final score; recorded because two labels
have now been answered on real data for the first time.

| probe | label | verdict | conf | status |
|---|---|---|---|---|
| probe-children | subprocess_accumulation | **subprocess_accumulation** | 0.60 | matches label |
| probe-workload | workload_growth | **workload_growth** | 0.90 | matches label |
| probe-leak | leak_suspicion | unknown at 899 s | 0.00 | withheld, threshold is 1200 s |
| probe-sawtooth | cache_or_retrievable | unknown at 584 s | 0.00 | withheld |
| probe-plateau | cache_or_retrievable | unknown | 0.00 | withheld |

The children verdict is the notable one. Its basis:

> group probe-children grew by 5 MB — growth from accumulating subprocesses
> (confidence 0.60; basis: [process count reached 21 (high-water) with growth
> not correlated to activity (corr 0.00)])

Twenty-one real child processes, recognised as accumulation rather than as a
leak. Before the fixes this bucket existed in the type system and was never
assigned anywhere.

No probe advisory was issued for the cache-shaped groups, and no leak advisory
fired before the evidence threshold. The earlier behaviour — a warning every
window about Chrome and node — is gone in this run.

The remaining three verdicts need the 1200 s evidence threshold. This file will
be replaced by the scored result when the run ends.
