# v1 Scope Contract — the single source of truth for what v1 is

**Status:** Binding on all other documents. Where another document's phrasing suggests a different v1 scope, **this document wins** — the other document is a bug, to be fixed (not debated).
**Date:** 2026-10-23 (amended: T4 framework + L2-managed per ADR-014)
**Amends-clarity-of:** ADR-006 (scope phrasing superseded, reasoning retained), roadmap Phase 4, `conversation-synthesis.md` §3.
**Preserved-by:** ADR-013 ("v1 scope only", Accepted).
**Widened-by:** ADR-014 (Accepted) — T4 empty → framework; new L2-managed stop row. This is the §5 widening procedure working as designed: ADR-014(a) + safety suite (b) ship now; canary evidence (c) remains open.

---

## 1. What v1 is, in one paragraph

Memory Pilot v1 is a **user-level macOS daemon** that watches memory pressure, forecasts it, attributes it to workloads, **prevents new avoidable load at its own managed launch points**, **asks cooperating applications to release memory through a registered adapter protocol**, **stops workloads the daemon itself spawned under explicit prior authorization**, **detects orphan processes and recommends** — never terminates them — and **advises the user** with evidence. It also shrinks its own footprint. It intervenes only through measured, gated, revocable paths; it never terminates a process it did not spawn.

## 2. The v1 action table

| Tier | Capability | Executor reach in v1 | Notes |
|---|---|---|---|
| T1 — self-maintenance | `TrimOwnBuffers`, `RotateOwnLedger`, `ReduceSamplingDetail` | **yes** | self-targeted only |
| T2 — admission control | `DeferLaunch` via `mp run` (managed points only) | **yes** | prevents *new* demand; frees nothing already running |
| T3 — orphan handling | `detect` + `recommend` | **yes** | lists candidates with the full evidence bar (§3.3 of `protection-actions.md`) |
| T3 — orphan handling | `act` (`RequestGracefulQuit`, `TerminateVerifiedOrphan`) | **no code path exists** | designed in schema/policy only; needs a separate ADR amendment with per-class false-positive data to write executor code for the first time |
| T4 — cooperative release | `RequestCooperativeRelease` through a **registered adapter** (ADR-014) | **yes — canary-gated** | the app decides what to release; `unsupported` → T5 fallback; effective/partial/ineffective only by measurement |
| L2-managed — owned-workload stop | `StopManagedWorkload` (ADR-014) | **yes — canary-gated** | only processes the daemon spawned via `mp run` **and** the user pre-authorized; identity revalidated at execute; no `SuspendManagedWorkload` in v1 |
| T5 — user advisory | `EmitAdvisory`, `RecommendCloseSession`, `RecommendReduceConcurrency` | **yes** | recommendations, never coercions; resume claims need a verified adapter |

**One-line test of understanding:** if a reader believes v1 can end, suspend, freeze, or signal any process **it did not spawn and the user did not pre-authorize for stop**, they have misread one of the other documents — this table is the correction. The single legal signal call site is `StopManagedWorkload`, on an owned PID, behind the identity revalidation in ADR-014 §2.

**Explicitly outside v1** (no executor code path, unchanged from ADR-012/013): L3 terminate-any-in-allowlist, L4 automatic CRITICAL intervention, suspend-as-relief, and any "universal heap release" claim (no such macOS API exists).

## 2b. Action-naming rule (owner decision, 2026-10-23)

One mechanism, one name, no aliases. The generic `RequestReclaimFromProvider` is retired; the cooperative path is `RequestCooperativeRelease` and the owned-workload path is `StopManagedWorkload`. Schemas, fixtures, SCN-006 prose, and `appendix/schemas.md` use exactly these names — a stale reference is a contract bug, caught by the validator run after the rename.

## 3. What v1 must do

1. Measure system pressure from the kernel source of truth (Dispatch mask + sysctl), never from a proxy alone.
2. Forecast time-to-critical with a calibrated lower bound; a warming/forecast-off state is displayable but never actionable.
3. Attribute growth to workload groups with identity `(pid, start_sec, start_usec)`; never sum RSS across processes and present it as one process's usage.
4. Gate new launches at managed points; never block on missing estimates, even at L4/L5 (escalate the warning instead).
5. Detect likely orphans, present evidence, recommend one-command user actions; never terminate.
6. Ask registered adapters to release memory; record `unsupported` when none exists and never imply a universal release capability.
7. Stop a managed workload only when it was spawned through `mp run`, the user pre-authorized stop, and its identity revalidates at execute time.
8. Advise on idle sessions and concurrency with stated strength; never imply resumability without a verified adapter, never imply conversation-resume preserves in-flight work.
9. Verify every executed action's effect by measurement — for T4 and L2 attest with `phys_footprint` where available plus system pressure state, not RSS alone; report `prevented (counterfactual)` only as unobserved, never as measured.
10. Record every decision, its evidence, its uncertainty, and its outcome in the ledger.

## 4. What v1 must never do

1. Send a signal, suspend, freeze, terminate, or restart any process **the daemon did not spawn under a managed launch point with prior stop authorization** — the single legal call site is ADR-014 §2, and ST-1 permits no other (SI-1, ST-1/ST-24).
2. Present schema-representability as capability: the decision schema can *describe* T3 act actions so policy logic can be designed and tested; the v1 executor cannot *perform* them. Any document, log line, or message that blurs this is a bug.
3. Report a cooperative release as successful from an adapter acknowledgement; only a measured drop counts.
4. Block a launch because the estimate is unknown.
5. Claim a suspect leaks, or a recommendation is safe, beyond the recorded evidence and alternatives.
6. Clear a warning on missing data; de-escalate without honoring dwell times.
7. Report success without a measurement.
8. Pretend T2 covers launches that bypass managed points.

## 5. How to change this contract

- **Narrowing** (removing a capability, e.g. dropping T2 deferral to warn-only): allowed by ADR amendment with the reason recorded.
- **Widening** (adding executor reach — the first executor code path for T3 act for any class, adding a T4 adapter with a real mechanism, adding any new externally-effective action): requires **all three**: (a) a separate ADR amendment naming the class and its false-positive evidence, (b) the safety-suite update that covers the new path (incl. an ST-22-style forbidden scenario for its misuse), (c) benchmark/canary evidence per ADR-013 §Revisit triggers. A policy-flag flip on existing code is **not** a widening path, because for T3 act there is no existing code.

## 6. Relationship to the other scope statements

| Document | Its scope statement is… |
|---|---|
| ADR-006 | reasoning retained; allowlist phrasing superseded (see §2) |
| ADR-013 | the tier revision this contract freezes |
| Roadmap Phase 4 | restated to match §2 (no "self-trimming only" phrasing) |
| `conversation-synthesis.md` §3 | restated to match §2 (no "nothing beyond self-trimming" phrasing) |
| Charter §3.5 | tiers mapped to categories A/B; C empty in v1 except the two ADR-014 paths (cooperation + owned-workload stop), which are B/A-side interventions that free no foreign memory without consent |
| ADR-014 | the narrow widening this contract records: T4 framework + L2-managed stop; L3/L4/suspend explicitly out |

New scope statements go **here first**, then propagate — never the reverse.
