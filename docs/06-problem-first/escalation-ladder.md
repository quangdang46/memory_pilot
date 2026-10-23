# Escalation Ladder — five severity levels

**Status:** Draft
**Date:** 2026-10-23
**Companion to:** `protection-actions.md` (tiers by *what can break*) — this document is levels by *how bad it is*.

---

## 0. How the two scales combine

Two orthogonal axes. Confusing them is how products either act too early or too late.

| Axis | Question | Values |
|---|---|---|
| **Severity** (this doc) | how bad is the situation right now, and how fast is it getting worse? | L1 … L5 |
| **Tier** (`protection-actions.md`) | what may we do about it, given what it could break? | T1 … T5 |

**Severity selects the eligible tiers; the tier's own default still applies.** A high severity never unlocks a tier that the user has not enabled.

```
severity L4  ──eligible──►  T1, T2, T5        (T3 if opted in, T4 if a provider exists)
severity L5  ──eligible──►  T1, T2, T5, T3*   (*only if the user enabled T3 for that class)
```

Raising severity **never** overrides `T3 = off`. If it did, the opt-in would mean nothing.

---

## 1. L1 — Anomaly detected

**Trigger.** One identity or group shows sustained growth across several samples, **or** a step change whose magnitude is material, **and** system pressure is still normal.

**System state.** macOS `normal`; forecast may or may not be actionable; no dialog.

**What mp does.**
- Record the finding with confidence and evidence.
- Increase sampling detail for the affected group (within budget).
- Prepare a protection plan (compute what would happen at the next level) **without acting**.

**What mp must not do.**
- Conclude "leak". Growth is a symptom, not a diagnosis.
- Warn the user loudly. Most L1 events self-resolve.
- Touch anything.

**Exit.** Growth stops ⇒ back to baseline, record the episode. Pressure reaches `watch` ⇒ L2.

---

## 2. L2 — Limit further growth

**Trigger.** Pressure reaches `watch`, **or** the forecast becomes actionable with a horizon that matters, **or** swap rate turns positive and sustained.

**The core insight at this level:** if the cause is *aggregate demand*, the cheapest effective action is **not adding more demand**. That does not disturb any running work.

**What mp does.**
1. **T2 — admission control becomes active.** New work routed through a managed launch point is admitted, warned, or deferred per `admission-control.md`.
2. T5 advisory: what is growing, by how much, how fast, and what the forecast says.
3. T1 if our own footprint is material.
4. Tighten its own I/O and sampling cost (SI-6).

**What mp must not do.**
- Stop, pause, or close anything already running.
- Claim it has reduced pressure. It has not — it has avoided *increasing* it.

**Honest limitation to state in the report:** T2 has no effect on memory already allocated, and no effect on launches that bypass the managed path.

**Exit.** Pressure returns to `normal` and the trend flattens for a dwell period ⇒ L1/baseline. Pressure reaches `prevent` ⇒ L3.

---

## 3. L3 — Reclaim what is safely reclaimable

**Trigger.** Pressure `prevent`, or a critical forecast within the actionable horizon, with T2 already active.

**What mp looks for, in order:**

| Candidate | Available? | Notes |
|---|---|---|
| Its own buffers, history, log | ✅ always | small effect; must never be presented as relief for the system |
| Verified orphan processes | ⚠️ only with evidence | detect + recommend by default |
| Cooperative app release | ❌ no API known | tier T4 is empty |
| Kernel reclaim (purge-style) | ❌ rejected | no measured benefit; plausible cost (ADR-006) |

**What mp does.**
- T1 in full.
- T3 **detect**: enumerate orphan candidates with the evidence set from `protection-actions.md` §3.3; if T3-recommend is enabled, surface the specific advisory.
- State plainly, in the report, if there is nothing safe to reclaim.

**What mp must not do.**
- Purge another app's cache. There is no general, safe way to do it.
- Present its own 40 MB trim as meaningful progress against a 2 GB gap.

**Exit.** Pressure falls to `watch` ⇒ L2. Reaches `critical` ⇒ L4.

---

## 4. L4 — Controlled intervention on the suspect workload

**Trigger.** Pressure `critical`, **or** `prevent` sustained beyond a dwell period, **and** L1–L3 measures have not changed the trajectory.

**At this level mp must be able to answer, for the dominant grower:**
- which process tree and which group,
- whether it is a root or a child,
- whether it appears to be doing work (CPU, I/O, child spawns, recent output),
- whether a safe reduction path exists (cooperative API, resumable session, lower-cost profile).

**What mp may do.** Only what is enabled:

| Path | Requires |
|---|---|
| Ask the app to reduce (T4) | a verified provider — **none today** |
| Recommend closing a **resumable** session (T5) | an adapter confirming the resume mechanism; the core alone must not claim resumability |
| Recommend killing a specific session (T5) | nothing — but the advice must state that work will be lost |
| Terminate a verified orphan (T3 act) | explicit user opt-in for that class |

**What mp must not do.**
- **Pause or freeze** a process as a general remedy. It does not release memory, and it destroys responsiveness — the worst of both (see ADR-012 rationale, still valid).
- Kill a process because its footprint is large.
- Claim it has fixed a leak inside the application. It cannot.

**Exit.** Pressure falls ⇒ L3/L2 as it recovers. Sustained `critical` with degradation ⇒ L5.

---

## 5. L5 — System protection

**Trigger.** Pressure `critical` **sustained**, with at least one of: swap still growing, measurable responsiveness collapse, or the dominant grower still accelerating, **and** L4 had no safe path or its advice was not taken.

**This level is a decision point, and the product must present it as one** rather than pretending a safe automatic action exists.

| Option | Condition |
|---|---|
| Continue limiting new work | whenever T2 can still prevent additional growth |
| Ask the user to stop a specific workload | default when nothing else safe remains |
| Terminate a workload **the user previously authorized** | only with opt-in, verified identity, and acknowledged work-loss risk |
| Kill whatever uses the most RAM | **never** — not even at L5 |

### 5.1 If the user has authorized termination for named workloads

Then, and only then:

1. Re-verify identity `(pid, start_sec, start_usec)` immediately before acting.
2. Prefer a graceful request (SIGTERM) over SIGKILL.
3. **Do not escalate to SIGKILL automatically** if the process ignores the request. Record that it did not exit and hand the decision back.
4. Record the action, the evidence, and the outcome.
5. Never auto-resume or restart it.

### 5.2 The honest statement at L5

If a process consumes memory faster than the system can cope, someone must choose between keeping that workload and protecting the machine. `mp`'s job is to make that choice **explicit, informed, and attributable** — not to make it silently.

> We cannot guarantee every job is saved. We can guarantee that when something is sacrificed, it was chosen deliberately, with evidence, by someone who knew the cost.

---

## 6. De-escalation

Escalation is fast; de-escalation is deliberately slower, to prevent flapping.

| Transition | Dwell before de-escalating |
|---|---|
| L5 → L4 | sustained improvement + one complete healthy sample |
| L4 → L3 | 5 min below the L4 trigger |
| L3 → L2 | 5 min below the L3 trigger |
| L2 → L1 | 15 min below the L2 trigger |

**Never de-escalate on missing evidence** (SI-4). If a metric goes `unavailable` while at L4, stay at L4.

Every transition is recorded with the inputs that caused it, so `mp explain` can reconstruct why the level changed — including the case where it changed because a source went dark.

---

## 7. What the five levels map to, in the reference vocabulary

| Level | Pressure state (from estimator) | Output mode |
|---|---|---|
| L1 | `normal`, trend rising | observe |
| L2 | `watch` | protect (T2 active) |
| L3 | `prevent` | protect + reclaim attempts |
| L4 | `critical` or sustained `prevent` | protect + intervention |
| L5 | sustained `critical` + degradation | emergency policy |

The pressure state is an *input* to the level, not the same thing: a `critical` pressure with a known, resolvable orphan is L4 (act on it); a sustained `critical` with no safe path is L5 (decision point). **Level is a function of state × available actions × trend**, which is why it cannot be derived from the pressure state alone.

---

## 8. Tests per level

| Level | Scenario | Asserts |
|---|---|---|
| L1 | `leak-like` trace, pressure normal | detection, no warning, no action, episode recorded |
| L2 | `ramp-slow` | T2 defers a launch that would not fit; running work untouched |
| L3 | orphan-accumulation trace | candidates listed with evidence; no action unless opt-in |
| L4 | `ramp-fast` with a resumable session | recommendation includes the resume path **only via an adapter** |
| L5 | sustained critical, no safe path | explicit decision point; zero automatic kills without opt-in |
| all | de-escalation | dwell honored; never on missing evidence |

See `../05-testing/scenarios/` SCN-007 … SCN-011.
