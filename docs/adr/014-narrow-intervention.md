# ADR-014 — Narrow intervention: T4 Cooperative Release Framework + L2 managed-only stop

**Status:** Accepted
**Date:** 2026-10-23
**Related:** ADR-006 (reasoning retained), ADR-011 (heap migration rejected), ADR-012 (firm, untouched), ADR-013 (tiers), SI-1/SI-1a, `../00-product/v1-scope-contract.md` §5 (widening procedure — this ADR *is* step (a))
**Amends:** `v1-scope-contract.md` §2 (T4 row, new L2-managed row), `protection-actions.md` §4 (+§2.5), `action-authorization.md` §§1–2/5, `safety-invariants.md` SI-1a, `safety-tests.md` (ST-1 carve-out, new ST-23/ST-24)

---

## Context

ADR-013 froze v1 as prevention-and-advice: no executor path touches another process's memory, and T3 `act` has no code path at all. That left v1 unable to relieve *current* pressure by any means — only to prevent future load (T2) or ask the user (T5). The owner accepted a **narrow widening**: two capabilities with bounded blast radius, each gated so that a misconfiguration degrades to inaction, never to work loss.

The widening procedure in `v1-scope-contract.md` §5 requires (a) an ADR naming the capability and its evidence, (b) a safety-suite update covering the new path, (c) benchmark/canary evidence per ADR-013. This ADR is (a). (b) ships in the same batch (ST-1 carve-out, ST-23, ST-24). (c) remains open — **no new executor path runs outside `observe` until canary evidence exists**, per the mode rules below.

## Decision

Admit exactly two narrow capabilities into v1 scope. Everything else — L3 allowlist-terminate, L4 automatic CRITICAL intervention — stays outside v1 with no executor code path (unchanged from ADR-012/013).

### 1. T4 — Cooperative Release Framework (was: empty extension point)

An adapter protocol through which an application (or its agent) **volunteers** to release memory on request. The app decides what to release; the daemon only asks, waits, and measures.

Naming (owner decision): the action is `RequestCooperativeRelease`. The old generic name `RequestReclaimFromProvider` is retired repo-wide — one name for one mechanism, no aliases.

Framework rules (all binding):

1. **Registration, not discovery.** An adapter registers with a named capability (`cooperative-release`), a documented vendor API it calls, and a declared `EffectRange` source (a prior measurement, cited). Unregistered target ⇒ the decision records `unsupported` and falls back to T5 advisory. There is no generic "ask any app" path.
2. **Explicit per-integration opt-in**, revocable at runtime without daemon restart (SI-1a items 1/5, retained).
3. **Bounded attempt:** one request timeout (default 30 s, constants), at most 2 attempts per decision, attempts separated by ≥ 5 min. A timeout is `timed_out`, never success.
4. **Own budget:** T4 has its own rate-limit/circuit-breaker counters, separate from T1/T2/T5 (MP-REQ-044 covers per-class breakers; T4's class is `cooperative-release`). A breaker opened on T4 never blocks T2 or T5.
5. **Verdict only by measurement.** `effective | partial | ineffective` require `window_s` + `observed_effect_bytes` (schema-enforced, as for all actions). Additionally for T4/L2 the verification SHOULD compare `phys_footprint` (where available) **and** system pressure state — RSS alone is not the effect metric, because a cooperative trim can move pages without moving RSS proportionally, and RSS movement without pressure movement is not relief.
6. **Verdicts available without a positive measurement:** `ineffective` (measured, no drop), `unsupported` (no adapter), `unverified` (measurement unavailable), `unknown_crash_window` (crash between intent and result). `success` is never reported from a bare acknowledgement — an adapter replying "ok" with no measurable drop is `ineffective`, and repeated `ineffective` opens the breaker (backoff ×2, cap 24 h).
7. **Modes:** `observe` never sends a request (planning only); `canary` sends within the strict budget with post-hoc comparison; `enforce` only after canary evidence (≥ 20 invocations, verified effect, no adverse events). Same ladder as the old provider rule — the framework changes *what can be called*, not *how promotion works*.

### 2. L2-managed — `StopManagedWorkload` (new, narrow)

The daemon may stop a process **iff all of the following hold**:

1. The daemon itself spawned it through a managed launch point (`mp run`) — the daemon is the recorded parent at spawn time (managed-set membership, persisted in the state file).
2. The user gave **prior authorization** for managed-stop on that launch (per-invocation `--allow-stop` or a recorded class policy; a bare `mp run` without it is stop-ineligible).
3. **Identity revalidation at execute time:** `(pid, start_sec, start_usec)` must still resolve to the same process AND the daemon must still be its parent. PID reuse or reparenting ⇒ `refused(precondition: identity)`.
4. Verification window 60 s: the process exited AND its RSS was reclaimed AND no child of it remains holding > 10 MB (orphan check — stopping a parent that leaves heavy children is `partial` at best).
5. Own breaker class (`managed-stop`), same backoff shape as §1 item 4.

**No `SuspendManagedWorkload` in v1.** Suspend freezes work without freeing RAM — it has no measured relief effect, so it cannot pass the provider bar (ADR-006 §investigation plan, still in force). If a future measurement shows a suspend-then-pressure-drop effect with a bounded resume cost, that is a separate ADR, not an interpretation of this one.

This is compatible with SI-1 as written: SI-1 forbids signaling "any process **other than one the daemon itself spawned**" — managed-stop operates strictly inside the carve-out. What changes is ST-1, which previously scanned for *any* signal primitive: it must now distinguish owned-PID signaling (the single managed-stop call site, audited) from general signaling (still forbidden). See §(b) below.

### 3. Explicitly not admitted (unchanged)

| Proposal | Status | Why |
|---|---|---|
| L3 terminate-any-process-in-allowlist | **out** | no false-positive corpus exists (ADR-013 revisit trigger unmet) |
| L4 automatic CRITICAL intervention | **out** | this is what ST-22 forbids; needs ≥ 2-week canary with no adverse events first |
| Suspend as relief | **out** | no measured effect; freezes work without freeing RAM |
| Coercing an unregistered app ("universal heap release") | **impossible, not just out** | no such macOS API exists; any doc implying it is fiction |

## Consequences

**+** v1 gains its first R-capability (capability matrix): measured relief through cooperation, and bounded stop of workloads the user explicitly placed under management.
**+** Both paths degrade to inaction under uncertainty (unsupported → advisory; identity mismatch → refusal; breaker open → refusal).
**−** ST-1 becomes a smarter check (owned-PID carve-out) — a weaker syntactic gate that must be compensated by ST-24's behavioral tests. The residual risk (a bug addressing the wrong PID) is contained by precondition 3's revalidation, not by the scan alone.
**−** `RequestReclaimFromProvider` rename touches schemas, fixtures, SCN-006 prose, and `appendix/schemas.md`. Mechanical, but must be complete — a stale reference is a contract bug. The validator run after the rename is the proof.

## (b) Safety-suite changes shipped with this ADR

- **ST-1 amended:** the AST/dependency scan allowlists exactly one owned-PID signal call site (`internal/executor/managedstop.go`, function `stopOwnedChild`), which must take a validated `OwnedPid` type — never a raw PID — and must be reached only after the identity revalidation. Any other signal primitive anywhere ⇒ build fails.
- **ST-23 (T4 path):** unregistered target ⇒ `unsupported` + T5 fallback, no request sent; adapter "ok" with no measured drop ⇒ `ineffective` + breaker progress; breaker open ⇒ `refused(breaker_open)` with zero adapter calls (spy adapter); repeated `ineffective` ⇒ backoff ×2.
- **ST-24 (L2 path):** stop of a non-managed PID ⇒ refused; stop after simulated PID reuse (same pid, different start time) ⇒ `refused(precondition: identity)`; stop leaving a > 10 MB child ⇒ verdict `partial`, not `effective`; assert no `Suspend*` variant exists in the `Action` sum type (golden test — same mechanism as ST-2).
- **ST-22 unchanged and still green:** the forbidden scenario (4 GB agent, `enforce`, no authorization) must still produce zero signals and an advisory. Managed-stop authorization is per-launch; the ST-22 fixture grants none.

## (c) Evidence still required before any new path runs live

Per the mode rules (§1 item 7, §2): both paths plan in `observe` from day one, but execute only in `canary` after, and `enforce` only after, the canary evidence bar (constants: ≥ 20 invocations, verified effect, no adverse events). SCN-006's stub-provider logic applies unchanged to the renamed action.

## Revisit triggers

- A measured suspend-then-relief effect with bounded resume cost ⇒ separate ADR for `SuspendManagedWorkload`.
- A per-class false-positive corpus for orphan termination ⇒ ADR-013 amendment for T3 `act` (independent of this ADR).
- An adapter that proves unreliable (high `ineffective` rate) ⇒ revocation procedure, not a framework change.
