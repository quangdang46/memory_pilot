# Specification Completion Checklist

**Purpose:** define when the spec phase is *done*, so implementation is not started against a moving target — and so "the docs exist" is not confused with "the design is settled".

The owner's instruction was to spend ~80 % of the effort on specification. This checklist is the definition of that 80 % being complete.

---

## A. Structural completeness

| # | Criterion | Status |
|---|---|---|
| A1 | Product charter with problem, user, success criteria, limitations | ✅ `00-product/product-charter.md` |
| A2 | Goals and non-goals, each with a reason | ✅ `00-product/goals-and-non-goals.md` |
| A3 | Requirements, each falsifiable, with acceptance + verification method | ✅ `00-product/requirements.md` (55 requirements) |
| A4 | Scenarios: user, failure, and anti-scenarios | ✅ `00-product/scenarios.md` |
| A5 | Acceptance criteria per scenario | ✅ `05-testing/scenarios/` (001–005) |
| A6 | System architecture with layers, data flow, state, failure domains | ✅ `02-architecture/system-architecture.md` |
| A7 | Module contracts as interfaces | ✅ `02-architecture/module-contracts.md` |
| A8 | Concurrency model with bounded channels and an explicit goroutine inventory | ✅ `02-architecture/runtime-and-concurrency.md` |
| A9 | macOS integration with verified API detail | ✅ `02-architecture/macos-integration.md` |
| A10 | Persistence, schema, retention, crash safety | ✅ `02-architecture/persistence.md` |
| A11 | CLI surface with exit codes and output shapes | ✅ `02-architecture/cli-surface.md` |
| A12 | Security & privacy threat model + data inventory | ✅ `02-architecture/security-and-privacy.md` |
| A13 | Algorithms: telemetry, pressure, forecast, attribution, leak suspicion | ✅ `03-algorithms/` (5 docs) |
| A14 | Safety: invariants, authorization, protection, failure recovery | ✅ `04-safety/` (4 docs) |
| A15 | Test strategy incl. replay corpus, fault injection, budgets, safety suite | ✅ `05-testing/` (6 docs) |
| A16 | ADRs for every consequential decision, with status and evidence | ✅ `adr/` (13) |
| A17 | Reference audits with file:line evidence | ✅ `01-research/` (3 audits) |
| A18 | Traceability from requirement → module → test, and reference idea → classification | ✅ `01-research/traceability-matrix.md` |
| A19 | Implementation roadmap with entry conditions and exit gates | ✅ `00-product/roadmap.md` |
| A20 | Glossary + FAQ | ✅ `appendix/` |

---

## B. Quality gates on the spec itself

| # | Criterion | Status |
|---|---|---|
| B1 | No requirement without a test | ✅ matrix A covers all MP-REQ-xxx |
| B2 | No borrowed idea without an Adopt/Adapt/Reject/Investigate classification | ✅ matrix B |
| B3 | No claim stated as fact without a source tag | ✅ tags in use; unverified claims quarantined in `01-research/conversation-synthesis.md` §5 |
| B4 | No contradiction between docs | ⚠️ **needs a final pass** — see §D |
| B5 | Every `[Open]` has an experiment or an ADR | ✅ listed in §C |
| B6 | Every ADR has "revisit triggers" | ✅ |
| B7 | Terminology consistent (checked against the glossary) | ⚠️ **needs a final pass** |
| B8 | Constants are named once and referenced, not re-invented per doc | ⚠️ **partially** — several docs cite defaults; consolidate into a constants appendix before code |

---

## C. Open questions registry

Every unknown in the spec set, with its resolution path.

| ID | Question | Where | Resolution |
|---|---|---|---|
| Q-1 | Watch thresholds in free-% rate or swap rate? | pressure-estimator §9 | traces |
| Q-2 | Can compressor % alone justify Watch? | pressure-estimator §9 | traces |
| Q-3 | Reference values F/C/D on the target machine | pressure-estimator | spike S-3/S-5 |
| Q-4 | Is headroom = free pages sufficient? | forecasting §8 | traces + S-5 |
| Q-5 | Separate forecasters per series? | forecasting §8 | design review |
| Q-6 | Which horizons to surface? | forecasting §8 | UX decision, cheap |
| Q-7 | Grouping recompute cadence | attribution §9 | stability tests |
| Q-8 | Group identity: anchor only or with session index | attribution §9 | design review |
| Q-9 | Is footprint worth the extra call? | attribution §9 | spike (ADR-002 E3) |
| Q-10 | Cost of `proc_pid_rusage` per PID | performance-budget §7 | spike S-5 |
| Q-11 | `ReadMemStats` cadence cost | performance-budget §7 | measurement |
| Q-12 | `Adaptive` vs `Standard` `ProcessType` latency (`Background` already excluded) | performance-budget §7 | measurement |
| ADR-000..014 | see `adr/README.md` | — | each has an evidence section |

Plus the macOS unknowns in `01-research/reference-audit-macos-telemetry.md` §9.

---

## D. Pre-implementation gates

These replace the earlier loose "final passes" list. The spec is not done because it has 57 files; it is done when each gate is demonstrably met. **A gate is met by an artifact that can be checked, not by a document that asserts it.**

| Gate | Condition | Current status |
|---|---|---|
| **G1 — Reference audit** | Every mechanism marked `Verified` has file:line evidence *and* its main-path status recorded; no stale conclusions remain | ⚠️ **in progress** — conformal/e-process/scorecard now `Verified · Adopted`; audit + traceability updated. Remaining: full sweep of every row |
| **G2 — Contract consistency** | No contradiction in: forecast bound representation, pressure-state names, mode names, exit-code namespaces, action outcomes, LaunchAgent config, **designed-vs-compiled action scope**, action naming | ⚠️ **partly met** — conformal bound ✅, exit namespaces ✅, naming conventions ✅, LaunchAgent/Q-12 ✅, ADR-012/ADR-013 action-scope ✅ (schema may *represent* T3 act actions; the v1 executor has no code path for them, stated in `protection-actions.md` §3.4, `process-relief-engine.md` §0, `schemas/README.md` invariant 4). ADR-014 ✅: one-name-per-action (`RequestCooperativeRelease`, `StopManagedWorkload`; old `RequestReclaimFromProvider` retired), ST-1 carve-out stated in SI-1b + `safety-tests.md` ST-1, ST-23/ST-24 added, T4/L2 statements propagated to ladder, matrix, roadmap, `safety-invariants.md`. Remaining: full-doc pass |
| **G3 — Schema** | Real JSON Schema (Draft 2020-12) for every core object, with valid **and** invalid fixtures and a runnable validator | ⚠️ **partial** — 8 objects covered (`Forecast`, `PressureState`, `Attribution`, `Classification`, `Decision`, `ActionResult`, `StateFile`, `common`), **35 fixtures** (see `schemas/README.md`; the `30` written here before 2026-10-24 was stale — the r2 record `execution-records/2026-10-23-schema-validation-r2.md` is the count of record), validator green and **fail-closed** (exit 2 if the validator cannot run, exit 3 if no fixtures are found — a check that cannot run never reports pass). **Core-contract objects still missing: `Sample`, `Incident`, `Advisory`, `Episode`.** Gate not met until the scope question is answered: bead `memory_pilot-m00` owns defining the core-contract set explicitly, then either schematising the rest or moving them out of core |
| **G4 — Safety** | Every invariant has a mechanical enforcement and a test that proves it | ❌ **designed only** — enforcement is specified (ST-1..ST-22) but nothing is implemented; no invariant has been executed. This gate cannot be met before code exists |
| **G5 — Algorithm acceptance** | Replay tests for forecast, calibration, scorecard, planner, **and the positive-provider case** | ⚠️ **specified only** — MP-SCN-006 exists as acceptance criteria; no trace corpus recorded, no replay run. Note SCN-006 proves *planner logic*, not that a real provider exists |
| **G6 — Runtime readiness** | Crash recovery, backpressure, resource budget and launchd lifecycle designed to implementation depth | ⚠️ **designed, unverified** — `runtime-and-concurrency.md`, `persistence.md`, `failure-recovery.md` are detailed, but no design survives contact with a real launchd until S-4/IT-9 run |
| **G7 — Traceability** | Every requirement → module → schema → test; every reference idea classified with reason | ⚠️ **partly** — matrix exists and now carries the schema column. **G7 is met when, for every MP-REQ row: module is named (not `—` unless the requirement is genuinely cross-cutting), schema is named or the requirement is explicitly recorded as "object not yet schematised, owned by `memory_pilot-m00`", and at least one test ID is named. Acceptance is a checklist run, not a prose review: a script must extract the matrix rows and FAIL on any `—` that is not accompanied by one of those two exemption notes. Bead `memory_pilot-iy1` owns the sweep; its done criteria include that script's output. |
| **G8 — Go/no-go** | Every unresolved question has an owner, a measurement method and a decision condition | ⚠️ **registry complete, questions open** — §C lists 12 questions with resolution paths, but *none is answered*. "Met" would mean the questions are resolved; what exists is the honest enumeration |

### G3 note — schema is the contract, prose is the explanation

`docs/appendix/schemas.md` describes intent; `schemas/*.schema.json` is what a validator runs. If the two ever disagree, **the schema wins and the prose is a bug**. The `validate.py` run is a required check, and the Go serializers will be tested against the same fixtures (G3's second half, once code exists).

### G2 note — the contradiction pass is not `grep`

Replacing one word with another across 57 files can create new inconsistencies. The pass must check, per concept, that the **domain name**, **serialization value**, **display form**, **thresholds**, and **transition rules** agree — see the naming table in `appendix/glossary.md`. Where two vocabularies legitimately coexist (macOS `warn` vs our `prevent`), the boundary must be documented rather than unified away.

---

## D.2 First tests to write (before or alongside the first code)

1. `schemas/validate.py` wired into the build (already runnable).
2. Go serializers round-tripping every fixture in `schemas/testdata/`.
3. Golden human output for `status`, `report`, `explain` at levels 0–3.
4. Trace acceptance files — one per trace in the corpus (`05-testing/scenarios/` has six; the corpus lists twelve).

---

## E. What "done" does *not* mean

- Does not mean every algorithm is proven — several are `Proposed` and require Phase 0 spikes.
- Does not mean every relief action exists — v1 deliberately has no provider that frees another process's memory (see `00-product/v1-scope-contract.md`; this line previously cited ADR-006's superseded phrasing).
- Does not mean the product will prevent every OOM — it cannot (charter L1), and the spec says so.

The spec is complete when the **shape** is settled and the **unknowns are enumerated with a plan**. It is not complete when every number is known; that requires the machine.

---

## F. Spec freeze — Go / No-Go criteria (added 2026-10-23)

The freeze decision is: **start implementation, or keep the spec open**. It is taken once, against this section, not re-litigated per document.

**Go — start implementation — when ALL hold:**

1. **Scope contract exists and is referenced.** `00-product/v1-scope-contract.md` is the binding scope; requirements carry MP-REQ-051; the docs index points at the contract first. (Done this batch.)
2. **No scope contradiction remains open.** Every document that once said "nothing beyond self-trimming" has been restated or explicitly cites the contract as the correction. (Done this batch: ADR-006, roadmap Phase 4, synthesis §3, charter §3.5.)
3. **Designed-vs-compiled is stated where T3 act appears.** Any doc naming `RequestGracefulQuit`/`TerminateVerifiedOrphan` states the executor has no code path in v1 and ST-1/ST-2 verify that. (Done: ADR-013, `protection-actions.md` §3.4, `process-relief-engine.md` §0, `schemas/README.md` invariant 4.)
4. **Validator green.** `schemas/validate.py` exit 0 on the current fixtures. (35 fixtures, green — `execution-records/2026-10-23-schema-validation-r2.md`.)
5. **The unresolved questions are enumerated with owners and methods, and none of them can make us build the wrong product.** That is §C + the Phase 0 spike list: answering them changes numbers and thresholds, not the architecture. This is a judgment call the owner makes at freeze time — the checklist's job is to make the open set visible, not to close it.

**No-Go — keep the spec open — if ANY hold:**

1. A scope contradiction is found that the contract does not resolve (a document implies an executor capability the contract denies).
2. A safety invariant lacks a mechanical enforcement *specified* (ST-1..ST-22 coverage gap) — enforcement is implemented later, but the specification of how each invariant is checked must exist before code.
3. A v1 requirement has no acceptance test named (spec-side traceability gap, §B1).
4. A schema in the core-contract set has no valid+invalid fixture pair (G3 scope).

**After freeze, the spec reopens only for:**

1. a contradiction that blocks implementation,
2. a newly found safety problem,
3. a core requirement that proves untestable as designed,
4. real data that falsifies a load-bearing assumption.

Everything else goes to the backlog. Feature ideas discovered during implementation are **not** freeze-breakers.

**Freeze status: GO on items 1–4; item 5 is the owner's call.** The honest statement is: items 1–4 were closed this batch. Item 5 — "none of the open questions can make us build the wrong product" — has not been asserted; §C lists open questions with resolution paths but no owner sign-off. The owner freezes by recording the decision (date + signature line) here:

> Spec freeze decision: ______________ Date: __________ Go / No-Go (circle one). Open questions accepted as post-freeze work: __________.
