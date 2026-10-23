# MP-SAFE-001 — Safety Invariants

**Status:** Draft — these are binding; breaking one is a release blocker.
**Version:** 0.1.0

An invariant here is not a policy setting. It is a property of the artifact. If it can be disabled by configuration, it is not an invariant — it is a policy, and belongs in `action-authorization.md`.

Each invariant lists: the claim, why it exists, how it is *mechanically* enforced, and the test that proves enforcement still holds.

---

## SI-1 — No signaling of non-owned processes

**Claim.** No code path in the shipped binary can send a signal (including SIGSTOP/SIGCONT/SIGTERM/SIGKILL), call `proc_terminate`, `launchctl kill`, `task_terminate`, or `SetPriorityClass`-style suspension to any process other than one the daemon itself spawned.

### SI-1a — The exact boundary, stated plainly

Two different commitments are often conflated. This product makes the **narrow one**, deliberately:

| Commitment | In this product? |
|---|---|
| Never *coerce* another process: no signal, no suspend, no freeze, no terminate, no restart, no priority/QoS change without consent | **Yes — SI-1, absolute** |
| Never *influence* another process at all, including by calling an API it offers | **No — not the commitment we make** |

A cooperative provider would mean: an application publishes a documented API ("release caches", "trim buffers"), the user enables that integration explicitly, and the daemon calls it. The app decides what to release; nothing is taken from it, and if it declines, nothing happens.

**However, v1 ships no cooperative provider** (ADR-006, ADR-011b). The capability exists in the architecture so a future provider does not require redesign; it does not exist in the binary.

**Requirements if a cooperative provider is ever added:**
1. Explicit per-integration user opt-in — never on by default, never inferred.
2. The app's API must be public and documented by that app's vendor.
3. The provider must declare a measured `EffectRange` and a verification method.
4. It runs in `canary` before `enforce`.
5. It must be revocable at runtime without restarting the daemon.

**Why this distinction matters:** if the product's promise were "never influence anything", the entire provider abstraction would be dishonest architecture. If the promise is "never coerce", the architecture is coherent and the v1 emptiness is a staging decision, not a contradiction.

**Why.** Any such action risks destroying hours of agent work. The product exists because "just kill it" is an unacceptable answer.

**Mechanically enforced by:**
1. The executor's action type is a **closed sum type** containing only relief actions (cache purge requests *through the owning API*, log rotation of our own logs, memory-pressure advisory emission). There is no `Signal`/`Kill`/`Suspend` variant to construct.
2. Platform layer exposes no signal-sending wrapper. Where a syscall binding package would otherwise be imported, it is not linked.
3. `internal/actionguard` is the only package importing `os/exec`; it rejects any argument vector matching a signal-sending shape, and is tested against an explicit denylist.
4. Static analysis gate: a build-time check scanning the compiled dependency graph and source for `syscall.Kill`, `unix.Kill`, `SIGSTOP`, `SIGKILL`, `proc_terminate`, `task_terminate` in non-test code. The gate fails the build.

**Test:** `../05-testing/safety-tests.md` ST-1 (static), ST-2 (type-level), ST-3 (runtime deny on crafted config).

**Deliberate consequence.** Even a future user-requested "kill this process for me" command would violate SI-1 — it would have to be a separate binary, or SI-1 would have to be renegotiated in an ADR with the user. Keeping it out of this artifact is the point.

---

## SI-2 — Analysis cannot act

**Claim.** The collector, pressure estimator, forecaster, attribution, leak-suspicion, and policy-evaluation packages produce values only. They cannot perform I/O against the system beyond reading telemetry and writing to their own sinks.

**Why.** A wrong forecast must never *directly* cause a system change. The gap between "we think X" and "we do Y" is where evidence, authorization, and rate limits live.

**Mechanically enforced by:**
1. Package dependency direction: `internal/{pressure,forecast,attribution,leaksuspect,policy}` may import only `internal/{model,mathx,clock}` — never `internal/providers`, `internal/executor`, `internal/platform`.
2. A build check (`go list -deps`) asserting the forbidden edges do not exist. This is the "architecture test" pattern; it runs in CI and locally.
3. The policy package returns a `Decision` value; the executor is a separate consumer that re-validates the decision against current preconditions before acting (TOCTOU defense, mirroring pt's revalidation protocol).

**Test:** ST-4 (dependency-graph assertion), ST-5 (precondition re-check under mutated state).

---

## SI-3 — No destructive filesystem action on user data

**Claim.** The daemon never deletes, truncates, renames, or moves a file outside its own data directory (`~/Library/Application Support/mempilot/`).

**Why.** Disk cleanup is a different product (sbh). A memory tool that deletes files to "free resources" would be an unaccountable escalation of scope.

**Mechanically enforced by:**
1. All filesystem writes go through `internal/store`, which resolves every path against the data root and rejects any path that escapes it (`filepath.Rel` + prefix check, symlink-resolving).
2. No `os.RemoveAll` in non-test code; ledger rotation uses a bounded, enumerated segment list.
3. Log rotation only touches files matching our own naming convention inside the data root.

**Test:** ST-6 (path escape attempts, symlink tricks), ST-7 (rotation cannot touch non-mempilot files).

---

## SI-4 — Missing evidence never improves the verdict

**Claim.** No state transition from a more-severe pressure state to a less-severe one may occur on the basis of an `unavailable` or `degraded` metric. Warnings are cleared only by positive evidence.

**Why.** A collector that dies at the worst moment must not make the system look calm.

**Mechanically enforced by:**
1. Pressure state carries per-input quality; the state machine's "recover" transition requires `ok` quality on the inputs that raised it (sbh's `SBH-RAM-SCRATCH-INCOMPLETE` pattern, adopted).
2. Advisory reason codes are *sticky* until a complete healthy sample (same pattern, adopted).

**Test:** ST-8 (kill a source mid-warning; assert warning persists), ST-9 (partial coverage ⇒ no recovery).

---

## SI-5 — Uncertainty is always visible

**Claim.** Any output that asserts a future state, a cause, or a classification carries a confidence and the evidence that produced it. There is no schema-valid way to emit a bare claim.

**Why.** Users act on our reports. "Chrome is leaking" without qualification is a wrong answer presented as fact (charter L2).

**Mechanically enforced by:**
1. The JSON schema for `attribution`, `leak_suspicion`, and `forecast` marks `confidence`, `evidence[]`, and (for suspicion) `alternative_explanations[]` as required.
2. Human renderers cannot be constructed from an object lacking those fields (constructor returns `Result`).
3. Golden-output tests assert the rendered strings always include a confidence.

**Test:** ST-10 (schema rejection), ST-11 (renderer constructor rejection), ST-12 (golden outputs).

---

## SI-6 — The daemon degrades itself before the host degrades further

**Claim.** When host pressure rises, the daemon's own resource usage is monotonically non-increasing in (sampling rate × detail × logging × retention).

**Why.** On a 16 GB machine under pressure, our own work is part of the problem. sbh's "self-monitoring + CPU budget" idea, adopted.

**Mechanically enforced by:**
1. `internal/selfmonitor` owns a single `Budget` object that every worker consults before doing work; budgets shrink on pressure escalation.
2. A test asserts: for escalation notices in a synthetic sequence, `effective_work()` never increases.
3. Hard cap: at the RSS hard limit, the process exits with a documented code; launchd restarts it after ThrottleInterval. It does not attempt self-repair by growing.

**Test:** ST-13 (monotonic degradation), ST-14 (hard-limit exit path).

---

## SI-7 — Fail closed on internal error

**Claim.** Any panic in a worker, any failed ledger write, any schema mismatch, any unexpected nil → the daemon enters `observe`-only mode (or exits if even observation is unsafe). It never continues executing relief actions.

**Why.** A memory manager operating on corrupted state is more dangerous than no memory manager.

**Mechanically enforced by:**
1. Workers run under a supervisor that converts panic/recover into a fault event; the fault event drives a policy demotion (`MP-REQ-046`).
2. The executor checks a `healthy` flag set by the supervisor on every action; the default value is false until the first successful full cycle.
3. Ledger write failure is a first-class fault → demotion.

**Test:** ST-15 (panic injection in each worker), ST-16 (ledger failure → mode demotion).

---

## SI-8 — Single writer, single controller

**Claim.** At most one daemon instance writes the ledger or executes actions per (user, host).

**Mechanically enforced by:** advisory lock file with holder identity + TTL (pt's lock pattern, adopted) and exclusive bind of the Unix socket; both must be acquired before entering the main loop.

**Test:** ST-17 (two instances; second exits with a specific code and message).

---

## Invariant interaction rules

- SI-1 and SI-2 together mean: *the dangerous power is never acquired*, rather than *acquired and then restricted*. Prefer absence over policy.
- SI-4 and SI-5 together mean: *we can be wrong, but we cannot be quietly wrong*.
- SI-6 and SI-7 together mean: *when things go bad, we get smaller and quieter, and we stop acting*.

## What the invariants do not promise

They do not promise the product prevents every OOM event (charter L1). They promise the product never *causes* one and never *hides* one.

See also: `action-authorization.md`, `process-protection.md`, `failure-recovery.md`, `../05-testing/safety-tests.md`.
