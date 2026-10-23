# MP-TEST-003 — Replay & Simulation

**Status:** Draft
**Version:** 0.1.0

Replay is how we test a data-driven product honestly. The rules:

1. The replay harness drives the **real** pipeline. No reimplementation in the test.
2. Traces are recorded from reality whenever possible.
3. Synthetic traces exist only to test a specific mechanism with a known ground truth.
4. Every trace has a declared expectation; anything unexpected fails.

---

## 1. Trace format

JSONL, one record per line, same shape as the ledger's hot tier plus a header:

```jsonc
{"type":"trace_header","v":1,"name":"ramp-fast","source":"synthetic","macos":"15.6","arch":"arm64","machine":"M4/16GB","notes":"..."}
{"type":"sample","t":0.000,"sys":{"pages_free":812345,"pages_compressor":12000,"swap_used":0,"kernel_level":1},"proc":[{"id":"(501,1729,0)","resident":540000000,"cpu_ns":1234567}]}
...
{"type":"expect","at_end":{"max_state":"critical","warn_before_critical_s":600,"advisories":["pressure_forecast"],"forbidden":["action:any"]}}
```

`proc[]` is truncated to the top-K contributing processes for size; the harness treats absence as "no new information".

---

## 2. Corpus

| Trace | Ground truth | Asserts |
|---|---|---|
| `flat-idle` | nothing | zero advisories, ≤ 1 state transition, zero actions |
| `ramp-slow` | linear fill over 3 h to Warn | Watch before Prevent; ETA error < 25 %; confidence rises with samples |
| `ramp-fast` | fill to Critical in 20 min | Critical forecast ≥ 5 min before arrival (on the trace's own timeline) |
| `burst-single` | one 2 GB allocation | classified burst (not trend); no escalation beyond Watch on the burst alone |
| `leak-like` | linear growth, CPU flat | `leak_suspicion` with alternatives present; phrase test passes |
| `workload-growth` | growth ∝ CPU | `workload_growth`; no suspicion |
| `cache-sawtooth` | repeated rise/fall with plateau | `cache_or_retrievable`; no escalation on peaks |
| `subprocess-storm` | child count grows | `subprocess_accumulation`; group total attributed |
| `swap-cliff` | swap rate step | swap advisory; state → Prevent; disk correlated |
| `source-loss` | vm_stats dies mid-trace | state does not recover; `unknown` fields |
| `clock-jump` | wall jumps | rates sane; warmup |
| `real-incident-*` | a recorded OOM event | site-specific golden expectations; used for calibration |

---

## 3. Running

```
mempilot replay --trace testdata/traces/ramp-fast.jsonl --mode observe --out /tmp/replay.json
mempilot replay --trace ... --compare-baseline    # vs. the naive threshold baseline
```

Output: a report with the same fields as the ledger (decisions, advisories, calibration), plus a diff against expectations.

**Determinism:** replay must be bit-identical across runs and machines. Sources of nondeterminism (map iteration, time, randomness) are forbidden in the pipeline; a test runs each trace twice and diffs.

---

## 4. Building the corpus

Recording is a first-class feature:

```
mempilot record --name my-incident --duration 2h --out ./traces/
```

Constraints:
- Redaction applied at write time (paths, usernames → placeholders) — never rely on post-hoc scrubbing.
- A recording of a real incident should capture ±15 min around it; the daemon can auto-mark an incident window when state reaches Critical and preserve full-resolution data for it (a "snapshot on incident" feature).
- Recorded traces are reviewed before committing: they contain process names, which can reveal what software the user runs. That is acceptable in a private repo; the policy must be stated.

---

## 5. Baseline comparison

Every tuning change must be evaluated against:

1. **Naive baseline** (threshold on free %),
2. **previous release** on the same corpus.

Report: lead time, false-positive rate, ETA MAE, calibration coverage. A change that improves one metric while worsening another must state the trade explicitly (e.g. "earlier warnings, +3 % false positives").

This is the product's equivalent of a benchmark suite, and it should be treated with the same rigor: no unmeasured tuning.

---

## 6. What replay cannot prove

- Real dispatch event latency (simulated).
- Real process enumeration cost (the trace carries the samples, not the syscalls).
- Behaviour under a genuinely thrashing machine (the trace cannot reproduce the I/O).

Those belong to the macOS integration and soak tiers (`test-strategy.md` §2.4, §2.6).
