# Memory Pilot — Specification Set

**Status:** specification phase (no implementation yet)
**Version:** 0.1.0 · 2026-10-23

Memory Pilot is a macOS daemon that **prevents** memory exhaustion for long-running coding-agent workloads by measuring pressure correctly, forecasting it, attributing growth to workloads, and relieving pressure only through mechanisms proven safe — with **no ability to kill, pause, or suspend the user's processes**.

---

## Why this documentation exists first

The project owner's instruction was explicit: spend the effort on the specification, not on code that will be rewritten. This set is written so that:

- every requirement is falsifiable and has a test;
- every architectural decision is tagged `Proposed` / `Verified` / `Rejected`;
- claims taken from reference projects or from the design conversation are traceable to evidence, and everything unverified is marked as such.

A claim that cannot be verified is not stated as fact anywhere in this set. See `01-research/conversation-synthesis.md` §5 for the list of claims that were *not* verified and are therefore excluded from the specs.

---

## Reading order

| # | Document | Read it if you want… |
|---|---|---|
| 1 | `00-product/product-charter.md` | the problem, the user, success criteria, what this is not |
| 2 | `00-product/goals-and-non-goals.md` | decomposed goals and the 13 explicit non-goals |
| 3 | `00-product/requirements.md` | MP-REQ-xxx with acceptance criteria and verification method |
| 4 | `00-product/scenarios.md` | user scenarios, failure scenarios, anti-scenarios |
| 5 | `01-research/reference-audit-storage-ballast-helper.md` | what a working macOS pressure daemon actually does |
| 6 | `01-research/reference-audit-process-triage.md` | evidence/decisision/safety patterns worth adapting |
| 7 | `02-architecture/system-architecture.md` | layers, data flow, concurrency, state |
| 8 | `02-architecture/macos-integration.md` | the macOS APIs, verified field-by-field |
| 9 | `02-architecture/module-contracts.md` | Go interfaces per module |
| 10 | `03-algorithms/*` | pressure model, forecasting, attribution, leak suspicion |
| 11 | `04-safety/*` | invariants, authorization, protection, failure recovery |
| 12 | `05-testing/*` | strategy, replay corpus, fault injection, budgets, safety suite |

---

## Document map

```
docs/
├── 00-product/
│   ├── product-charter.md        MP-SPEC-001
│   ├── goals-and-non-goals.md    MP-SPEC-002
│   ├── requirements.md           MP-SPEC-003
│   ├── scenarios.md              MP-SPEC-004
│   └── roadmap.md                implementation phases and gates
├── 01-research/
│   ├── conversation-synthesis.md
│   ├── reference-audit-storage-ballast-helper.md
│   ├── reference-audit-process-triage.md
│   └── traceability-matrix.md
├── 02-architecture/
│   ├── system-architecture.md    MP-ARCH-001
│   ├── module-contracts.md       MP-ARCH-002
│   ├── runtime-and-concurrency.md MP-ARCH-003
│   ├── macos-integration.md      MP-ARCH-004
│   ├── persistence.md            MP-ARCH-005
│   ├── security-and-privacy.md   MP-ARCH-006
│   └── cli-surface.md            MP-ARCH-007
├── 03-algorithms/
│   ├── telemetry-model.md        MP-ALG-001
│   ├── pressure-estimator.md     MP-ALG-002
│   ├── forecasting.md            MP-ALG-003
│   ├── process-attribution.md    MP-ALG-004
│   └── leak-suspicion.md         MP-ALG-005
├── 04-safety/
│   ├── safety-invariants.md      MP-SAFE-001
│   ├── process-protection.md     MP-SAFE-003
│   ├── action-authorization.md   MP-SAFE-004
│   └── failure-recovery.md       MP-SAFE-005
├── 05-testing/
│   ├── test-strategy.md          MP-TEST-001
│   ├── replay-and-simulation.md  MP-TEST-003
│   ├── stress-and-soak.md        MP-TEST-004
│   ├── fault-injection.md        MP-TEST-005
│   ├── performance-budget.md     MP-TEST-006
│   ├── safety-tests.md           MP-TEST-SAFETY
│   └── scenarios/                per-scenario acceptance criteria
├── adr/                          architecture decision records
├── appendix/
│   ├── glossary.md               incl. the naming conventions table
│   ├── faq.md
│   ├── constants.md              every default, with a verified/provisional tag
│   └── schemas.md                explanatory notes on the JSON shapes
└── spec-completion-checklist.md  definition of "spec done" + gate status + open questions
```

```
schemas/                   ← NORMATIVE. Draft 2020-12, machine-checkable.
├── common.schema.json     Metric quality rule (SI-4), identity tuple, enums
├── forecast.schema.json   actionable ⇒ bound ∧ calibrated ∧ !fallback
├── pressure-state.schema.json
├── attribution.schema.json
├── classification.schema.json
├── decision.schema.json   closed action enum — no Kill/Signal exists
├── action-result.schema.json
├── state-file.schema.json
├── testdata/{valid,invalid}/   27 fixtures
├── validate.py            python -I -B schemas/validate.py
└── README.md
```

**Prose vs schema:** `appendix/schemas.md` explains *why* each object looks the way it does; `schemas/*.schema.json` is what actually validates. If they disagree, the schema wins and the prose is a bug.

---

## The eight invariants (the short version)

1. **SI-1 — No signaling capability.** There is no code path that can signal, suspend, or terminate a process the daemon did not spawn — not disabled by config, *absent*.
2. **SI-2 — Analysis cannot act.** Estimator/forecaster/attributor return values; only the executor acts, only on a closed allowlist, only after re-validating preconditions.
3. **SI-3 — No filesystem writes outside the daemon's data root.** No deletion of user data, ever.
4. **SI-4 — Missing evidence never improves the verdict.** A dead collector must never make the system look calm.
5. **SI-5 — Uncertainty is always visible.** No output can state a forecast, a cause, or a classification without confidence and evidence.
6. **SI-6 — Degrade before the host does.** Under pressure the daemon's sampling, detail, logging and retention shrink.
7. **SI-7 — Fail closed.** Any internal fault demotes to observe-only and stops acting.
8. **SI-8 — Single writer, single controller.** At most one daemon instance per user/host.

Full definitions with enforcement mechanisms and tests: `04-safety/safety-invariants.md`.

---

## The five things this product will not do

1. Kill, pause, freeze, or restart your applications.
2. "Free RAM" to make a number look better.
3. Hide macOS's out-of-memory dialog.
4. Move another app's memory to SSD.
5. Claim a process is leaking.

See `00-product/goals-and-non-goals.md` for the reasoning behind each.

---

## Status legend

| Tag | Meaning |
|---|---|
| `[Verified: sbh]` | Confirmed by reading a working implementation |
| `[Apple doc]` | Documented by Apple |
| `[Measured: …]` | Measured on the target machine (none yet) |
| `[Open]` / `[Investigate]` | Not established; an ADR or experiment is required before it can be relied on |
| `Proposed` | Written down, not implemented or verified |

**Nothing in this set is `[Measured]` yet.** That is the honest state of a project that has not run a line of its own code.
