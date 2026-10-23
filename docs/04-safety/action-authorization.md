# MP-SAFE-004 — Action Authorization

**Status:** Draft
**Version:** 0.1.0
**Depends on:** `safety-invariants.md` (SI-1, SI-2), `../02-architecture/system-architecture.md`

This document defines what Memory Pilot is allowed to *do*, under what conditions, and how that permission is represented so that it cannot be bypassed by a bug in the analysis layer.

---

## 1. The closed action domain

Actions are a **closed sum type**. Adding an action is a code change with a spec change, never a configuration change.

```go
// internal/action — the ONLY package that may touch the system.
type Action interface{ actionTag() }   // sealed

type RequestCooperativeRelease struct {    // ask a REGISTERED adapter to shrink (ADR-014 T4)
    AdapterID   AdapterID
    BudgetBytes int64
}
type StopManagedWorkload struct {          // stop a process WE spawned, pre-authorized (ADR-014 L2)
    Target      OwnedPid                   // validated type: identity tuple + parent check, NOT a raw pid
    Reason      StopReason
}
type TrimOwnBuffers struct{}               // drop our own memory
type RotateOwnLedger struct{}              // rotate our own log segments
type EmitAdvisory struct {                 // produce a user-facing record
    Severity Severity
    Body     Advisory
}
```

**Nothing else exists.** In particular there is no `Signal`, `Suspend`, `Terminate`, `Restart`, `PurgeAll`, `RunCommand`, or `Exec` variant, and no way to name an arbitrary PID. `StopManagedWorkload` is the **only** externally-effective variant, and its target type makes an unowned PID unconstructible (ADR-014 §2). A future feature cannot "just add" one without editing this file and the corresponding ADR — which is exactly the friction we want.

**Retired name:** `RequestReclaimFromProvider` no longer exists; the cooperative path is `RequestCooperativeRelease` through a registered adapter (ADR-014, owner naming decision). Any code or doc still using the old name is a contract bug.

### 1.1 Why no generic "run this command" escape hatch

A generic command action re-introduces every risk SI-1 forbids, through a string. If a relief mechanism requires a command, it must be a *named provider* with its own spec, preconditions, and effect measurement (§4).

---

## 2. Provider contract

A provider is a mechanism that claims it can return memory to the system. Providers are registered explicitly, each with a declaration:

```go
type Provider struct {
    ID          ProviderID
    Kind        Kind            // subsystem-level: kernel, app-cooperative, self
    Cost        Cost            // latency, CPU, risk to user work
    Expected    EffectRange     // bytes reclaimed, as a range with confidence
    Preconditions []Precondition
    Verify      VerifySpec      // how we measure whether it worked
    Reversible  bool
    Notes       string          // including measured evidence citation
}
```

`Expected.EffectRange` is a *measured* range from a recorded experiment, cited in `Notes`. A provider with no measurement is registered as `StatusUnproven` and can never run outside `observe` mode.

### 2.1 v1 provider roster

| Provider | Status | Effect | Runs in | Notes |
|---|---|---|---|---|
| `TrimOwnBuffers` | ✓ implemented | frees own RSS back to OS | all modes | trivial, always safe |
| `RotateOwnLedger` | ✓ implemented | disk only | all modes | does not reduce RAM pressure; kept for disk headroom |
| `EmitAdvisory` | ✓ implemented | 0 bytes | all modes | the honest "we cannot fix this" path |
| `RequestCooperativeRelease` | ⚠ framework + `StatusUnproven` adapters | declared per adapter | `observe` plans; executes in `canary`+ after measurement | ADR-014 §1 |
| `StopManagedWorkload` | ✓ managed-only, per-launch authorized | bounded (the workload's own footprint) | `canary`+ after canary evidence | ADR-014 §2 |
| `memory_pressure`-style purge request | ⏳ investigate | unknown; may hurt | **never in v1** | ADR-006 |
| App-cooperative release via a **generic/universal** API | ✗ does not exist | — | **impossible, not merely out** | no such macOS API |
| `SuspendManagedWorkload` | ✗ not admitted | no measured RAM drop | **never in v1** | ADR-014 (needs a separate ADR with evidence) |

**If the only available relief is `EmitAdvisory`, that is a legitimate outcome** and the product says so. A tool that always finds something to do is lying about its capabilities.

---

## 2.2 Capability in the architecture vs. provider enabled in v1

These are two different questions and the docs must not blur them:

| Question | Answer |
|---|---|
| Can the architecture express a cooperative release request? | **Yes** — `RequestCooperativeRelease` + the registered-adapter contract (ADR-014) |
| Is a cooperative adapter enabled in v1 by default? | **No** — framework ships, every adapter is off until the user enables that integration |
| Can an unproven adapter run at all? | **No** — `StatusUnproven` adapters cannot run outside `observe` |
| Can v1 stop a process the daemon did not spawn? | **No** — `StopManagedWorkload` takes an `OwnedPid`; an unowned target is unconstructible |

See SI-1a for the *safety* boundary this supports (never coerce an unwilling/non-owned process; may request from a consenting, registered app).

### The v1 rule, stated so it cannot be misread

> **In v1 the only action that affects another process is `StopManagedWorkload`, restricted to processes the daemon itself spawned under explicit prior authorization, identity-revalidated at execute time. A cooperative adapter enters the active set only after (a) a written design decision, (b) a measured effect on the target machine, and (c) a safety verification — in that order.**

This matters because `canary` and `enforce` are *modes*, and a reader could interpret "mode: enforce" as "all providers are permitted". It is not:

```
Mode governs WHICH ALLOWLISTED actions may run.
The allowlist governs WHAT EXISTS as a runnable action.
A mode can never promote a provider that is not on the allowlist,
and can never widen an action's target set beyond its type's construction rules.
```

Concretely, `enforce` is not "everything is allowed". It is "the allowlisted provider set may run up to its full budget instead of a canary-limited one". No mode setting can make `StopManagedWorkload` target an unmanaged PID, because `OwnedPid` cannot be constructed for one; and no mode setting can call an adapter the user has not enabled.

**Test (T-AA-10):** construct a config with `mode = enforce` and a provider registry containing only unproven providers; assert zero actions can execute and every decision records `no_safe_relief`. A build that fails this test has confused mode with permission.

**Consequence for reviewers:** encountering `app-cooperative` in a type is not evidence that the product can touch another app. Encountering it in the **enabled allowlist** would be — and that requires an ADR, a measured effect, and canary evidence first.

---

## 3. Why `memory_pressure` / `purge` is not in v1

macOS ships `memory_pressure(1)` with a `-l`/purge-like mode, and several third-party "memory cleaners" shell out to it. The README of one audited reference (Boost) reportedly uses `memory_pressure` to request that the system release reclaimable memory.

**We do not adopt it in v1 because none of the following is established:**

1. **Does it actually reduce pressure?** Freeing file cache reduces "memory used" while providing no headroom benefit under pressure (macOS already evicts cache when needed). Purge can make the *next* few minutes slower because caches must be re-read from disk.
2. **What is the latency/IO cost?** Purging means re-reading from SSD afterwards. On a 256 GB machine already swapping, this can deepen the problem.
3. **Is it safe to invoke repeatedly?** Unknown. It is a system-wide operation with no per-app scoping.
4. **Does it have any effect when the compressor is the consumer?** Likely not — compressed pages are not "purgeable cache".

Test plan (ADR-006): induce controlled pressure with a *test* process, measure `vm_stat` before/after, measure UI latency proxy (e.g. a fixed benchmark), and the time to re-warm. Only if purge shows a net benefit under a defined condition does it ever become a provider — and it would be `canary`-only.

---

## 4. Preconditions and the TOCTOU rule

Every action is validated **at decision time** and **again immediately before execution**, against freshly read state. This is pt's revalidation discipline applied even though we do not signal processes.

Reasons this matters for us:

- the pressure state may have resolved by the time an action is planned;
- the provider's subsystem may have become unavailable;
- the daemon may have been demoted by a fault between plan and execute;
- a user may have changed policy in the interim.

Execution contract:

```
execute(decision):
  1. if supervisor.healthy == false        -> refuse(not_healthy)
  2. if policy.mode < decision.required_mode -> refuse(mode_demoted)
  3. if now - decision.created_at > max_age -> refuse(stale_decision)
  4. for each precondition: recheck; fail -> refuse(precondition: <which>)
  5. if rate_limit exceeded for this action class -> refuse(rate_limited)
  6. if breaker open for this class         -> refuse(breaker_open)
  7. perform, with timeout
  8. record outcome + schedule verification
```

Refusals are recorded as first-class events. A refusal is a success of the safety system, not a failure of the product.

---

## 5. Policy modes

| Mode | Actions permitted | Purpose |
|---|---|---|
| `observe` (default, and the state after any fault) | `TrimOwnBuffers`, `RotateOwnLedger`, `EmitAdvisory` (+ planning for everything else, execution for none) | Only self-affecting actions execute. Safe forever. |
| `canary` | observe + `RequestCooperativeRelease` (enabled adapters) + `StopManagedWorkload` (pre-authorized managed targets), each up to its own strict budget class, with post-hoc comparison logged | Limited live trial; every invocation compared against what observe-mode would have done |
| `enforce` | canary + full allowlisted provider budget | Earned only after canary evidence |

**Promotion is manual by default.** `mempilot policy promote` requires the canary evidence report to exist and meet the criteria (min invocations, verified effect, no adverse events). Automatic promotion is out of scope for v1 (sbh has an auto-recover-to-canary mechanism after clean windows; we may adopt it later for *recovery from fallback*, not for promotion).

**Demotion is automatic and immediate** on any of:
- action failure/ineffectiveness streak,
- forecast degradation alarm,
- ledger write failure,
- worker panic,
- self-monitor budget breach,
- invalid config detected at runtime.

Demotion is recorded with the triggering reason.

---

## 6. Rate limits and breakers

| Control | Default (tunable) | Rationale |
|---|---|---|
| per-provider min interval | 15 min | avoid churn |
| per-provider hourly cap | 2 | a provider invoked repeatedly is either ineffective or harmful |
| global hourly action cap | 6 | bounds total system disturbance |
| ineffectiveness backoff | ×2 per consecutive ineffective result, cap 24 h | MP-SCN-108 |
| breaker: consecutive failures | 2 | open the breaker, require manual clear or long cooldown (24 h) |
| cooldown after breaker | 24 h | |

All counters persisted so a restart does not reset the budget (a crash loop must not produce an action storm).

### 6.1 Budgets are charged on execution, not on approval

**`[Verified: sbh]`** sbh's canary hourly budget was originally charged per *approval*. In production that spent **1,165 approvals for 15 actual deletions** — the budget looked exhausted while almost nothing had happened, and a single evaluation pass could approve far more than it executed (`policy.rs:1760-1780`, `:1863`). The fix: charge on **executed** actions, and additionally count in-evaluation approvals so one batch cannot overshoot.

**Adopt:** a rate-limit budget counts `outcome ∈ {succeeded, failed, timed_out}` — i.e. a syscall that actually happened — never a plan, a decision, or an approval. A refused action costs nothing (otherwise a misconfiguration that produces refusals would silently disable relief when it is finally needed).

### 6.2 A recorded decision outranks live configuration

**`[Verified: pt]`** pt's `PolicyEnforcer::check_action` evaluates the **policy recorded at plan time first**, before current config (`decision/enforcer.rs:544`). Consequence: tightening or loosening the config between planning and execution cannot retroactively change what a recorded decision was authorized to do.

**Adopt.** Our `Decision` record carries the policy snapshot it was made under; the executor validates against **that plus the current safety invariants**, not against current user config. Otherwise a config edit during a pressure event could loosen a limit that was about to be applied.

**Limits of the snapshot — it can never be a bypass.** The executor's check is the *conjunction* of four things, and the snapshot is only one of them:

| # | Checked | Can a snapshot override it? |
|---|---|---|
| 1 | safety invariants (SI-1..SI-8) | **never** — they are properties of the code, not configuration |
| 2 | current daemon health and ledger writability | no |
| 3 | current mode (a demotion takes effect immediately) | no — `mode_demoted` refusal |
| 4 | preconditions re-read live, decision age, rate limits, breakers | no |
| 5 | policy snapshot recorded with the decision | only here does the snapshot apply |

So a snapshot can only ever say "this action had been authorized under a policy at plan time". It cannot authorize anything the invariants forbid, cannot outlive a demotion, cannot outlive a breaker, and cannot outlive a failed precondition. Test: T-AA-9 constructs a decision whose snapshot allows an action, demotes the mode, and asserts refusal.

### 6.3 Emergency escalation must be bounded and self-limiting

**`[Verified: sbh]`** sbh allows an automatic promotion out of its safe fallback when the system is in real danger (fallback + Yellow+ for 5 min + kill-switch not set), and then imposes a **30-minute no-reentry grace** (`policy.rs:1453-1505`). Similarly, its guard's caution penalty is *scaled down by pressure* ({Green 0, Yellow .25, Orange .10, Red 0, Critical 0}) because full caution at high pressure produced deadlocks on real hosts (`:1739-1758`).

**Adopt the shape, with our own limits:**
- Pressure may raise the *willingness to advise*, and may relax *conservatism in how we phrase a warning*.
- Pressure may **never** authorize an action that the allowlist or mode forbids — we have no destructive capability to trade away, which is exactly why this product can be simpler and safer than sbh here.
- Any automatic relaxation must carry a bounded duration and a re-entry grace, and be recorded with its reason.

---

## 7. What each action must report

```jsonc
{
  "action_id": "...",
  "class": "RequestCooperativeRelease",
  "provider_id": "...",
  "decision_id": "...",
  "mode": "canary",
  "preconditions": [{"name":"...","ok":true,"observed":"..."}],
  "executed_at": "...",
  "duration_ms": 140,
  "outcome": "succeeded",           // succeeded | refused | failed | timed_out
  "refusal_reason": null,
  "expected_effect_bytes": [1e8, 4e8],
  "verification": {
    "window_s": 120,
    "pressure_before": {...},
    "pressure_after": {...},
    "observed_effect_bytes": 2.2e8,
    "verdict": "effective"          // effective | partial | ineffective | unverified
  }
}
```

`verdict` defaults to `unverified` unless a completed measurement exists. Never `succeeded` alone (P5).

### 7.1 Write the intent before the operation

**Invariant (SI-7 extension): if the intent cannot be durably recorded, the action must not run.** A failure to write the intent is a *refusal* (`refusal_reason: "intent_not_durable"`), never a reason to proceed "to save the system". This follows from the general rule that a fault demotes rather than permits.

**`[Verified: pt]`** pt's rate limiter uses **durable two-phase accounting**: `begin_kill_accounting` writes a pending intent *before* signalling, and `finish_kill_accounting` charges only if the signal was actually delivered (`decision/rate_limit.rs`). A crash between the two leaves a pending record that is resolved on restart. Result: a crash loop cannot reset the budget, and an action whose outcome was never observed cannot silently disappear.

**Adopt, with our action domain.** Because our actions are far less dangerous, the stakes are lower — but the property is still worth having, and it costs one extra ledger write:

```
1. record action_intent{id, class, provider, planned_at, decision_id}   (fsync)
2. perform
3. record action_result{id, outcome, duration, ...}
```

On startup, any `action_intent` with no matching result is recorded as `outcome: unknown_crash_window` and **counts against the budget**. An action we cannot prove did not happen must be treated as having happened.

#### Recovery contract (idempotent, exactly-once)

1. **Identification.** An intent is "unresolved" iff a record with `type: action_intent, id: X` exists and no record with `type: action_result, action_id: X` exists. Matching is by `action_id` only — never by timestamp or provider, which can collide.
2. **Idempotency.** The recovery pass is keyed by action id in a set persisted across restarts. Replaying the recovery pass produces **exactly one** `unknown_crash_window` result per intent. A second pass finds the result already written and does nothing. (Test: run recovery 3×; assert 1 result record.)
3. **No re-execution.** Recovery **never** re-runs the action. It records what it could not prove and moves on. Re-running a relief action whose first attempt may have succeeded would double-apply it.
4. **Budget.** An `unknown_crash_window` result charges the budget exactly as a `succeeded` would, and additionally increments a counter `intents_lost` surfaced by `doctor`. A nonzero `intents_lost` is a bug signal, not a normal condition.
5. **Ledger append-only.** Results are appended; an intent is never rewritten or deleted. `explain` shows both records in order.
6. **Intent write failure.** If the fsync of the intent fails, the executor refuses `intent_not_durable` and does not perform the action. A ledger that cannot record an attempt is a ledger that cannot be trusted to have recorded its effects.

The `ActionResult.outcome` enum therefore contains five values, not four: `succeeded | refused | failed | timed_out | unknown_crash_window`. `unknown_crash_window` is a *result* value rather than a separate event type so that there is exactly one record shape per attempt — this is the choice recorded in `../appendix/schemas.md` §8.

### 7.2 Loss matrices need a design rule, not just numbers

**`[Verified: pt]`** pt documents a rule for its cost matrix that is worth carrying over even though we have no such actions (`expected_loss.rs` / `policy.rs:82-107`):

> For the classes where an intervention *helps*, every non-terminal action must cost approximately as much as doing nothing — because a half-measure leaves the resource held. Otherwise the argmin never selects the only action that resolves the problem. Observed in a 20-host fleet: 0 kills, everything became `renice`.

**The transferable lesson** is not about kills. It is: **a cost model where the ineffective-but-cheap option always wins will silently never do anything.** For Memory Pilot this is a live hazard — our `NoAction` is always available and always "free", so a planning layer that minimizes cost will always prefer it. That is *correct* for us (we prefer no action), but it means the planner must be evaluated by **whether it ever relieves pressure when relief is possible**, not by whether it chose the cheapest option. Otherwise the product ships a plan generator that always outputs "do nothing" and every metric looks healthy.

Concrete test: a synthetic scenario where a provider *is* available and could help must produce a decision to use it. If the planner never fires even with a positive-expected-benefit provider, the cost model is broken.

### 7.3 Two divergent defaults are a bug

**`[Verified: pt]`** pt ships two conflicting loss matrices and two conflicting robot-mode budget defaults (code vs. JSON schema). Nobody knows which one runs. This is a class of defect, not an incident.

**Adopt as a rule:** exactly one default per constant, defined in code, generated into docs (`appendix/constants.md`), with a staleness test. Never a config-file default that can drift from a code default.

---

## 8. Prohibited action forms (enumerated for review)

A code review must reject any change introducing:

- a shell invocation (`os/exec`, `exec.Command`) outside `internal/actionguard`'s vetted adapter;
- any argument string containing a signal name or `kill`/`pkill`/`killall`;
- any import of a package whose purpose is process control;
- any filesystem write outside `internal/store`;
- any widening of the `Action` sum type without a spec update and ADR;
- any provider that cannot state a verification method.

---

## 9. Tests

| Test | Proves |
|---|---|
| T-AA-1 | Executor rejects a decision whose mode requirement exceeds current mode |
| T-AA-2 | Executor rejects a stale decision (> max_age) |
| T-AA-3 | Precondition flip between plan and execute yields `refused(precondition)` |
| T-AA-4 | Rate limiter enforces interval and cap across restarts |
| T-AA-5 | Breaker opens after N failures and blocks for cooldown |
| T-AA-6 | `unverified` is the default verdict; `effective` requires measurement |
| T-AA-7 | Fault → mode demotes → subsequent actions refused |
| T-AA-8 | Sealed action type: adding a new variant requires the spec (compile-time + golden check of the type list) |
