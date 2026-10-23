# ADR-013 — Action policy revision: tiers and orphan handling

**Status:** Accepted (v1 scope only)
**Date:** 2026-10-23
**Related:** ADR-006, ADR-012, `../06-problem-first/protection-actions.md`, `../06-problem-first/system-protection-core.md`

**ADR-012 status clarification:** ADR-012 (Accepted, firm) forbids adding process-termination capability to the shipped binary *as a general power*. This ADR does not overturn that. T3 `act` (`RequestGracefulQuit`, `TerminateVerifiedOrphan`) is **designed but not compiled into v1**: the schema can describe it, the policy can reason about it, but the executor has no code path that performs it, and ST-1/ST-2 still scan the binary for any such path. Turning `act` on for any class requires a separate ADR amendment with measured false-positive data. See `process-relief-engine.md` §0.

---

## Context

ADR-006 (v1 allowlist = self-only) and ADR-012 (no process-control capability) were written when the product's only answer to "what can it actually relieve?" was "itself, or nothing".

The Problem-First audit (`../06-problem-first/problem-coverage-matrix.md` §2) showed that decision makes the product unable to touch RC-1 through RC-4 — every cause that actually produces the dialog.

ADR-012's *reasoning* remains correct: killing an arbitrary process because it is large destroys work. But refusing in advance to design *any* new relief path — including refusal-side prevention (T2) and opt-in orphan reaping (T3) — turned a safety decision into a product ceiling.

This ADR revises the action space **without weakening ADR-012's core guarantee**.

---

## Options

**A. Keep the self-only allowlist unchanged.** Safest, weakest. RC-1..RC-4 stay unpreventable-by-design.

**B. Permit everything with a policy flag.** Restores leverage, destroys the product's central promise. Rejected.

**C. Add tiers by *what can break*, with independent defaults.** T1/T2/T5 on (break nothing), T3 detect+recommend on with `act` requiring explicit per-class opt-in, T4 empty. The nervous system rewrites; the invariants do not move.

---

## Decision

**C.**

Concretely:

1. **T1, T2, T5: on.** Cannot destroy work (T2 defers a request; nothing was running). T5 is advice.
2. **T3: detect + recommend on; act off.** Terminating even a likely orphan requires explicit user opt-in **per process class**, verified identity, and a recorded evidence set. There is no global "allow orphan killing" switch.
3. **T4: empty.** No API exists. The tier is a labeled extension point.
4. **ADR-012 stays.** No signal/suspend/freeze/terminate capability is added to the binary *unless and until* ADR-013 is amended for a specific class on the basis of measured false-positive data. A capability compiled in but "disabled by default" violates SI-1; a capability not compiled in cannot be reached.

## Evidence

- `problem-coverage-matrix.md` §2: the gap analysis.
- `research-feasibility-matrix.md` §6: the ranking ((effect × feasibility) / risk).
- Q-18 (idle-window measurement) and the orphan false-positive corpus: **open**. These are what would be needed to move T3 `act` on for any class, and they do not exist yet.

## Consequences

**Easy:** T5 and T2 can be built now; they require no capability the product has forbidden.
**Hard:** T3 `act` needs data that only exists after running T3 `detect` in production for months.
**Risk this ADR accepts:** T2 only works for launches that pass through a managed point. Stating that boundary honestly is part of the decision.

## Revisit triggers

- A measured false-positive rate for orphan detection on real traces → reconsider T3 `act` for that class only.
- A documented app API for cooperative release → populate T4 for that app only.
- Proof that T2 coverage is too low in practice → redesign the launch-point surface.
