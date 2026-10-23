# Execution Evidence — how a claim becomes a recorded fact

**Status:** Active from 2026-10-23
**Why this exists:** this project's rule is that a claim is only as good as the artifact behind it. Three words were being used loosely — *designed*, *tested*, *verified* — so they are defined here and every claim in the docs and checklist must carry one.

---

## 1. The three states

| State | Means | Requires | Does **not** require |
|---|---|---|---|
| **Designed** | a behavior is described in the spec precisely enough to implement | a spec section, a schema, or a test definition | any code |
| **Compiled/tested** | code exists, builds, and an automated check ran | a commit, a command, an exit code, captured output | the target OS |
| **Verified on macOS** | the behavior was observed on a real Mac with the recorded result | everything above, plus a macOS run with the artifact saved | a perfect result — a failed verification is still a verification |

**The rule that makes this useful:** a lower state is never reported as a higher one. "SCN-007 passes" may only be written after a run record exists. "IT-11 passes" may only be written after a build actually ran on the recorded commit. Nothing in this repository may claim *Verified on macOS* while the workspace has no Go source tree — a replay on a synthetic trace is not a macOS verification, and a design review is not a test.

## 2. Record format

Every executed check produces one file under `execution-records/`, named `YYYY-MM-DD-<id>-<rev>.md`, containing:

```markdown
# <ID> — <short title>

**State:** Compiled/tested | Verified on macOS
**Commit:** <full sha> (dirty: yes/no)
**Date/time:** <ISO 8601 with timezone>
**Host:** <macOS version, hardware, or "CI" / "replay only">

## Command

    <exact command, copy-pasteable>

## Exit code

    <integer>

## Output

    <verbatim tail, or a path to the captured log>

## What this proves

<one sentence: the specific claim the run supports>

## What this does NOT prove

<the scope boundary — e.g. "replay harness logic only; not the daemon pipeline">
```

A record with an empty *What this does NOT prove* is treated as overclaiming and is a review defect.

## 3. Which state each current artifact is in

| Artifact | State | Record |
|---|---|---|
| `schemas/validate.py` over 35 fixtures | **Compiled/tested** (Python on the dev host, not macOS-specific) | `2026-10-23-schema-validation-r2.md` |
| SCN-007..011 | **Designed** | none — no harness exists |
| IT-11 (core builds and runs with adapters disabled) | **Designed** | none — no Go source tree exists |
| ST-1/ST-2 (build scans for termination primitives) | **Designed** | none — the build they gate does not exist yet |
| ST-23 / ST-24 (ADR-014 T4 + L2 boundaries) | **Designed** | none — harnesses and implementation do not exist |
| Pressure decoding, swap layout, identity tuple | **Verified on macOS** (during the reference-audit phase, against the two reference repos' code and Apple headers — see `../01-research/reference-audit-macos-telemetry.md`) | recorded in that audit's file:line evidence, not in a run record |

## 4. What unblocks each pending state

| Pending | Blocked by | First actionable step |
|---|---|---|
| SCN-007..011 → Compiled/tested | no implementation; no harness | either a minimal Go core, or explicitly-labeled harnesses that test *policy logic* against fixtures (and say so in *What this does NOT prove*) |
| IT-11 → Compiled/tested | no `go.mod`/`cmd/`/`internal/` | create the module skeleton (Phase 1 of the roadmap) |
| ST-1/ST-2 → Compiled/tested | the build they gate does not exist | write the scan as a build step in the skeleton; it can be proven *effective* by injecting a canary call that fails the build |
| Any → Verified on macOS | the target machine has not run the daemon | Phase 5 observe-mode deployment |

## 5. Counting requirements (audit note, 2026-10-24)

The distinct, testable requirement IDs in `docs/00-product/requirements.md` number **62** (MP-REQ-001..080 with gaps for reserved numbers). An earlier working note said 63 — that count came from a loose regex that also matched the literal string `MP-REQ-` in a table header. The authoritative count is what a script produces by matching `MP-REQ-[0-9]{3}` and de-duplicating: 62. All 62 appear in bead COVERS notes. If a future count differs, re-run the strict regex — do not eyeball it.

## 6. Anti-patterns

| Don't | Why |
|---|---|
| "Spec says X, therefore X is implemented" | that is the exact confusion this file exists to prevent |
| Running a harness and calling it a system test | a harness proves the harness; the daemon is unproven |
| Recording a run without the commit sha | the record cannot be reproduced or invalidated |
| Recording only the passing output | a record without the failing cases hides the boundary |
| Claiming macOS verification from a Linux/Windows/CI run | the platform is part of the claim |
