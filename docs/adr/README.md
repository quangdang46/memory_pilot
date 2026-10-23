# Architecture Decision Records

An ADR is written when a decision is **consequential, contested, or unverified**. If a decision is obvious and cheap to reverse, it does not need one.

Every ADR has a **status**:

| Status | Meaning |
|---|---|
| `Proposed` | written down, not yet decided |
| `Accepted` | decided; may still be based on unverified assumptions (see *Evidence*) |
| `Verified` | decided **and** backed by a measurement on the target machine |
| `Superseded by ADR-xxx` | replaced |
| `Rejected` | considered and declined, with reason |

The distinction between `Accepted` and `Verified` is the point of this directory. Most projects accept decisions on plausibility. This project requires anyone reading an architecturally load-bearing doc to be able to see whether the claim was *measured* or merely *argued*.

---

## Format

```markdown
# ADR-XXX — Title

**Status:** Proposed | Accepted | Verified | Rejected | Superseded by ADR-YYY
**Date:** YYYY-MM-DD
**Deciders:** …
**Related:** MP-REQ-xxx, other ADRs

## Context
What forces are at play. What we know, and what we only believe.

## Options
Each option with its actual cost, not a strawman.

## Decision
What we chose and why.

## Evidence
What we measured, or explicitly: "none yet — this decision is Accepted-but-unverified
because <reason>; the verification is <experiment>."

## Consequences
What becomes easy, what becomes hard, what we can no longer do.

## Revisit triggers
The conditions that would make us reopen this.
```

---

## Index

| ADR | Title | Status |
|---|---|---|
| [000](000-language-and-stack.md) | Language & stack: Go | Accepted (unverified) |
| [001](001-pressure-signal.md) | Memory-pressure signal: dispatch events + kernel level + polling | Proposed |
| [002](002-process-footprint-source.md) | Per-process footprint source | Proposed |
| [003](003-ledger-format.md) | Ledger format: JSONL vs SQLite | Proposed |
| [004](004-sampling-schedule.md) | Adaptive sampling schedule and floors | Proposed |
| [005](005-forecast-model.md) | Forecast model complexity | Proposed |
| [006](006-relief-actions.md) | Which relief actions are real on macOS | Accepted (reasoning; scope superseded by ADR-013) |
| [007](007-ipc-protocol.md) | CLI↔daemon IPC protocol | Proposed |
| [008](008-resource-budget.md) | Daemon resource budget | Proposed |
| [009](009-history-horizon.md) | History retention vs detection horizon | Proposed |
| [010](010-fallback-triggers.md) | Fallback and degraded-mode triggers | Proposed |
| [011](011-no-heap-migration.md) | Rejecting transparent heap migration / SSD-as-RAM | **Rejected (firm)** |
| [012](012-no-process-kill-capability.md) | No process control capability in the product | Accepted (firm) |
| [013](013-action-policy-revision.md) | Action policy revision: tiers and orphan handling | Accepted (v1 scope only) |
