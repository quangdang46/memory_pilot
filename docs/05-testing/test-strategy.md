# MP-TEST-001 — Test Strategy

**Status:** Draft
**Version:** 0.1.0

The spec is 80% of the work; the tests are what make the spec real. Every requirement in `../00-product/requirements.md` maps to at least one test here. A requirement without a test is a bug in the spec.

---

## 1. Test pyramid for this product

```
                 ┌──────────────────────┐
                 │  Soak (72h, real Mac)│  ~1 per release
                 ├──────────────────────┤
                 │  Integration (macOS) │  ~30 scenarios
                 ├──────────────────────┤
                 │  Replay (recorded)   │  ~50 traces
                 ├──────────────────────┤
                 │  Unit + property     │  ~400
                 └──────────────────────┘
```

The unusual weight at the *replay* layer is deliberate: the hard problems here (forecasting, attribution, calibration) are data problems, and the only honest way to test them is against recorded reality.

---

## 2. Layers

### 2.1 Unit & property tests

Pure functions: estimator math, projection, burst statistics, confidence composition, shape classification, group assignment, policy state machine, rate limiter, schema validation.

Property tests worth having:

| Property | Test |
|---|---|
| Monotonicity | increasing free bytes at any rate never produces a *shorter* ETA |
| Idempotence | re-processing the same sample twice does not change the estimate |
| Ordering | `Critical` never ranks below `Normal` in the score |
| Invariants | `confidence ∈ [0,1]` for arbitrary (including NaN-producing) inputs |
| No NaN leakage | adversarial floats (0, ±Inf, denormals) never propagate NaN into output |

The adversarial-float suite matters: a NaN in a confidence field would silently disable alerts.

### 2.2 Golden / snapshot tests

- Every CLI human output for a fixed ledger.
- Every JSON schema sample.
- Rendered advisory text (also used by the "never say 'is leaking'" string test).

Snapshots are generated, reviewed, and committed. Updating a snapshot requires a rationale in the commit message — a snapshot diff is a behavior diff.

### 2.3 Replay tests

A **trace** is a recorded stream of telemetry samples (from the real Mac, from integration runs, or synthetic). Replay drives the *real* pipeline (estimator → forecaster → attribution → policy in shadow) with the trace and asserts on outputs.

Trace format: JSONL, one sample per line, same schema as the ledger, plus an optional `expected` block for classification tests.

Minimum trace corpus:

| Trace | Purpose |
|---|---|
| `ramp-slow` | 3 h slow fill to warn |
| `ramp-fast` | 20 min fill to critical |
| `burst-single` | one 2 GB allocation event |
| `leak-like` | linear growth with flat CPU |
| `workload-growth` | growth with matching CPU/IO |
| `cache-sawtooth` | repeated rise/fall |
| `subprocess-storm` | many short-lived children |
| `swap-cliff` | swap rate step change |
| `flat-idle` | nothing happens (must produce zero advisories) |
| `source-loss` | a metric disappears mid-trace |
| `clock-jump` | monotonic vs wall divergence |
| `real-incident-*` | actual recorded OOM events (the most valuable) |

### 2.4 Integration tests (macOS runner required)

These need a real Mac because they exercise Mach/libproc/dispatch. They run in a dedicated harness that:

- runs as a normal user, no root;
- uses a **test pressure generator** (a child process that allocates and touches memory) to induce pressure in a controlled way — never by loading the machine with real agent sessions;
- asserts on observable state transitions and on the *absence* of forbidden actions.

| ID | Test |
|---|---|
| IT-1 | Collector against `vm_stat`/`sysctl` cross-check |
| IT-2 | Dispatch source delivers an event on induced pressure change |
| IT-3 | Fallback polling path when the dispatch subscription is disabled |
| IT-4 | Process enumeration coverage counters are plausible vs `ps` |
| IT-5 | Full pipeline in observe mode during induced pressure: correct state transitions, no actions |
| IT-6 | LaunchAgent install/start/stop/uninstall round trip |
| IT-7 | IPC: CLI reaches daemon; wrong-UID connection rejected |
| IT-8 | Sleep/wake: estimator resets, no bogus spike |
| IT-9 | Own-RSS budget under an 8 h synthetic workload |
| IT-10 | Ledger rotation under a forced small cap |

### 2.5 Fault injection

Covered in `fault-injection.md`. Implemented by build-tagged fault switches in the platform layer plus a `mempilot doctor --inject` dev command.

### 2.6 Soak / endurance

72 h with a realistic workload (real agent sessions). Assertions:

- RSS slope not significantly > 0 (OLS on hourly means, 95% CI includes 0);
- no unbounded growth in ledger segment count or in-memory sample store;
- no goroutine leak (count stable within ±2 after warmup);
- no tick skips beyond budget;
- action budget consumed at most as expected;
- zero forbidden actions (from the audit log).

---

## 3. Release gates

A release requires **all** of:

1. Unit + property + replay suites green.
2. Integration suite green on the target macOS version.
3. Safety suite (`safety-tests.md`) green — this is a hard gate, never waived.
4. Performance budgets met (`performance-budget.md`) on Mac mini M4 16 GB.
5. A 72 h soak with no budget breach and no forbidden action.
6. Snapshot diffs reviewed with rationale.
7. `mempilot doctor` clean on a fresh install.
8. The spec documents updated for any behavior change (spec-code parity is a gate, not a habit).

---

## 4. What cannot be tested automatically

Be honest about these, and cover them with a documented manual protocol:

- Whether the product actually reduces the frequency of the OS dialog on the owner's real workload (requires weeks).
- Whether a `leak_suspicion` is correct (requires Instruments confirmation).
- Whether a relief provider's effect generalizes across macOS versions.
- User-perceived latency impact.

---

## 5. Test data hygiene

- Recorded traces may contain process names and paths. Before committing a trace, apply the redaction rules from `../03-algorithms/telemetry-model.md` §6 and a redaction pass that replaces home-directory paths, user names, and command lines with stable placeholders.
- Real incident traces are the most valuable and the most sensitive. Store them with redaction; keep the raw ones only locally.

---

## 6. Related

- `replay-and-simulation.md`
- `fault-injection.md`
- `stress-and-soak.md`
- `performance-budget.md`
- `safety-tests.md`
