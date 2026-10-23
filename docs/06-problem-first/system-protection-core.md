# System Protection Core — the app-neutral architecture

**Status:** Draft (supersedes the app-centric framing in earlier drafts)
**Date:** 2026-10-23
**Principle:** the core must work for any workload; adapters only sharpen what the core already sees.

---

## 0. The rule this document establishes

> **Memory Pilot protects the operating system first, and learns about applications second.**
> No core capability may depend on knowing which application is running.

Today you run an agent in a terminal. Tomorrow you run a different one in an IDE, or Docker, or a Python worker with no UI at all. If the protection logic is written against "the agent", the product dies with the tool it was written for.

**Consequence:** every module below is specified in terms of *processes, process trees, footprint over time, and system pressure* — never in terms of an application name.

---

## 1. Three layers

```
┌──────────────────────────────────────────────────────────────────┐
│ C. PROTECTION ACTIONS        tiered by safety, see §4            │
│    own memory · managed launch points · orphan handling ·        │
│    cooperative release · ask the user                            │
└──────────────────────────▲───────────────────────────────────────┘
                           │ decides
┌──────────────────────────┴───────────────────────────────────────┐
│ A. MEMORY PROTECTION CORE    ← REQUIRED. Works with zero adapters│
│                                                                  │
│  System Monitor   Process Inventory   Growth Detector            │
│  Risk Forecaster  Cause Analyzer      Protection Policy          │
│  Recovery Verifier                                               │
└──────────────────────────▲───────────────────────────────────────┘
                           │ enriches (never enables)
┌──────────────────────────┴───────────────────────────────────────┐
│ B. WORKLOAD ADAPTERS         ← OPTIONAL. Degrades gracefully.    │
│  agent lifecycle · terminal sessions · containers · IDE builds   │
│  browsers                                                        │
└──────────────────────────────────────────────────────────────────┘
```

**Hard architectural rule:** layer A must compile, run, and pass its acceptance tests with **layer B entirely absent**. There is a test for this (`IT-11`: build with all adapters disabled, run the full pipeline). If any core behavior degrades beyond "slightly less precise labels", the dependency is in the wrong direction and must be moved.

---

## 2. Layer A — Memory Protection Core

Each module below states: what it consumes, what it produces, and **what it must do when its inputs are missing**.

### A1. System Monitor

| | |
|---|---|
| Consumes | `host_statistics64` VM stats; `vm.swapusage`; `kern.memorystatus_vm_pressure_level`; dispatch pressure events; `statfs` disk headroom |
| Produces | `SystemSample` (per-metric quality), `PressureState` |
| If a source fails | metric `unavailable` with reason; never defaulted; never recovers a warning on missing data (SI-4) |
| App knowledge | **none** |

### A2. Process Inventory

| | |
|---|---|
| Consumes | `proc_listpids` + `proc_pidinfo(PROC_PIDTASKALLINFO)` per PID on a slower loop |
| Produces | `ProcessSnapshot` with identity `(pid, start_sec, start_usec)`, ppid, name, exe path, resident, CPU; **plus coverage counts** (attempted / succeeded / denied) |
| If a PID is unreadable | excluded, counted, coverage reported — never silently dropped |
| App knowledge | **none** — names and paths are data, not classification |

### A3. Growth Detector

| | |
|---|---|
| Consumes | delta series of `ProcessSnapshot` per identity |
| Produces | `GrowthFinding` per identity: rate, acceleration, shape (flat / plateau / linear / accelerating / sawtooth / step), burst probability |
| If history is short | `insufficient_history`, no finding — never a fabricated trend |
| App knowledge | **none** |

### A4. Risk Forecaster

| | |
|---|---|
| Consumes | `SystemSample` series, `PressureState` |
| Produces | `Forecast` — point estimate *display only*, one-sided lower bound *for policy*, calibration state, `actionable` |
| If not calibrated | `actionable = false`; policy cannot use it |
| App knowledge | **none** |

### A5. Cause Analyzer

**This is the module that replaces "which app is it?" with something computable.**

It answers *what kind of pressure* this is, from evidence available without app knowledge:

| Cause class | Evidence pattern | What it enables |
|---|---|---|
| `single_grower` | one identity/group accounts for most of the growth; others flat | precise recommendation; possible orphan check |
| `aggregate_demand` | many identities growing moderately; system total near budget; no single outlier | admission control (the lever that works) |
| `orphan_accumulation` | many processes whose parent identity is gone; count rising; footprint per process roughly constant | orphan candidates for layer C tier 3 |
| `cache_pressure` | compressor growth dominant; process footprints flat; reclaimable pages high | explain only — no action available |
| `swap_pressure` | swap rate high; disk falling; footprints steady | warn + admission control |
| `undetermined` | coverage poor, or conflicting signals | say so; do not guess |

**Rule:** `undetermined` is a valid and common output. The analyzer must never produce a cause it cannot support, because the cause decides which action tier is even considered.

### A6. Protection Policy

Consumes the above and decides **which tier of action, if any**. See `protection-actions.md`.

### A7. Recovery Verifier

Measures pressure before and after any action over a window; records `effective | partial | ineffective | unverified`. Never infers success from an exit code.

---

## 3. Layer B — Workload Adapters

An adapter may **add information**, and may **register a managed launch point** (see `admission-control.md`). It may never be required for monitoring, detection, forecasting, or generic recommendations.

### 3.1 Adapter contract

```go
type Adapter interface {
    ID() string
    // Observe enriches process trees; it must never block, and must degrade to nil.
    Observe(ctx context.Context, snap model.ProcessSnapshot) (Enrichment, error)
    // LaunchPoints returns command interception points this adapter can provide.
    // Returning none is valid and normal.
    LaunchPoints() []LaunchPoint
}
```

`Enrichment` may contain: workload/session labels, lifecycle events (session started/ended), ownership hints, and a *confidence* for each. **Every field is optional.** A core consumer must behave identically when `Enrichment` is empty.

### 3.2 Baseline adapters (in priority order, all optional)

| Adapter | What it adds | Cost if absent |
|---|---|---|
| Terminal session | process-tree root per terminal window/tab | groups split at the shell; still correct, less readable |
| Agent lifecycle | session start/end events, subagent spawn | orphan detection uses tree-gone evidence only; lower precision |
| Container | maps container processes to their runtime root | containers appear as a flat process cluster |
| IDE | build / language-server grouping | builds not separable from the IDE |
| Browser | helper-process grouping | helper swarm appears as many equal processes |

**No adapter is in scope for v1** except the generic tree grouping already in A2/A5. This is deliberate: adapters are valuable only once the core's own grouping has been measured for accuracy.

### 3.3 The test that keeps this honest

`IT-11 — core-only build`: with `adapters/` removed from the build entirely, the daemon must still:
- detect pressure transitions correctly,
- attribute growth to process trees,
- produce `single_grower` / `aggregate_demand` classifications,
- issue advisories,
- pass all safety tests.

If any of these fail, an adapter has leaked into the core.

---

## 4. Layer C — Protection actions, by safety tier

The decisive part. Actions are grouped by **what could break**, not by what they achieve.

| Tier | Action class | Breaks what | Automatic? | Precondition |
|---|---|---|---|---|
| **T1** | Shrink our own memory and logs | nothing | **always allowed** | none |
| **T2** | Defer / warn at a **managed launch point** | nothing (the launch has not happened) | automatic at that point | the launch went through us |
| **T3** | Terminate a **verified orphan** | possibly a long-running detached task | only with explicit opt-in + per-process evidence | full orphan verification (§4.3) |
| **T4** | Ask an application to release memory | depends on the app honoring it | only with opt-in, canary-gated | a documented app API exists |
| **T5** | Ask the user to act | nothing | always allowed | none |

`T2` and `T5` are new relative to the previous spec and are where most of the product's actual effect lives.

### 4.1 Why T2 matters more than it looks

T2 prevents a *future* allocation instead of trying to reverse a past one. It requires no permission over existing processes, breaks nothing, and is fully reversible (the user simply runs again later). It is the only tier whose effect is both large and free of risk.

### 4.2 Why T3 stays off by default

An orphan and a legitimately detached long build are **indistinguishable from outside** (see `research-feasibility-matrix.md` §3). Terminating the wrong one destroys work that no resume path recovers. T3 therefore ships as **detect-and-recommend**, and any future automatic mode requires a measured false-positive rate on real traces plus an explicit ADR.

### 4.3 Orphan verification (the evidence bar, if T3 is ever enabled)

All of the following, recorded in the decision:

1. Parent identity `(pid, start_sec, start_usec)` no longer resolvable, verified twice ≥ 60 s apart.
2. Child's ancestry now terminates at `launchd`.
3. Child has consumed < 1 % CPU and zero meaningful I/O for ≥ 10 min.
4. Child is not a registered launchd service and not SIP-protected.
5. No adapter reports a live session owning it.
6. The user has enabled T3 and confirmed this specific class of process.

Even then it is a *recommendation* unless the user has opted into automatic handling for that class.

### 4.4 Why T4 remains empty

No general macOS API lets a third party request heap release from another process. Without a documented per-app API, T4 has nothing to call. It stays as an extension point (`protection-actions.md` §5), not a promise.

### 4.5 T5 is not a failure

Asking the user is a legitimate outcome, and in a well-designed report it is often the *best* outcome: the user knows whether a session matters. The product's job is to make that question easy to answer, with the exact numbers and the exact resume path.

---

## 5. What the core explicitly does not do

| Not done | Because |
|---|---|
| Terminate/suspend/pause a process it did not spawn, except T3 with opt-in | work loss |
| Reset or resume paused applications | does not address the cause |
| Suppress the macOS out-of-memory dialog | no public API; hiding symptoms |
| Reserve RAM in advance to "release later" | reserving reduces headroom; net zero |
| Move another process's heap | pointer graph belongs to that process |
| Assume an application's identity to make decisions | the whole point of this document |

---

## 6. Related

- `protection-actions.md` — the tier system in detail
- `admission-control.md` — T2, the launch-point mechanism
- `product-capability-matrix.md` — feature → detect / prevent / remediate / recommend
- `problem-coverage-matrix.md` — which root cause each module addresses
- `benchmark-protocol.md` — how efficacy is measured
- `../adr/013-protection-action-tiers.md`
