# v1 Scope Contract — the single source of truth for what v1 is

**Status:** Binding on all other documents. Where another document's phrasing suggests a different v1 scope, **this document wins** — the other document is a bug, to be fixed (not debated).
**Date:** 2026-10-23
**Amends-clarity-of:** ADR-006 (scope phrasing superseded, reasoning retained), roadmap Phase 4, `conversation-synthesis.md` §3.
**Preserved-by:** ADR-013 ("v1 scope only", Accepted).

---

## 1. What v1 is, in one paragraph

Memory Pilot v1 is a **user-level macOS daemon** that watches memory pressure, forecasts it, attributes it to workloads, **prevents new avoidable load at its own managed launch points**, **detects orphan processes and recommends** — never terminates — and **advises the user** with evidence. It also shrinks its own footprint. It is a prevention-and-advice product, not a reclamation product.

## 2. The v1 action table

| Tier | Capability | Executor reach in v1 | Notes |
|---|---|---|---|
| T1 — self-maintenance | `TrimOwnBuffers`, `RotateOwnLedger`, `ReduceSamplingDetail` | **yes** | the only tier that touches the machine directly |
| T2 — admission control | `DeferLaunch` via `mp run` (managed points only) | **yes** | prevents *new* demand; frees nothing already running |
| T3 — orphan handling | `detect` + `recommend` | **yes** | lists candidates with the full evidence bar (§3.3 of `protection-actions.md`) |
| T3 — orphan handling | `act` (`RequestGracefulQuit`, `TerminateVerifiedOrphan`) | **no code path exists** | designed in schema/policy only; ST-1/ST-2 verify absence at build; needs a separate ADR amendment with per-class false-positive data to write executor code for the first time |
| T4 — cooperative release | — | **nothing to reach** | tier is an empty extension point; no adapter exists |
| T5 — user advisory | `EmitAdvisory`, `RecommendCloseSession`, `RecommendReduceConcurrency`, `RequestReclaimFromProvider` | **yes** | recommendations, never coercions; resume claims need a verified adapter |

**One-line test of understanding:** if a reader believes v1 can end, suspend, freeze, or signal any process not owned by the daemon, they have misread one of the other documents — this table is the correction.

## 3. What v1 must do

1. Measure system pressure from the kernel source of truth (Dispatch mask + sysctl), never from a proxy alone.
2. Forecast time-to-critical with a calibrated lower bound; a warming/forecast-off state is displayable but never actionable.
3. Attribute growth to workload groups with identity `(pid, start_sec, start_usec)`; never sum RSS across processes and present it as one process's usage.
4. Gate new launches at managed points; never block on missing estimates, even at L4/L5 (escalate the warning instead).
5. Detect likely orphans, present evidence, recommend one-command user actions; never terminate.
6. Advise on idle sessions and concurrency with stated strength; never imply resumability without a verified adapter, never imply conversation-resume preserves in-flight work.
7. Verify every executed action's effect by measurement; report `prevented (counterfactual)` only as unobserved, never as measured.
8. Record every decision, its evidence, its uncertainty, and its outcome in the ledger.

## 4. What v1 must never do

1. Send a signal, suspend, freeze, terminate, or restart any process not owned by the daemon — **no code path for this exists**, so this is not a policy promise that can be misconfigured (SI-1, ST-1/ST-2).
2. Present schema-representability as capability: the decision schema can *describe* T3 act actions so policy logic can be designed and tested; the v1 executor cannot *perform* them. Any document, log line, or message that blurs this is a bug.
3. Block a launch because the estimate is unknown.
4. Claim a suspect leaks, or a recommendation is safe, beyond the recorded evidence and alternatives.
5. Clear a warning on missing data; de-escalate without honoring dwell times.
6. Report success without a measurement.
7. Pretend T2 covers launches that bypass managed points.

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
| Charter §3.5 | tiers mapped to categories A/B; C empty in v1 |

New scope statements go **here first**, then propagate — never the reverse.
