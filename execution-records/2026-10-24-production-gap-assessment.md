# Response to production-readiness audit (2026-10-24)

Audit's four-level model accepted: implemented ✓ / tested ✓ / verified-on-device
✓(dev) / production-proven ✗. This record closes the actionable gaps and
states precisely what remains calendar-bound.

## Fixed in this commit (audit section C — doc inconsistencies)

1. **spec-completion-checklist G3–G6 rows updated** from design-era states to
   current evidence: G3 met (m00 scope decision: Sample/Incident/Advisory/
   Episode are runtime records, not core contracts — wire round-trip covers
   them); G4 met (ST-1..24 map in g-p4 record); G5 met (corpus v2 + naive-
   baseline + SCN green; real T4 provider absent by design); G6 met at dev
   level (crash-tail, clean restart, on-machine budgets; real-LaunchAgent
   lifecycle now ALSO tested — see below).
2. **SI-1 wording reconciled with ADR-014** in README.md + docs/README.md:
   the invariant is "no signaling of processes the daemon did not spawn";
   the single audited L2 exception (pre-authorized owned workload, one
   SIGTERM call site, ST-1-gated) is now stated everywhere SI-1 appears.

## LaunchAgent lifecycle test (release checklist item, clean run)

- `launchd.Install`: plist written to ~/Library/LaunchAgents, launchctl
  bootstrap OK (gui/501 domain, service visible).
- Second instance spawned by the agent exited on the SI-8 flock — single
  writer held by the running daemon; no dual-instance window.
- `launchd.Uninstall`: bootout + plist removed; original daemon unaffected
  and still serving IPC.
- Verified on-machine 2026-10-24 (~19:31 local).

## Release candidate

- RC commit: this commit's parent chain from 71329a1 (dev-frozen build);
  `make ci` green at RC.

## Remaining calendar-bound work (NOT closable by code)

Per audit section A/B — unchanged and honest:
- Full-duration G-P1 (24h), soak (72h), baseline (7d) + A/B/C on real
  workload, canary ≥20 real invocations for T4/L2 promotion.
- Daemon is running in canary mode and accumulating real data continuously;
  re-running the full-duration validations later requires only the existing
  tooling (bench/budget/*), no new code.
- T4 remains framework-only (no adapter) — a capability limit, not a bug.
