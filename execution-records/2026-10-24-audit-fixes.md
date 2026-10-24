# Execution record — fixes from external audit (2026-10-24)

External audit flagged 4 items; verified and resolved:

1. **Automation G-P1 existence**: CONFIRMED active via CronList in the owning
   session (id automation-9754d68b, fires 00:50 2026-10-25). The audit tool
   queried from a different session and saw 0 automations — cross-session
   visibility limitation, not a missing schedule.
2. **Bead count mismatch**: recount from .beads/issues.jsonl = 75 total,
   66 closed, 9 open. Earlier "50/59" was a mid-session snapshot before the
   remaining Phase 4/5/6 beads were closed. Current truth: 66/75.
3. **Missing bench tooling**: created `bench/budget/run_loaded.sh` (8h
   workload A/B/C: cache-growth allocator + subprocess churn + sampler) and
   `bench/budget/run_pressure.sh` (30min induced pressure, hard-capped
   MAX_GB, releases at end, records kernel pressure level alongside).
   run_pressure.sh smoke-tested 120s/1GB on the target Mac: daemon stayed
   at RSS 12.0 MB / CPU 0.001% while host pressure was induced; analyzer
   PASS. The 2026-10-24 validation-tooling record's "same contract" claim is
   now backed by actual files.
4. **Stale docs**: README "next: Phase 0 spikes" replaced with the real
   remaining work (long-run validations + freeze). execution-evidence.md
   rows for SCN-007..011, IT-11, ST-1/ST-2, ST-23/24 updated from
   "Designed / no source tree" to Compiled/tested with their records.
   All gates re-run green after edits.
