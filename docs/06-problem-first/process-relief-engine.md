# Process Relief Engine — quit/kill with memory-pressure awareness

**Status:** Draft · this document ADOPTS the pt-style approach the user approved, with the safeguards below
**Date:** 2026-10-23
**Related:** ADR-012 (stays), ADR-013 (amended by this doc), `protection-actions.md` T3, pt `reference-audit-process-triage.md`

---

## 0. What changed, and why it does not contradict ADR-012

ADR-012 forbids a general kill capability: *no terminating a process because it is large, quiet, or inconvenient*. This document adds a **narrow, named, gated** capability: terminating a specific class of process (verified orphan of a defined class, or a process the user explicitly pre-authorized) through an explicit pipeline with verification. The two coexist because:

- The **forbidden thing** is an open-ended "kill whatever" power.
- The **permitted thing** is a closed set of actions (`RequestGracefulQuit`, `TerminateVerifiedOrphan`), each with its own evidence bar, gate, and audit trail — and `act` stays **off** until false-positive data exists.

If a future reader finds this contradictory: the contradiction is resolved in `protection-actions.md` §3 — the tier is *detect + recommend on, act off*.

**Designed vs compiled, stated precisely (see ADR-013's ADR-012 clarification):** this document, the decision schema, and the policy layer may all describe and reason about `RequestGracefulQuit` and `TerminateVerifiedOrphan` — that is "designed". None of that makes the capability exist in the shipped binary. The v1 executor has no function that sends a signal to another process; there is no code path to compile out, because it was never written in. ST-1/ST-2 (`safety-invariants.md` §4) scan the dependency graph and source for termination primitives and fail the build if one appears outside test code — so "designed but not compiled" is a build-verifiable claim, not a policy promise. Flipping a class from `act: off` to `act: on` requires writing that executor code for the first time, under a separate ADR amendment backed by measured false-positive data (ADR-013 §Revisit triggers) — it is not a config flag on existing code.

---

## 1. The pipeline

```
Detect → Classify → Estimate Impact → Select Action → Safety Gates → Quit → Verify → [Force Kill] → Verify
```

Each stage is a pure function of evidence; only the last two touch the system.

### 1.1 Detect — Candidate Detector

Finds processes that *might* be releasable, using only observation:

| Evidence | Weakness alone |
|---|---|
| Large footprint | says nothing about need |
| Fast sustained growth | may be legitimate work |
| CPU ~ 0 for a long window | may be waiting on I/O / network / human |
| Parent identity gone | may be an intentional detach |
| No recent activity | quiet ≠ done |
| Belongs to a finished workload | only useful if verified independently |
| Holds shared files/sockets | raises the cost of being wrong |

**No single signal is sufficient.** A candidate is a process with several independent signals, each recorded. A candidate list is not a kill list.

### 1.2 Classify — four states, not one score

| State | Meaning | Default routing |
|---|---|---|
| `useful` | active work, or insufficient evidence to say otherwise | observe |
| `suspicious` | anomalies present, alternatives not excluded | recommend only |
| `abandoned` | strong evidence no live work depends on it | recommend; act only with opt-in |
| `reaped` | already gone (zombie / exited but uncollected) | no action needed |

**Default is `useful`.** The burden of proof is on moving *away* from useful, and the evidence for each transition is stored.

### 1.3 Estimate Impact — three separate numbers, never one score

| Measure | Meaning | Why separate |
|---|---|---|
| **Abandonment confidence** `[0,1]` | how sure we are it is not needed | drives the gate |
| **Interruption risk** `[0,1]` | what breaks if we are wrong | drives the default (high risk ⇒ recommend only) |
| **Expected memory relief** `[bytes, uncertainty]` | what we gain if we are right | drives the *priority*, never the *permission* |

A 15 MB abandoned process is cheap to close and nearly useless against a 4 GB shortfall. A 4 GB busy process is dangerous to close and solves the shortfall. **Merging these into one ranking produces exactly the wrong action in both cases.** The three numbers are reported together and decided on separately.

### 1.4 Select Action — four levels

| Level | Action | Condition |
|---|---|---|
| L0 | Observe | default |
| L1 | Recommend | suspicious or abandoned without opt-in |
| L2 | Graceful quit | abandoned + opt-in for the class + all gates pass |
| L3 | Force kill | L2 attempted and failed + still eligible + all gates pass |

**No auto-kill by default.** The engine ships complete through L1; L2/L3 require policy the user has explicitly enabled, per process class.

### 1.5 Safety gates — all must pass, rechecked at execution time

1. Identity re-verified: `(pid, start_sec, start_usec)` read live, immediately before acting.
2. Not in the protected set (terminals, shells, session managers, IDEs, system services, user-marked groups).
3. Not a coding agent, unless the class is explicitly one the user authorized.
4. No shared-file/socket hazard unresolved; launchd-service check passed.
5. Rate limits: per-class kills per hour + expected-relief cap per incident.
6. No respawn loop: if the supervisor recreates the process, stop retrying and report.
7. Recorded evidence ≥ the class threshold, and the record is attached to the decision.

A gate failure is a *refusal with reason*, recorded — never a silent skip.

### 1.6 Quit, then verify — then optionally force-kill, then verify again

```
1. re-verify identity + protection + gates      (live state)
2. send the graceful request appropriate to the process type
3. wait, bounded (default 10 s)
4. check: did it exit?  → if yes: verify relief, done
5. if no, and L3 is enabled for this class: consider SIGKILL
6. check again; record whether the process actually exited
7. verify: did pressure/footprint actually fall?
```

**Sending a signal is not success.** Only the measured outcome counts (`verification.verdict`), per the schema rule already in place.

macOS note: a graceful request on macOS is process-type dependent (Apple Event quit for GUI apps where supported, SIGTERM otherwise). There is no single "quit" primitive; the engine must pick per type and must not assume delivery equals exit. This is macOS-specific work still open (Q-19).

---

## 2. What we take from pt, precisely

| pt mechanism | Adopted as | Status |
|---|---|---|
| Multi-evidence classification, four states | §1.2 | design |
| Evidence ledger / Bayes-factor-style explanation | the `basis` fields, already schema-required | design |
| `expected_loss`-style separation of P(abandoned) from P(useful) | §1.3 three numbers | design |
| Revalidation immediately before acting | gate 1 | design |
| Protection before scoring, per-rule exclusion counts | gate 2 | design |
| Rate limits + respawn-loop guard (begin-intent accounting) | gate 5–6 + intent ledger | design |
| Shadow → confirmed → restricted automation | `protection-actions.md` rollout | design |
| Loss-matrix numbers, Bayesian posteriors, cgroup actions, Linux-only paths | — | **rejected** (not portable, uncalibrated for this workload) |

**pt is Linux-first.** Its README documents that macOS deep inspection is limited and that cgroup actions (freeze, throttle, quarantine) do not port. This engine is specified against macOS APIs only (`proc_pidinfo`, libproc identity, launchd service checks); nothing is copied from pt's execution paths.

---

## 3. Memory Relief Value — why the engine exists at all

The one thing a triage tool does not compute and a pressure controller needs: **how much does this action help the actual problem?**

```
relief_priority = expected_relief_bytes / interruption_risk    (guarded: risk == 0 ⇒ recommend-first)
```

A candidate with high abandonment confidence but 15 MB of relief is a cleanup nicety, not a crisis response. The engine reports both, so that at L4 the user can see: "closing these three verified orphans frees ~400 MB; the gap is ~2 GB; this will not suffice."

**Honesty rule:** the engine must state when its best available action is insufficient. A kill list that "solves" an unfixable gap is the most dangerous output this module can produce.

---

## 4. v1 scope

| In v1 | Out of v1 |
|---|---|
| Detect + Classify + Impact (L0/L1) | L2/L3 execution |
| All safety gates implemented and tested | any automatic termination |
| Shadow comparison (recommendations vs. user judgment) | SIGKILL path |
| Orphan precision measured on real traces | macOS graceful-quit per-type matrix (Q-19) |

v1's deliverable is **a verified recommendation list with relief estimates**, not a killer. L2/L3 need ADR-013 amended with measured false-positive data.

---

## 5. Tests

| ID | Proves |
|---|---|
| T-PR-1 | a 15 MB abandoned process never outranks a 2 GB busy one for *permission* (only for *priority*) |
| T-PR-2 | identity reuse between plan and execute ⇒ refusal |
| T-PR-3 | a coding agent is never L2-eligible without an explicit class authorization |
| T-PR-4 | respawn loop stops after the cap and reports |
| T-PR-5 | signal sent ≠ success; verdict requires measurement |
| T-PR-6 | L3 requires a failed L2 first; no direct-to-SIGKILL path exists |
| T-PR-7 | orphan precision measured on recorded traces (the only number that can ever justify L2/L3) |

---

## 6. Open questions

- **Q-19:** Per-type graceful quit on macOS (Apple Event vs SIGTERM) — which processes support which, and how to verify exit.
- **Q-20:** Orphan false-positive rate on the owner's real workload (needs months of detect-mode data).
- **Q-21:** The resupply question for T3 `act`: which process classes, if any, should ever be auto-eligible — default answer stays "none".
