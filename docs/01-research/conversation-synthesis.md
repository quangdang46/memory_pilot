# Research Note — Synthesis of the design conversation

**Source:** `ChatGPT-Read File Summary-20261023-1033.txt` (3 827 lines, 2026-10-23 08:35 → 10:31)
**Purpose:** preserve what was decided, separate it from what was merely suggested, and flag claims that need verification before they influence the build.

---

## 1. Chronology of the decision path

| # | User input | Effect on the design |
|---|---|---|
| 1 | "Run many agent sessions long-term without RAM blowing up; don't touch memory of running apps; keep things safe" | Established the core tension; rejected the "kill when full" framing |
| 2 | "Don't build a killer — if RAM fills and you kill, there's no research left" | **Decisive.** Killed the process-killer direction permanently |
| 3 | "Read `process_triage`" | Imported evidence-based reasoning, expected-loss, protection-before-scoring |
| 4 | "Product goal: memory never overflows but apps stay smooth" | The charter's success criteria |
| 5 | "Longer-term: manage memory like a stack — stash it when nearly full, release it back. Think wild but feasible ideas" | Explored memory tiers; ended at a *feasible* subset (see §4) |
| 6 | "I'm on macOS; the real pain is the out-of-memory dialog with Resume/Quit; I don't want to see it again" | **Sharpened the problem.** The dialog is the symptom; the target is preventing the state that triggers it |
| 7 | "Mac mini M4, 16 GB, 256 GB" | Concrete constraints; SSD is small, so swap growth is also a disk risk |
| 8 | "Read `storage_ballast_helper`" | Daemon architecture, EWMA/PID, evidence ledger, shadow/canary/enforce |
| 9 | "Full product from the start, not a thin MVP; Go for dev/build speed" | Architecture-first, complete-spec approach; language decision |
| 10 | "Daemon like sbh? Never kill. Rust or Go?" | Confirmed daemon + Go |
| 11 | "Is MemGuard a good name?" | Renamed |
| 12 | "memory_pilot / mp — does mp collide?" | Final naming |
| 13 | "Research spec/stack/architecture thoroughly — 80 % spec" | This document set exists because of this instruction |
| 14 | "Did you fully convert the learnings from the two repos? Research more repos" | Reference-audit approach + traceability matrix |
| 15 | "Is the architecture settled?" | Answer: directionally yes, implementation-ready no → ADRs |

---

## 2. Decisions that are settled

| Decision | Value |
|---|---|
| Product | Memory Pilot (`mempilot`, alias `mp` optional) |
| Language | Go |
| Deployment | macOS user LaunchAgent, no root, no privileged helper |
| Architecture | Modular monolith, layered, platform isolated |
| Kill/suspend capability | **None, anywhere** |
| Analysis/execution separation | Analysis produces values; only the executor acts on an allowlist |
| Approach | Research → spec → verification plan → implementation |
| Primary machine | Mac mini M4 / 16 GB / 256 GB, macOS |
| Reference repos | `storage_ballast_helper`, `process_triage` (both MIT) |

## 3. Decisions made in this document set (beyond the conversation)

The conversation left these open; they are decided here, each with an ADR:

- kernel pressure level decoding uses XNU dispatch masks 1/2/4 (ADR-001)
- hybrid pressure signal: dispatch events + kernel sysctl + polling liveness
- two sampling loops (system vs. process) with separate cadences and hard floors (ADR-004)
- JSONL-only ledger in v1, with a derivable sidecar index if needed (ADR-003)
- no relief provider in v1 beyond self-trimming; `memory_pressure` purge explicitly rejected for v1 (ADR-006)
- forecast tiers T0→T3, each gated on measurable improvement (ADR-005)

---

## 4. The "memory stack" idea — what survived, what didn't

The user's most interesting formulation was: *"when it's nearly full, stash that memory away; when there's room, release it back."*

A careful decomposition (see ADR-011):

| Part of the idea | Reality | In the product? |
|---|---|---|
| Compress pages | XNU already does it | Observed, not controlled |
| Push pages to swap | XNU already does it | Observed; disk interaction modeled |
| Drop reclaimable cache | XNU already does it | Observed |
| App releases its own memory | possible per-app | Future provider (ADR-011b) |
| **Move another process's live heap to SSD and back** | **impossible safely in user space** | **No — rejected permanently** |
| Predict and act before the cliff | feasible and valuable | **Yes — the actual product** |

So the "wild idea" resolved into: *forecast early, relieve only through cooperation, never relocate memory you don't own.* That is a weaker-sounding but genuinely buildable product.

---

## 5. Claims in the conversation that need verification before use

These were asserted or implied by the assistant in the transcript. None should enter a spec as fact.

| Claim | Status | Action |
|---|---|---|
| Claude Code GitHub issues #22427 (18–23 GB sessions), #24658 (swap thrashing), #22188 (heap growth) | **Unverified.** Numbers and issue IDs came from a web search; issue numbers change meaning over time and some may be misremembered. | Do not cite in specs. If needed, re-verify on GitHub and cite the actual issue titles/URLs. |
| "FrankenTerm" and "misc_coding_agent_tips_and_scripts" describe WezTerm mux stalls with 20+ agents | Unverified | Not used in design |
| Boost uses `memory_pressure` to release memory | Reported in the transcript; the repo link in the export is mangled | Verify before relying on it; ADR-006 treats purge as unproven regardless |
| AppWrangler = GPL-2.0, Boost = GPL-3.0, Memory Pressure = Apache-2.0 | Unverified in this session | Re-check licenses before reading any code for reuse; we only take *ideas*, and our own code is written fresh |
| `kern.memorystatus_vm_pressure_level` uses masks 1/2/4 | **Verified** by reading sbh's working implementation (`pal.rs:496-504`) | Adopt |
| `GOMEMLIMIT` limits the daemon's total memory | **Partially wrong.** It is a soft limit on Go-managed heap, not on RSS (stacks, cgo, mmap not covered) | Documented as a soft limit; RSS is measured directly |
| "PID controller" needed for memory | **Not established.** PID controls a continuous actuator; memory relief has no continuous actuator on macOS | Investigated, not adopted (ADR-005) |
| macOS memory-pressure dialog cannot be disabled by third parties | **Likely true** (no public API found) | Stated as a limitation; the product prevents causes, never hides symptoms |
| "memory_pressure -l warn"-style purge has a beneficial effect | **Unproven and plausibly negative** | Excluded from v1 (ADR-006) |

**Rule going forward:** a claim enters a spec only with `[Verified: source]`, `[Apple doc]`, or `[Measured: date/machine]`. Everything else stays in this file.

---

## 6. Naming verification still outstanding

| Item | Status |
|---|---|
| `mempilot` binary name | Chosen; no collision check done against Homebrew formulae / crates.io / npm |
| `mp` alias | Known collisions: Multipass (common user alias), `mp(1)` (MetaPost) on some Unixes. Not installed by default |
| `com.mempilot.agent` label | Chosen |
| Domain / trademark | Not checked. Deferred until the project is public |

---

## 7. What the conversation did *not* cover (gaps filled here)

1. **Concrete macOS API plan** — the conversation named APIs; `../02-architecture/macos-integration.md` maps them to verified field-level detail from a working implementation.
2. **Module contracts** — never specified in the conversation; now in `../02-architecture/module-contracts.md`.
3. **Safety as invariants vs. policy** — the conversation said "never kill"; the mechanism (no capability at all) is new here (SI-1, ADR-012).
4. **Test strategy** — the conversation only listed categories; now replay corpus, fault injection, safety suite, soak gates.
5. **Performance budget** — mentioned as a requirement; numbers and measurement protocol now defined.
6. **Ledger schema and retention** — not covered; now specified.
7. **The distinction between capability degradation and confidence degradation** — new (ADR-010).
8. **Attribution's shared-page/compression accounting** — the conversation noted RSS ≈ leak is wrong; the *reporting* consequences (why sums exceed the delta, why percentages are forbidden) are new here.

---

## 8. Tone and audience

The user's working language in the conversation is Vietnamese, with English technical terms. Product-facing documentation in this repo is written in English (code, identifiers, specs) with Vietnamese reserved for conversational summaries. This is recorded so future contributors do not split the docs across languages by accident.
