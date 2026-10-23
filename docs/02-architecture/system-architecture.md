# MP-ARCH-001 — System Architecture

**Status:** Draft (Proposed — each decision is tagged `[Proposed]`, `[Verified]`, or `[Open]`)
**Version:** 0.1.0

---

## 1. Deployment shape

```
┌──────────────────────────── macOS (user session) ────────────────────────────┐
│                                                                              │
│  launchd ──► com.mempilot.agent (LaunchAgent, user scope, no root)           │
│                     │                                                        │
│                     ▼                                                        │
│            ┌──────────────────┐        Unix socket        ┌────────────────┐ │
│            │   mempilotd      │◄─────────────────────────►│   mempilot     │ │
│            │   (daemon)       │  ~/Library/…/mempilot.sock│   (CLI)        │ │
│            └────────┬─────────┘                           └────────────────┘ │
│                     │                                                        │
│         ┌───────────┼───────────────┬──────────────────┐                     │
│         ▼           ▼               ▼                  ▼                     │
│    Mach/host APIs  sysctl      libproc proc_*    own data dir (ledger)       │
│    host_statistics64  kern.memorystatus  proc_pidinfo                        │
│    dispatch memorypressure               proc_pid_rusage                     │
│    swap usage                                                            │
└──────────────────────────────────────────────────────────────────────────────┘
```

**Decisions:**

| # | Decision | Status | Rationale / ADR |
|---|---|---|---|
| D1 | One daemon, two entry points (`mempilot`, `mempilotd`), one core library | Proposed | Modular monolith; no microservices on a Mac mini |
| D2 | User LaunchAgent, not LaunchDaemon; no root | Proposed | Least privilege; all v1 data is user-visible (sbh verifies this is workable on macOS) |
| D3 | IPC over Unix domain socket with peer-UID check | Proposed | No TCP surface; ADR-007 open on framing |
| D4 | Ledger on local disk, bounded, rotated | Proposed | ADR-003 open on JSONL vs SQLite (recommendation: JSONL v1 + optional SQLite index later) |
| D5 | No network | Verified by design | SI, MP-REQ-076 |

---

## 2. Layering

Five layers, strict downward dependency. An upper layer may not be imported by a lower one.

```
┌───────────────────────────────────────────────────────────────┐
│ L5  Interface        cmd/mempilot, cmd/mempilotd, reporting    │
├───────────────────────────────────────────────────────────────┤
│ L4  Policy & Action  policy, planner, actionguard, executor,   │
│                      providers, verification                   │
├───────────────────────────────────────────────────────────────┤
│ L3  Intelligence     pressure, forecast, attribution, workload,│
│                      leaksuspect                               │
├───────────────────────────────────────────────────────────────┤
│ L2  Telemetry        collector, telemetrymodel, selfmonitor    │
├───────────────────────────────────────────────────────────────┤
│ L1  Platform         platform/macos (build-tagged), platform   │
│                      (interfaces), store, ledger, clock, model │
└───────────────────────────────────────────────────────────────┘
```

**Arithmetic of the dependency rule:**

- L3 imports L2 model types and L1 (`model`, `mathx`, `clock`) — never L2 collectors, never L4.
- L4 imports L3 results + L1 model — never L2 collectors directly.
- L1 never imports L2..L5.
- `ledger`, `store`, `clock`, `model` are L1 "kernel" packages importable by everyone.
- Enforced by `go list -deps` assertions (SI-2).

> Rationale: this is exactly the property that makes SI-2 ("analysis cannot act") true by construction rather than by discipline, and it is what makes G-10 (portability) real: replacing `platform/macos` with `platform/linux` touches nothing above L2.

---

## 2.5 Three outcomes, not one pipeline

**The most important architectural correction.** A pipeline diagram reads as "pressure rises → action happens". That is the wrong mental model for this product, and it is how a codebase drifts into "we must relieve something".

The system has **three peer outcomes**. `Act` is not the terminal stage; it is the least-used branch, and it is empty in v1.

```
                      ┌─────────────────────────┐
                      │  Observe                │  always available
                      │  collect, estimate,     │  the default and the
                      │  forecast, attribute    │  only outcome in most ticks
                      └───────────┬─────────────┘
                                  │
              ┌───────────────────┼───────────────────┐
              ▼                   ▼                   ▼
      ┌──────────────┐   ┌──────────────────┐  ┌──────────────┐
      │  no output   │   │  Recommend       │  │  Act         │
      │              │   │  explain + advise│  │  allowlisted │
      │              │   │  to the user     │  │  provider    │
      └──────────────┘   └──────────────────┘  └──────────────┘
                          product value        empty in v1
                          even with no         (ADR-006)
                          relief mechanism
```

| Outcome | Guaranteed available? | Value delivered |
|---|---|---|
| **Observe** | Always | data, history, the ability to answer "what happened" |
| **Recommend** | Always | diagnosis, forecast with confidence, named contributors, advice |
| **Act** | **Only if** a proven provider exists | measured relief |

**Architectural consequences:**

1. `Recommend` must be a first-class output, not a fallback path taken when `Act` fails. The renderers, advisories, and `explain` are core modules, not error handlers.
2. The planner's "no action" outcome is **success**, not a failure to be retried. It must be recorded as a positive decision (`no_action_reason`), never as an empty list.
3. A product that ships Observe + Recommend and no providers is **complete**, not a stub. That is exactly v1 (ADR-006).

**Why this matters operationally:** if the codebase is shaped as `… → planner → executor` with the executor at the end, every future feature request will be "make the executor do more". If it is shaped as three outcomes, the natural question becomes "is Recommend good enough yet?" — which is the question this product actually turns on.

---

## 3. Data flow (one tick)

```
                 ┌──────────────┐
   timer/event ─►│  Supervisor  │  (owns lifecycle, health, budget)
                 └──────┬───────┘
                        │ tick
                        ▼
          ┌─────────────────────────────┐
          │ Collector                   │
          │  • host VM stats            │
          │  • swap                     │
          │  • kernel pressure level     │
          │  • process table (delta)    │
          │  • disk headroom            │
          └─────────────┬───────────────┘
                        │ Sample (with per-metric quality)
                        ▼
          ┌─────────────────────────────┐
          │ Pressure Estimator          │ → PressureState{level, score, inputs}
          └─────────────┬───────────────┘
                        │
        ┌───────────────┼────────────────┐
        ▼               ▼                ▼
┌──────────────┐ ┌──────────────┐ ┌─────────────────┐
│ Forecaster   │ │ Attribution  │ │ Leak Suspicion  │
│ trend/ETA/   │ │ per-group    │ │ classification  │
│ confidence   │ │ deltas       │ │ + alternatives  │
└──────┬───────┘ └──────┬───────┘ └────────┬────────┘
       └───────────────┼──────────────────┘
                       ▼
          ┌─────────────────────────────┐
          │ Policy Engine               │
          │  mode, risk, allowlist,     │
          │  vetoes, rate limits        │
          └─────────────┬───────────────┘
                        │ Decision (or NoAction + reason)
                        ▼
          ┌─────────────────────────────┐
          │ Planner                     │  orders candidate actions
          └─────────────┬───────────────┘   by expected benefit / cost
                        ▼
          ┌─────────────────────────────┐
          │ actionguard → Executor      │  re-validates preconditions
          └─────────────┬───────────────┘
                        ▼
          ┌─────────────────────────────┐
          │ Verification (measure)      │  Δ pressure over window
          └─────────────┬───────────────┘
                        ▼
          ┌─────────────────────────────┐
          │ Ledger + metrics + CLI view │
          └─────────────────────────────┘
```

**Contract notes:**
- Each arrow is a value, not a callback. No layer calls "up".
- `NoAction` is a first-class decision with a reason — recorded, not omitted.
- The Verification step is what makes "success" mean *measured relief* (P5).

---

## 4. Process/goroutine model

| Component | Concurrency | Bounded by |
|---|---|---|
| Supervisor | 1 goroutine, owns ticker + event channel | — |
| Collector | n workers, one per source family, fan-in via bounded channel | per-source timeout; global tick deadline |
| Estimator/Forecaster | called synchronously on the tick goroutine (cheap, pure) | O(1) per tick |
| Attribution | 1 goroutine, may be skipped if tick overruns | snapshot budget; skip-on-backpressure |
| Policy/Planner | synchronous (pure) | O(actions) |
| Executor | 1 goroutine, serialized | action timeout + global action lock |
| Ledger writer | 1 goroutine, bounded channel, drop-oldest-with-counter | channel cap; fsync cadence |
| Pressure event listener | 1 goroutine (dispatch callback → channel) | bounded channel; coalescing |
| IPC server | per-connection goroutine, capped | max conns, rate limit, IO timeout |
| Self-monitor | 1 goroutine | reads own `/proc`-equivalent or Mach task info |

**Backpressure policy:** when the tick deadline is exceeded, the tick is *skipped*, not queued. Sampling is idempotent and time-relative; queuing stale samples produces wrong rates. A skipped tick is recorded as a gap (mirrors SBH's `incomplete observation` discipline).

**No unbounded goroutines anywhere.** Every goroutine has an owner and a documented exit condition.

---

## 5. State

**Four state classes, kept separate because conflating them is the product's main failure mode.**

| Class | Example | Who owns the truth | Stored where |
|---|---|---|---|
| **OS state** | macOS says `warning`; free pages = 6.2% | **macOS** — we only read it | `SystemSample`, tagged with source + quality |
| **Model state** | "pressure is `watch`; 78% confident we hit critical in ~14 min (`>410 s` with 90% coverage)" | Memory Pilot — a claim, not a fact | `PressureState`, `Forecast`, `Attribution` |
| **Decision state** | "mode `observe`; no safe relief → recommend only" | Memory Pilot — a judgment | `Decision` |
| **Action state** | "action `act-123` executed, verification `unverified`" | Physical reality — we can only observe it | `ActionResult`, `intent`, `verification` |

The four questions are genuinely different, and answering one does not answer another:

> macOS is at `warning` (OS) **≠** we predict exhaustion (model) **≠** we recommend something (decision) **≠** an action ran (action) **≠** pressure fell (outcome).

**Rules:**

1. **A downstream state never overwrites an upstream one.** A forecast does not change what the OS reported; a recommendation does not change the forecast; an executed action does not change the pressure reading.
2. **Each state carries its own uncertainty.** OS state carries `quality`; model state carries `confidence` + `calibration`; decision state carries its reason; action state carries `verdict`.
3. **Reports must not flatten them into one sentence.** "Memory is at 92% and climbing" mixes OS state (a number) with model state (a judgment) — see `cli-surface.md` for the required decomposition.
4. **An action's success never implies relief.** `outcome: succeeded` + `verdict: unverified` is a normal, honest record.

**This separation is what makes the product auditable.** When a user asks "why didn't it fix it?", the answer is a specific state class: "the model was confident but no provider was authorized" (decision), not a vague failure.

### 5.1 Storage

| Class | Storage | Survives restart |
|---|---|---|
| Ephemeral | current sample, EWMA window, tick counters | no (rebuilt) |
| Durable-config | policy mode, protected groups, thresholds | yes |
| Durable-history | samples (downsampled), events, decisions, actions, outcomes | yes |

**Rule:** ephemeral state is *always* reconstructible from history + config. If it is not, it does not belong in ephemeral state.

---

## 6. Failure domains

| Domain | Blast radius | Containment |
|---|---|---|
| One telemetry source | metric unavailable | per-source quality flag; SI-4 |
| Collector worker panic | that source only | worker supervisor records fault; demote (SI-7) |
| Ledger write failure | no history | switch to stderr fallback; demote to observe; never grow buffers unbounded |
| IPC failure | CLI can't query | daemon unaffected; CLI reports "daemon unreachable" with doctor hint |
| Executor fault | actions disabled | breaker; mode → observe |
| Daemon crash | everything | launchd restart; ledger tolerant of truncated tail; state rebuilt |
| Host OOM | everything | we predicted and recorded it; we did not cause it |

---

## 7. What is explicitly outside the process

- No privileged helper (NG-7).
- No browser extension, no app cooperation adapters in v1 (G-5 notes them as a later provider class).
- No system extension, no kext.
- No shelling out to `memory_pressure`, `purge`, or `killall` **in v1**. If a purge-style provider is added later, it must be (a) measured, (b) registered as a provider with declared expected effect, (c) gated behind `canary`. ADR-006 keeps this open.

---

## 8. Open architecture decisions

| ADR | Question |
|---|---|
| ADR-001 | Native pressure events only, polling only, or hybrid? (Recommendation: hybrid with events as primary, polling as liveness/heartbeat — SBH's implementation is the working proof.) |
| ADR-002 | Per-process footprint source: `proc_pidinfo(PROC_PIDTASKALLINFO)` libproc vs Mach `task_info` per PID vs `RUSAGE_INFO_V4 ri_physical_footprint_bytes`. |
| ADR-003 | Ledger: JSONL vs SQLite vs both. |
| ADR-004 | Adaptive sampling schedule & its floor. |
| ADR-005 | Forecast model complexity (baseline → EWMA → conformal → anything more). |
| ADR-006 | Which relief actions are real on macOS 13–26 and measurably effective? |
| ADR-007 | IPC framing: JSON-lines vs gRPC-lite vs length-prefixed. |
| ADR-008 | Resource budget numbers (draft in performance-budget.md). |
| ADR-009 | How much history is needed for anomaly/leak detection. |
| ADR-010 | Fallback trigger conditions. |
| ADR-011 | Is any app-cooperation provider (e.g. an app's own "release memory" API) worth the integration cost in v1? |

See `../adr/`.
