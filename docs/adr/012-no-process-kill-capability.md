# ADR-012 — No process-control capability in the product

**Status:** Accepted (firm)
**Date:** 2026-10-23
**Related:** SI-1, MP-REQ-040, MP-REQ-041, ADR-011

---

## Context

The owner's original framing drifted, in several iterations of discussion, toward "monitor memory, kill offenders when RAM is full". process_triage exists precisely to do that (Bayesian scoring of abandoned processes, staged SIGTERM→SIGKILL). It is well-built and its safety design is worth learning from.

It is still the wrong tool for this product, for a reason that is not about safety engineering:

**The set of processes that would be killed is exactly the set of processes whose state is most valuable.** A coding agent that has been reading, planning and editing for five hours holds that work in memory. A browser holds tabs, form state, and sessions. The processes that consume the most memory in this workload are the productive ones. A policy that trims memory by killing them optimizes for a number and against the user.

Secondary considerations (real but not decisive):

- Killing is *not* reliably effective: apps relaunch, or the pressure just moves to another process.
- The macOS kernel is better positioned to choose victims (it does, with jetsam), and it already does so — the dialog the user is complaining about is the kernel's own behavior. A third-party killer would make it worse.
- The user's actual request was, verbatim in spirit: "I don't want to keep clicking Resume/Quit; I don't want the memory to blow up, and I want my apps to stay smooth." Killing satisfies neither.

---

## Options

**A. Kill/suspend capability, off by default, available under `--force`.** The "common sense" option.

**B. Kill/suspend capability behind a separate binary/helper.** Keeps the gap technical, not moral.

**C. No such capability anywhere in the artifact.**

---

## Decision

**Option C.**

1. The action type is a sealed sum type with no process-control variant (`../04-safety/action-authorization.md`).
2. The safety suite statically forbids the syscalls (`../05-testing/safety-tests.md` ST-1).
3. The documentation states the limitation plainly rather than hinting at a future flag.
4. If the owner later wants a manual "terminate this process" utility, it is a **separate product** with its own spec, its own safety analysis, and its own binary. It will not be a flag on `mempilot`.

---

## Consequences

**Given up:** the ability to fix an acute crisis by force. In the worst case, the product can only advise, and the user must act.

**Gained:**
- The product is safe to leave running forever, on a machine with 10 valuable sessions, without the owner needing to trust a policy engine's judgment about which session matters.
- The safety analysis shrinks dramatically: there is no "did we kill the right process" failure mode to mitigate.
- The product's value proposition is forced to be honest: it must *prevent* and *explain*, because it cannot *coerce*.

**Accepted cost:** some users will ask for a kill button. The answer is "that's a different tool", documented in the FAQ.

## Revisit triggers

None. This decision is not expected to be revisited; it defines the product. If the owner asks for process control, that is a new project.
