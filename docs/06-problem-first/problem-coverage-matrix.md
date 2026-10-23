# Problem Coverage Matrix

**Status:** Draft — this is the document the rest of the spec should have started from.
**Date:** 2026-10-23
**Purpose:** answer "does this design actually solve the user's problem?" before answering "is the design consistent?"

---

## 0. The problem, restated without architecture

> Run many coding-agent sessions concurrently, for many hours, on a 16 GB Mac mini. The machine reaches "out of application memory", macOS pauses apps, and the user must click Resume or Force Quit repeatedly. Sessions must not be lost.

Everything below is judged against **that sentence**, not against any module.

---

## 1. Root causes, and what each actually requires

Each row: how the pressure arises, what would have to be true to prevent it, what a third-party user-space daemon can actually do, and the evidence needed.

### RC-1 — Aggregate demand exceeds RAM (many legitimate sessions)

| | |
|---|---|
| **How it arises** | 6–10 sessions each genuinely holding 0.8–1.5 GB, plus browser and IDE. Nothing is broken; the total is simply larger than 16 GB. |
| **Detection** | Sum of per-group footprints vs. safe headroom; number of concurrent sessions; trend of the total. |
| **Prevention that works** | **Don't start session #7.** This is the one lever with real, immediate, certain effect. |
| **Prevention that does not work** | Warning alone. The user knows it is full; that is why they are clicking Resume. |
| **Remediation possible** | Only by ending or suspending work — which is the thing we promised not to do. |
| **Verdict** | **Solvable — by admission control, not by monitoring.** |

### RC-2 — Session exists but is not doing anything (idle accumulation)

| | |
|---|---|
| **How it arises** | A session finished a task an hour ago and sits holding context. Multiply by four. This is extremely common in this workflow and is the largest avoidable consumer. |
| **Detection** | Process tree age + no child activity + no CPU + no I/O + no foreground tty writes for N minutes. **All of these are weak individually**; together they narrow the case but never prove it. |
| **Prevention** | Prompt the user to close idle sessions **with a resume path**. |
| **Remediation possible** | Suspending/terminating an idle session loses in-memory context unless the agent can resume. |
| **Verdict** | **Partially solvable — via a reversible recommendation, not an automatic action.** |

### RC-3 — Orphaned child processes (leak by accumulation)

| | |
|---|---|
| **How it arises** | A subagent / build / MCP server spawns children that outlive the parent session. Each holds ~140 MB. Dozens accumulate over hours. Reported in public issue trackers for agent CLIs on 16 GB machines. |
| **Detection** | Parent identity gone (pid + start-time tuple no longer resolvable) **and** the child is still running **and** the child's ancestry chain terminates at launchd rather than at a live session. |
| **Prevention** | Reap at spawn time — not possible from outside. |
| **Remediation possible** | Yes, in principle: the owner is gone, so no live context is destroyed. **But "owner is gone" is exactly what a slow tool call looks like**, and getting it wrong destroys work. |
| **Verdict** | **The only class where automatic remediation is even conceivable — and it needs a much higher evidence bar than anything currently specified.** |

### RC-4 — One process grows without bound (true leak)

| | |
|---|---|
| **How it arises** | An allocator bug, an unbounded cache, a pathological context. |
| **Detection** | Sustained growth uncorrelated with work; no plateau; reproducible. |
| **Prevention** | None available from outside. |
| **Remediation possible** | Only by restarting the process — which loses the work. Not acceptable as an automatic action. |
| **Verdict** | **Not solvable from outside. Solvable only by naming it clearly enough that the user acts (or upstream fixes it).** |

### RC-5 — Swap thrash / disk headroom exhaustion

| | |
|---|---|
| **How it arises** | Long-running pressure pushes pages to a 256 GB SSD; free disk shrinks; latency collapses. |
| **Detection** | swap rate + free disk + kernel pressure. |
| **Prevention** | Reduce demand (same as RC-1) and stop adding demand. **Also: the daemon's own log growth must not contribute.** |
| **Verdict** | **Solvable indirectly — it is a consequence of RC-1/RC-2.** |

### RC-6 — Compression exhaustion

| | |
|---|---|
| **How it arises** | The compressor can no longer keep up; compressed pages cannot be evicted fast enough. |
| **Detection** | `compressor_page_count` trend + kernel pressure. |
| **Remediation** | Cannot be manipulated. |
| **Verdict** | **Observable only — useful as an *early* signal, not as an action target.** |

### RC-7 — The daemon itself becomes the problem

| | |
|---|---|
| **How it arises** | Unbounded sample history, log growth, goroutine leak, per-tick process enumeration. |
| **Prevention** | Budgets + degradation (already specified, SI-6). |
| **Verdict** | **Solvable — already covered, and must stay covered.** |

### RC-8 — Wrong diagnosis

| | |
|---|---|
| **How it arises** | RSS ≠ contribution; shared pages; compressed memory invisible to per-process accounting. |
| **Consequence** | The user acts on the wrong process. |
| **Verdict** | **Solvable — already specified. Necessary but not sufficient.** |

---

## 2. The coverage verdict, stated plainly

| Cause | Detection | Prevention | Remediation | What closes the gap |
|---|---|---|---|---|
| RC-1 aggregate demand | ✅ | ❌ **missing** | ❌ impossible | **Admission control** |
| RC-2 idle sessions | ⚠️ partial | ❌ **missing** | ⚠️ reversible only | **Session lifecycle + resume guidance** |
| RC-3 orphan children | ⚠️ partial | ❌ | ⚠️ conceivable | **Orphan verification (observe-first)** |
| RC-4 true leak | ✅ | ❌ | ❌ impossible | Honest naming + upstream reporting |
| RC-5 swap/disk | ✅ | ⚠️ indirect | ⚠️ indirect | Follows from RC-1 |
| RC-6 compression | ✅ | ❌ | ❌ impossible | Early-signal use only |
| RC-7 self-harm | ✅ | ✅ | ✅ | Already specified |
| RC-8 misdiagnosis | ✅ | ✅ | ✅ | Already specified |

**Reading the table:** the current spec covers the last three rows well and the first five poorly. That is the whole finding. Precisely:

> The spec's action allowlist (`TrimOwnBuffers`, `RotateOwnLedger`, `EmitAdvisory`) addresses **only RC-7**. For RC-1 through RC-6 — every cause that actually produces the dialog — the product has **detection and nothing else**.

Warning is not prevention. The user already knows the machine is out of memory; that is what the dialog says.

---

## 3. What "prevention" would have to mean

Three mechanisms are available to a user-space daemon that does not control other processes. Only the first two were previously considered.

| Mechanism | Lever | Effect | Requires |
|---|---|---|---|
| **A. Admission control** | refuse/defer *starting* new sessions when the forecast says they don't fit | **large, immediate, certain** | the user launches sessions through a managed entry point |
| **B. Session lifecycle guidance** | tell the user which idle sessions to close, and how to resume them | large when followed | agent supports resumption |
| **C. Cooperative release** | ask an app to release memory | small/unknown | an app API that does not appear to exist |
| **D. Orphan reaping** | end processes whose owner is provably gone | moderate, rare | very high evidence bar; policy change |

The current spec has **none** of A, B, or D, and correctly rejects C.

---

## 4. What this means for the design

1. **Admission control is not a feature of the daemon; it is the product's primary lever.** It must be in the architecture, not deferred to "Phase 6 if a provider is found."
2. **Session lifecycle must be a first-class module**, because RC-2 is the largest avoidable consumer and the only remediation path that is reversible.
3. **Orphan reaping stays observe-and-recommend in v1**, but the *detection* must be built now, because it is the only evidence base on which a future policy change could rest. See ADR-013.
4. **Everything already specified remains necessary** — you cannot do A or B without correct measurement and forecasting. But necessary is not sufficient, and the spec previously mistook one for the other.

---

## 5. Honest statement of what remains unsolvable

- A single process that allocates without bound cannot be stopped from outside without destroying its work. Memory Pilot's contribution is to name it precisely and early.
- If the user launches sessions outside the managed entry point and ignores recommendations, no mechanism in this design has any effect.
- If the hardware is genuinely insufficient for the workload, the correct output is "this needs more RAM or fewer concurrent sessions", and the product must say so rather than implying it will handle it.

**These are not excuses. They are the boundary inside which the product has to be designed, and stating them is what makes the rest of the design credible.**

---

## 6. Related

- `research-feasibility-matrix.md` — each mechanism's technical feasibility
- `product-capability-matrix.md` — feature → detect/prevent/remediate/recommend
- `session-lifecycle.md`, `admission-control.md` — the two new mechanisms
- `benchmark-protocol.md` — how efficacy is measured
- `../adr/013-action-policy-revision.md` — the policy change this implies
