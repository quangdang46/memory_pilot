# Protection Actions — the tier system

**Status:** Draft
**Date:** 2026-10-23
**Supersedes** the flat three-item allowlist in `../04-safety/action-authorization.md` §2.1.
**Related:** ADR-013

---

## 0. What changed and why

The previous spec had a flat allowlist of three actions, all of which only affected the daemon itself. That was safe and it was **useless for the stated problem**: it could not prevent a single out-of-memory event caused by other processes.

The replacement is a **tiered system ordered by what each tier can break**, not by what it achieves. A tier is enabled or disabled independently, and the default is the set that breaks nothing.

| Tier | Can break | Default in v1 |
|---|---|---|
| T1 — own memory | nothing | **on** |
| T2 — managed launch points | nothing | **on** |
| T3 — verified orphan termination | a detached long-running task | **off** (per-user opt-in) |
| T4 — cooperative app release | depends on the app | **off** (nothing to call) |
| T5 — ask the user | nothing | **on** |

---

## 1. T1 — Shrink own memory

| | |
|---|---|
| Actions | `TrimOwnBuffers`, `RotateOwnLedger`, `ReduceSamplingDetail` |
| Effect | small (tens of MB) |
| When | own RSS above budget, or host pressure ≥ prevent |
| Preconditions | none |
| Verification | own RSS before/after; recorded |
| Why it exists | the tool must never be the cause (RC-7). It is not a solution to anyone else's pressure and is never presented as one. |

**Honesty requirement:** T1 must never appear in a report as "relief". It is *self-maintenance*. A report that lists "freed 40 MB" while the system needs 2 GB is misleading.

---

## 2. T2 — Managed launch points

**The highest-value tier, and the only one whose effect is both large and risk-free.**

| | |
|---|---|
| Mechanism | a launch point the user goes *through*: `mp run <cmd>`, a shell function, a terminal profile hook |
| Decision | admit / admit-with-warning / defer |
| Effect | prevents an allocation that has not happened yet |
| Can break | nothing — nothing was running |
| Reversible | trivially (run again later, or `--force`) |
| Coverage | **only launches that pass through the point** |

### 2.1 The decision rule

```
budget       = headroom_now − safety_reserve
expected     = p50 footprint for this class (history → global → unknown)
worst        = p90 footprint

expected > budget            → defer        (offers: wait / force / lighter profile)
worst > budget ≥ expected    → admit + warn (recorded)
otherwise                    → admit
no estimate available        → admit + warn (never block on ignorance)
```

### 2.2 What defer must do

A refusal is only acceptable if it is actionable. `mp run` deferring must print:

- the numbers (`headroom 480 MB`, `this project's sessions usually take ~1.1 GB`),
- why (`2 sessions are growing; forecast says critical in ~12 min`),
- the options (`wait`, `--force`, `close session X whose resume id is …`),
- and must **not** silently fail.

A launcher that appears broken is worse than no launcher.

### 2.3 Coverage honesty

If the user starts work outside a launch point, T2 has **no effect**. The product must say this once, clearly, at install time and in `doctor` — not bury it. The managed path must be *more convenient* than the unmanaged one, or it will not be used and the tier is decorative.

### 2.4 What T2 cannot do

It cannot stop a running process from allocating more. It only gates new starts. This is a real limitation and must not be glossed over in the report language.

---

## 3. T3 — Verified orphan termination

**Off by default. Off by default. Off by default.** The repetition is deliberate: this is the only tier that can destroy work.

### 3.1 Why it is even considered

RC-3 (orphaned children accumulating) is the one root cause where the owner is already gone, so terminating the process destroys no live context. It is also the only cause with a *conceivable* automatic remedy.

### 3.2 Why it is off by default

A process whose parent has exited and which sits idle is **indistinguishable** from:

- a long-running build deliberately detached,
- an agent waiting on a slow network call whose parent shell exited,
- a tool the user started with `nohup` and intends to collect later.

Terminating any of these destroys work with no resume path.

### 3.3 The evidence bar

Every one of these, recorded in the decision record:

1. Parent identity `(pid, start_sec, start_usec)` unresolvable — checked twice, ≥ 60 s apart.
2. Process ancestry terminates at `launchd`.
3. CPU < 1 % and no significant I/O for ≥ 10 min.
4. Not a registered launchd service; not SIP-protected; not owned by another user.
5. No adapter reports a live session owning it.
6. The user has enabled T3 for this process class.

### 3.4 Three sub-modes

| Mode | Behavior | Default |
|---|---|---|
| `detect` | list orphan candidates in `report` / `status` with evidence | **on** |
| `recommend` | surface a specific advisory with the evidence and a one-command action | **on** |
| `act` | terminate automatically after all checks | **off** — requires opt-in, per-class confirmation, and a measured false-positive rate |

**v1 ships `detect` + `recommend` only.** `act` requires ADR-013 to be amended with real false-positive data from the owner's machine.

**Schema vs binary (ADR-013):** the decision schema's closed action enum *can represent* `RequestGracefulQuit` and `TerminateVerifiedOrphan` so that the policy layer can be designed, tested with fixtures, and reasoned about in traces. The v1 executor has no code path that performs them — there is nothing disabled-by-flag, there is something not-yet-written. ST-1/ST-2 verify this at build time.

---

## 4. T4 — Cooperative app release

Reserved for a documented API from an application that lets another process ask it to shrink. **No such API is known for any target application today.** The tier exists so a future integration does not require an architectural change; it ships empty.

Requirements if ever populated:

1. Documented, public API — no private/undocumented interfaces.
2. Explicit per-integration opt-in.
3. Measured `EffectRange` and a verification method.
4. Canary before enforce.
5. Revocable at runtime.

---

## 5. T5 — Ask the user

| | |
|---|---|
| Actions | `EmitAdvisory`, `RecommendCloseSession`, `RecommendReduceConcurrency` |
| Effect | depends on the user |
| Can break | nothing |
| Quality bar | the advisory must be **actionable**: named group, numbers, trend, and — where a resume path exists — the exact command |

### 5.1 The resume-aware recommendation

`[Verified on this machine]` Claude Code persists sessions as JSONL under `~/.claude/projects/<project>/<session-id>.jsonl` and supports `claude --resume <id>`. **This is a general capability of that agent, discovered via its CLI — it is an adapter-level fact, not a core assumption.**

The core emits `RecommendCloseSession{group, footprint, evidence}`. An adapter *may* enrich it with a resume command. Without the adapter, the recommendation is still valid and still useful — it just cannot promise recoverability, and its wording must not imply that it can.

**Rule:** the core never claims a session is resumable. Only an adapter that has verified the resume mechanism may add that claim.

---

## 6. Composition: how tiers combine in one incident

```
pressure rises
   │
   ├─ T1 if our own footprint is material            (always)
   │
   ├─ T2 at the next launch attempt                  (if the launch goes through us)
   │
   ├─ T5 advisory, always, with evidence             (always)
   │
   ├─ T3 candidates listed (detect), recommended     (if T3 enabled at all)
   │
   └─ T4 only if a verified provider exists          (empty today)
```

Multiple tiers may fire in one incident. Each records its own outcome and its own verification. **The report must distinguish which tier produced which effect** — otherwise a 40 MB self-trim gets credited with averting a 2 GB crisis.

---

## 7. Verification, per tier

| Tier | Verified by | Success means |
|---|---|---|
| T1 | own RSS delta | our footprint fell; says nothing about system pressure |
| T2 | the launch did/did not happen; pressure trajectory afterwards | we prevented an allocation; the counterfactual is unobservable and must be described as such |
| T3 | pressure before/after + the orphan's footprint returned | pressure fell **and** no complaint from a live session within a window |
| T4 | provider-declared measurement | provider reports a measured release |
| T5 | user action observed (session count fell, or pressure fell) | we can at best observe correlation, never causation |

**T2's success is structurally unprovable** — you cannot measure the crisis that did not happen. The right response is to report it as `prevented (counterfactual, unobservable)` rather than claiming a measured effect. This distinction belongs in the ledger and in the report.

---

## 8. What is still not possible

| Not possible | Consequence |
|---|---|
| Force any app to release heap | T4 stays empty; RC-4 unsolvable from outside |
| Stop a running process from allocating | only T3 (destroys work) or nothing |
| Guarantee no OOM ever | the honest ceiling; stated in the charter |
| Prevent launches outside managed points | T2 coverage limit |
