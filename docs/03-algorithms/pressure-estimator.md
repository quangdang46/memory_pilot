# MP-ALG-002 — Pressure Estimator

**Status:** Draft
**Version:** 0.1.0
**Depends on:** `telemetry-model.md`
**Open questions:** Q-3 (threshold derivation), ADR-004 (sampling), ADR-005 (forecast model)

---

## 1. Why an estimator and not a threshold

The user's problem statement is "the Mac says it's out of memory". The naive fix is "warn at 85% RAM". That is wrong for reasons Apple documents and the user has already observed:

- macOS uses free RAM for file cache; low free RAM is *normal and desirable*.
- Compression means "used" RAM includes compressed pages that cost less than their logical size.
- Two machines at the same % used can have very different pressure.

So the estimator's job is to produce an ordered state plus a scalar "how bad", from *several* signals, with **explicit precedence** and **explicit provenance**.

---

## 2. Inputs

| Input | Type | Source | Quality flag |
|---|---|---|---|
| `kernel_level` | normal \| warn \| critical \| **unavailable** | `kern.memorystatus_vm_pressure_level` (1/2/4) | `ok` / `unavailable(reason)` |
| `free_pct` | 0..100 | `free_count / total_pages` | `ok` / `unavailable` |
| `compressor_pct` | 0..100 | `compressor_page_count / total_pages` | `ok` / `unavailable` |
| `swap_used_bytes` | u64 | `sysctl vm.swapusage` | `ok` / `unavailable` |
| `swap_rate_bps` | f64 | derived (EWMA over swap_used) | `ok` / `warming` / `unavailable` |
| `disk_free_bytes` | u64 | `statfs` boot volume | `ok` / `unavailable` |
| `event_state` | normal\|warn\|critical, or none | dispatch source | `fresh(age)` / `stale` / `absent` |

**Rule (SI-4):** the estimator receives quality flags and must propagate them. It may not "fill in" a value.

---

## 3. State model

Five states. Note this is a *pressure* scale, not a *disk* scale — deliberately not sbh's Green/Yellow/Orange/Red/Critical, because our escalation logic differs (see §6).

| State | Meaning | Typical trigger |
|---|---|---|
| `Normal` | comfortable headroom | kernel normal, or fallback says normal |
| `Watch` | pressure is rising but not yet uncomfortable | free_pct falling at a sustained rate; compressor growing; kernel still normal |
| `Prevent` | pressure is materially elevated; relief should be considered | kernel warn, or fallback warn |
| `Critical` | pressure is severe; survival actions / advisory | kernel critical, or fallback critical |
| `Unknown` | insufficient evidence to classify | all sources unavailable, or contradictory |

`Unknown` is a real state, surfaced in CLI and ledger. It is not an alias for `Normal`.

---

## 4. Derivation

### 4.1 Primary path (kernel level available)

```
if kernel_level == ok:
    base = { 1: Normal, 2: Prevent, 4: Critical }[raw]
    # Watch is derived: kernel says normal, but the trend says it won't stay that way
    if base == Normal and trend_risk >= WATCH_TREND_THRESHOLD:
        state = Watch
    else:
        state = base
```

Rationale: the kernel's own assessment is the authoritative current state; the trend can **elevate** Normal→Watch but may **never lower** a kernel-reported state. `[Adopt from sbh: kernel level overrides derived estimate, pal.rs:492-506]`

### 4.2 Fallback path (kernel level unavailable)

Use the sbh-shaped inference, with our own thresholds:

```
free_pct  = ...
comp_pct  = ...
swap_hot  = swap_used_bytes > 0 and swap_rate_bps > 0

Critical  if free_pct < F_C and swap_hot
Prevent   if free_pct < F_P and (comp_pct >= C_P or swap_hot)
Watch     if free_pct < F_W or comp_pct >= C_W
else       Normal
```

sbh's values (2 / 5 / 10 / 10) are a starting point *for a fallback*, not a spec. `[Open]` derive ours from traces (Q-3). Mark every fallback-derived state with `provenance: "derived"` so reports can distinguish it.

### 4.3 Contradiction handling

If `event_state` is fresher than `kernel_level` read and they disagree, prefer the fresher one and record `source_conflict` for calibration. If sources are mutually contradictory within the same tick, emit `Unknown` **only if** no source has a fresh kernel-level reading; otherwise prefer the kernel.

### 4.4 Unknown

All of: kernel unavailable, VM stats unavailable, swap unavailable → `Unknown`, with `reasons[]`.

---

## 5. The scalar: `pressure_score`

Some consumers (planner, UI ordering, calibration) want a comparable number. Define it, but never let it be the sole input to a decision.

```
score ∈ [0, 1]

score = clamp(
    0.50 * level_component      # Normal 0.0, Watch 0.33, Prevent 0.66, Critical 1.0, Unknown 0.5
  + 0.20 * free_scarcity        # clamp((F_ref - free_pct) / F_ref)
  + 0.15 * compressor_load      # clamp(comp_pct / C_ref)
  + 0.10 * swap_pressure        # clamp(swap_used / total_ram)  -- swap relative to RAM, not to swap size
  + 0.05 * disk_scarcity        # clamp((D_ref - disk_free_pct) / D_ref), 0 if swap_rate == 0
  , 0, 1
)
```

Weights are deliberately dominated by the level (0.50) because the level encodes the OS's judgment. `F_ref`, `C_ref`, `D_ref` are tune points (ADR-004).

`Unknown` ⇒ score is still computed for ordering but carries `reliable: false`, and no action may be planned from an unreliable score.

**Anti-pattern guard:** the score is *not* displayed as "memory health %" to the user. It is an internal ordering key. Presenting it would invite the "85% means bad" misreading we are trying to eliminate.

---

## 6. Hysteresis and state transitions

Pressure state feeds user-visible advisories and (later) actions. Flapping is unacceptable — a state that oscillates every sample produces alert fatigue and, worse, action churn.

Rules `[Adopt: sbh's hysteresis_pct + behavior_hysteresis_secs]`:

1. **Asymmetric thresholds.** Escalation uses the raw threshold; de-escalation requires falling below `threshold − hysteresis` (default 5 percentage points of `free_pct`, or one full level for kernel-driven state).
2. **Dwell time.** A new state must persist for `min_dwell` (default 15 s) before it is published, except escalation to `Critical`, which publishes immediately.
3. **No recovery on degraded evidence.** (SI-4) De-escalation requires `ok` quality on the inputs that caused escalation.
4. **Monotone escalation within an incident.** While in an incident (state ≥ Prevent), record the peak and do not report a "recovered" event until a complete healthy sample.

State machine:

```
Normal ──(Watch cond)──► Watch ──(Prevent cond)──► Prevent ──(Critical cond)──► Critical
  ▲                        │                          │                        │
  └──(healthy × dwell)─────┴──(healthy × dwell)──────┴──(healthy × dwell)──────┘
```

Transitions recorded in the ledger as `pressure_transition{from,to,reason,inputs,score}`.

---

## 7. What this estimator deliberately does not do

| Not done | Why |
|---|---|
| Predict | that is `forecasting.md`; keeping them separate makes both testable |
| Attribute | that is `process-attribution.md` |
| Decide actions | that is `policy/` |
| Smooth the kernel level | the OS's judgment is not ours to filter |
| Use total RSS | a sum of overlapping/shared pages is meaningless (macos-integration §4) |
| Treat swap size as capacity | swap is disk, not memory (sbh's own warning) |

---

## 8. Test plan (see `../05-testing/`)

| Test | What it proves |
|---|---|
| T-PE-1 | Kernel level 1/2/4 maps to Normal/Prevent/Critical |
| T-PE-2 | Kernel unavailable ⇒ fallback used and `provenance=derived` |
| T-PE-3 | All sources unavailable ⇒ `Unknown`, never `Normal` |
| T-PE-4 | Trend can raise Normal→Watch but never lower Prevent→Normal |
| T-PE-5 | Hysteresis prevents flapping on a saw-tooth trace (≤ 1 transition per 60 s) |
| T-PE-6 | Degraded input during Prevent does not permit recovery |
| T-PE-7 | Score is monotone in each input, all else equal |
| T-PE-8 | Replayed real traces reproduce the same state sequence deterministically |

## 9. Open decisions

- **Q-1:** Are `Watch` thresholds better expressed in `free_pct` rate (points/min) or in `swap_rate`? Needs traces.
- **Q-2:** Does `compressor_pct` alone ever justify `Watch`? (Compression can be healthy — macOS does it proactively.) Leaning: no, only in combination with a falling free trend.
- **Q-3:** Derivation of F/C/D reference values on the target machine.
