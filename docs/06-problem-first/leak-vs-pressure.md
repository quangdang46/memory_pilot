# Leak vs Pressure — the distinction the whole product turns on

**Status:** Draft · this is the charter-level decision the next full-spec review will be judged against
**Date:** 2026-10-23

---

## 1. The two problems are different, and confusing them produces the wrong tool

| | **Memory leak** | **Memory pressure** |
|---|---|---|
| What it is | one process retains memory it no longer needs and keeps growing | the system no longer has enough headroom to meet current demand |
| Causes | a bug in one application's allocator or cache | a leak, **or** eight healthy sessions totaling more than the machine holds |
| Affected by | only the leaky process | *everything*, including the healthy ones |
| Fixed by | repairing or restarting the leaky process | reducing total demand or adding RAM |
| Can a third-party daemon fix it? | **no** (cannot repair another app's heap) | **partly** (can prevent new demand and advise demand reduction) |

**The decisive case:** a 16 GB Mac running eight healthy agents that genuinely need 2 GB each. Nothing is leaking. The system still dies. A leak detector would report "no leak" and watch the machine crash. A pressure protector would have said "you cannot fit session #8".

**Therefore: pressure, not leak, is the primary problem.** Leak detection is one input to pressure protection, not its goal.

---

## 2. The failure chain — and where each link is cut

```
memory retained
   │  (by a leaky process, or by many healthy ones)
   ▼
per-process growth
   │  cut here by ──►  Growth Detector (A3): shape, rate, burst
   ▼
aggregate demand approaches the budget
   │  cut here by ──►  Admission control (T2): don't start what won't fit
   │                 ──►  Session recommendation (T5): close what isn't needed
   ▼
headroom shrinks; forecast turns actionable
   │  cut here by ──►  de-escalation pressure via T2; honest warning
   ▼
kernel pressure warning; swap grows
   │  cut here by ──►  reclaim tiers (T1 always, T3 if opted in)
   ▼
kernel pressure critical; swap thrashing; latency collapse
   │  cut here by ──►  L4/L5 decision point: ask the user, with evidence
   ▼
macOS dialog; apps paused; work interrupted
```

Every link above L5 is prevention; L5 itself is damage control. **A product that only observes the chain has observed it.**

---

## 3. Growth is not a diagnosis

**A process growing does not mean it is leaking.** Three growth patterns that are almost always benign:

| Pattern | Meaning |
|---|---|
| Ramp then plateau | warm-up: indexing, cache fill, model load |
| Periodic sawtooth | GC cycles, periodic flush |
| Step after a command | allocation for the work the user just asked for |

**One pattern is worth suspecting:** unbounded, monotonic, uncorrelated with activity, with no plateau over a window that contains activity changes. Even then the conclusion is `leak_suspicion` with the evidence and the alternatives — never "leak". See `../03-algorithms/leak-suspicion.md`.

**The product rule:** suspicion is a reason to watch more closely (L1), not a reason to act. Action requires severity (the ladder) **plus** eligibility (the tier) **plus** a verified path. Suspicion alone authorizes nothing.

---

## 4. Anomaly → risk → action, immediately

> **"Phát hiện bất thường thì lập tức đánh giá và xử lý theo mức độ rủi ro."**

This is the loop latency requirement. It does not mean "act on every anomaly"; it means there is **no lazy stage in the pipeline**:

```
anomaly → risk assessment (this tick) → eligible tier (this tick) → action or explicit no-action
```

| What is forbidden | Why |
|---|---|
| A daily batch that "reviews" anomalies overnight | the machine crashes in minutes, not days |
| A dashboard that waits for a human to notice | the user runs sessions to do work, not to watch graphs |
| A queue where severity sits pending while the policy is recomputed | the recomputation is the response, not a preamble to it |
| Distinguishing "leak" from "legitimate growth" before acting | **the pressure response does not depend on the cause.** T2 gates a new launch whether the demand comes from a leak or from eight healthy sessions. Attributing the cause happens in parallel and sharpens the *next* decision; it never delays the *current* one. |

**The severity ladder (L1…L5) runs on pressure + trend, not on diagnosis.** Cause classification (`single_grower` / `aggregate_demand` / `orphan_accumulation` / `undetermined`) selects *which* tier within a level, not *whether* to respond to the level.

| Severity input | Diagnosis input |
|---|---|
| pressure state, trend, swap rate, headroom, forecast | identity trees, deltas, coverage |
| answers: **respond now?** | answers: **respond with what?** |
| allowed to be uncertain in the diagnosis | must never stall the severity response |

Concrete case: pressure is `critical` and growing, and the cause analyzer returns `undetermined` because half the processes are unreadable. The correct output is **L4 with an `undetermined`-cause advisory and the full eligible tier set** — not "wait for better attribution". Uncertainty in the cause constrains the *action*, never the *alarm*.

---

## 5. What mp can and cannot do about a leak

| It can | It cannot |
|---|---|
| Detect sustained, uncorrelated growth with evidence and confidence | Repair the leak (that is the application's code) |
| Force the leaky process's *future* demand to zero by not starting siblings (T2) | Shrink the leaky process's *current* heap |
| Recommend closing the session, with a resume path when an adapter verifies one | Restart the session without losing work (unless the resume path is proven) |
| Name the version, the history, and the repetition pattern so the bug can be reported upstream | Fix the bug |
| Protect the *rest* of the system from the leaky process (admission control + targeted advice) | Contain the leaky process's growth from outside |

**The mantra:** mp cannot cure a leak, but it can prevent a leak in one process from killing the other seven sessions' work. That prevention is the product — not a consolation prize.

---

## 6. The product goal, stated once

> **Build Memory Pilot as a background macOS daemon that lets a Mac sustain long workloads as long as physically possible — many applications and coding agents concurrently — by continuously monitoring system and process memory, detecting anomalies and leak signals early, assessing severity, and immediately applying the risk-appropriate protection: preventing the incident, reducing severe memory-pressure events, out-of-memory dialogs, app pauses, and work interruption — while preserving running work to the greatest extent possible.**

**Priority:** maximize stable operation time; act on risk as early as possible; preserve the user's work.

**Never:** terminate important work merely to free RAM.

**Success metrics:** fewer severe memory-pressure events, fewer out-of-memory dialogs, fewer app pauses, fewer interrupted jobs; faster detection-and-response to anomalies — with no significant side effects.

**Known limit (kept in the spec, not hidden):** mp can detect a leak and mitigate its consequences; it cannot guarantee repairing a leak inside every application. When no safe automatic action exists, mp must choose the least-risk option instead of an action that could destroy work.

---

## 7. What this changes in the existing spec

This document is a **charter-level amendment**. It binds the next full review:

1. `00-product/product-charter.md` — replace the success criteria with the goal above (owner's text, kept verbatim in §6).
2. Pressure response (the ladder in `escalation-ladder.md`) runs on **state + trend**, with diagnosis selecting the action, not gating the response.
3. `leak_suspicion` is an *input* to protection, never its objective. The leak-suspicion algorithm doc stays, but its role is renamed from "goal" to "sensor".
4. Every anomaly pipeline test must assert a **same-tick response**: either an action or a recorded explicit reason for inaction.
5. `Suspicion alone authorizes nothing` becomes SI-9 (a ninth invariant).
6. The benchmark protocol measures **time-to-mitigation from first signal**, not just detection accuracy.

---

## 8. Related

- `problem-coverage-matrix.md` — the eight root causes
- `escalation-ladder.md` — L1…L5 with de-escalation
- `protection-actions.md` — T1…T5
- `admission-control.md` — the primary lever
- `session-lifecycle.md` — T5 recommendations with resume
- `benchmark-protocol.md` — measuring what matters
