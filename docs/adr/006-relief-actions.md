# ADR-006 — Which relief actions are real on macOS

**Status:** Accepted (reasoning) — **scope phrasing superseded by ADR-013; the provider bar below remains in force**
**Date:** 2026-10-23
**Related:** `../04-safety/action-authorization.md` §2.1, §3, ADR-013, `../00-product/v1-scope-contract.md` §2

---

## Context

The product's relief layer needs at least one mechanism that measurably reduces memory pressure without harming the user's work. Candidate mechanisms, and what is actually known about each:

| Mechanism | What it would do | What we know | Verdict |
|---|---|---|---|
| Trim the daemon's own buffers | free our own memory | trivially true; tiny effect | **include** |
| Rotate our own ledger | free disk | helps swap headroom, not RAM | **include** (disk only) |
| Ask an app to release caches via its API | app-specific | no universal API; per-app integration, per-app verification | **investigate** |
| `memory_pressure`-style purge request | ask the kernel to reclaim | effect disputed; likely frees file cache (no pressure benefit) and warms nothing; possible latency penalty afterwards | **reject for v1**, investigate (see below) |
| Close/suspend an app | free its memory | destroys work | **forbidden** (ADR-012) |
| Grow swap | more swap space | not permitted to third parties; and it is not memory | **impossible** |
| Compress more | not exposed | — | **impossible** |

**Why purge is rejected for v1.** Purge-like operations free *file cache*. Under memory pressure, macOS already evicts file cache before compressing or swapping; purging ahead of time therefore trades a benefit the OS would have taken anyway for an immediate cost (the cache must be re-read). The one scenario where it might help is a long-running cache-heavy workload that has accumulated cache it "should" release — but the OS's own reclaim already handles that under pressure. Until measured otherwise, purge is at best neutral and at worst harmful.

---

## Decision

**v1 allowlist (amended by ADR-013 tiers — this section superseded in scope, reasoning retained).**

At the time this ADR was written:

```
TrimOwnBuffers
RotateOwnLedger
EmitAdvisory
```

That was the complete set: *reporting* plus *self-shrinking*. The reasoning below remains valid for **other-process memory** — a user-scope daemon cannot reach into another app's heap and free it.

ADR-013 revised the relief space without touching that reasoning: the added v1 capabilities are not memory-freeing mechanisms at all. **T2** (`DeferLaunch` via `mp run`) prevents *new* demand from arriving; **T3** detect+recommend names orphan candidates for the *user* to act on; **T5** (`RecommendCloseSession`, `RecommendReduceConcurrency`, `RequestReclaimFromProvider`) advises. None of them frees another process's memory, so none of them needed the provider bar this ADR defines. T3 `act` — the one thing that *would* touch another process — still needs a measured false-positive corpus per class (ADR-013 §Revisit triggers) and does not exist in the v1 executor.

The current v1 scope lives in ADR-013 and `../06-problem-first/protection-actions.md`; this ADR's investigation plan (§below) still governs any future *provider* (a mechanism claiming to free or return memory held by another application).

---

## Investigation plan for a future provider

To promote a mechanism to a provider it must pass:

1. **Controlled experiment.** Induce pressure with a test allocator (not real workload). Run the mechanism. Measure `vm_stat` before/after at 30 s and 5 min, plus a UI-latency proxy.
2. **Cost measurement.** CPU, I/O, and the time for caches to re-warm.
3. **Repeatability.** The effect must reproduce ≥ 5 times, and the sign must not flip.
4. **Bounded risk.** Documented worst case (e.g. "purge can add up to X ms of first-access latency").
5. **Registration** with an `EffectRange` derived from the experiment, plus a verification method.
6. **Canary** for at least 2 weeks before enforce.

If no mechanism passes, the allowlist stays self-only. **That is an acceptable product outcome**, not a failure to be papered over with a `--force` flag.

---

## Consequences

**+** The product cannot harm the user's work by construction; every released action is proven.
**−** Users expecting a "fix it" button will be disappointed. The docs must say clearly what the product does and does not do (charter L1).
**−** The product's value rests entirely on prevention and explanation, which means the accuracy of the forecast and the quality of the reports carry the whole weight. That is the correct place for the effort to go.

## Revisit triggers

A measured, repeatable mechanism with a documented cost is found — most plausibly an app-cooperative one (ADR-011b) or a kernel interface Apple ships later.
