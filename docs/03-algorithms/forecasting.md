# MP-ALG-003 — Forecasting & Calibration

**Status:** Draft
**Version:** 0.1.0
**Reference:** sbh `src/monitor/ewma.rs`, `src/monitor/predictive.rs`, `src/monitor/guardrails.rs`, `src/monitor/conformal.rs`

The forecasting layer answers exactly three questions and refuses to answer a fourth:

1. Is pressure rising, falling, or flat — and how fast?
2. If it continues, when does it become dangerous?
3. How much should we trust that answer?
4. *(refused)* Which process is causing it — that is attribution, a separate question with separate evidence.

---

## 1. What we estimate

**Primary series:** bytes of "headroom consumed". We do **not** forecast "RAM used", because used RAM is not a monotone pressure proxy. Instead, define headroom as:

```
headroom_bytes = free_bytes + reclaimable_bytes_estimate
```

where, to start, `reclaimable_bytes_estimate = 0` and `headroom_bytes = free_bytes * page_size`. This is deliberately crude and honest: we forecast the exhaustion of *free pages* plus, later, a vetted reclaimable estimate (ADR-004). We never invent reclaimable memory to make the forecast look calmer.

**Secondary series** (forecast independently, correlated in policy): `swap_used_bytes`, `compressor_pages`.

Why forecast free pages and swap separately: they have different dynamics and different costs. Swap rising fast is a *latency* problem even before free RAM is gone.

---

## 2. The estimator (adopted from sbh, adapted)

sbh's EWMA estimator is well-tested and the math is not disk-specific. The core is:

### 2.1 Adaptive alpha with burst damping

```
inst_rate   = (headroom_prev - headroom_now) / dt          # bytes/s, positive = consuming
burstiness  = |inst_rate - ewma_rate| / (|ewma_rate| + 1)
damping     = 1 / (2 * burstiness + 1)
alpha       = clamp(base_alpha * damping, min_alpha, max_alpha)
```

With `base_alpha = 0.30`, `min_alpha = 0.10`, `max_alpha = 0.75` (sbh defaults). The effect: during a spike, alpha drops toward `min_alpha` so the EWMA does not chase the spike; in steady state it tracks promptly.

**Adaptation for us:** `headroom` in bytes, ticks in seconds. Same constants as the starting point; re-tune on traces (ADR-005).

### 2.2 Acceleration from the smoothed rate, not raw samples

```
smoothed_accel = (ewma_rate_now - ewma_rate_prev) / dt
ewma_accel     = ewma(alpha, ewma_accel, smoothed_accel)
```

sbh's comment explains why: raw-rate differencing amplifies noise when the sampling interval varies — and ours *will* vary (adaptive cadence, MP-REQ-010). Adopt without modification.

### 2.3 Time-to-threshold with the quadratic model

Given a rate `v`, acceleration `a`, and distance `d` to the threshold:

```
t = d / v                                  if |a| is negligible
t = (-v + sqrt(v² + 2ad)) / a              otherwise, for a > 0
t = 2d / (v + sqrt(v² + 2ad))              for a < 0   (numerically stable conjugate form)
```

Guard rails sbh applies, all of which we adopt:

- `d <= 0` ⇒ `t = 0` (already there).
- `v <= 0` ⇒ `t = ∞` (not consuming).
- discriminant `< 0` ⇒ `∞` (deceleration is strong enough that the trend reverses before reaching the threshold).
- for `a < 0`, if `t > -v/a` (the time the rate would hit zero), return `∞` — the projection is unphysical.

**Why the conjugate form matters:** with a small negative acceleration, `sqrt(v²+2ad) ≈ v`, so `(-v + root)/a` loses precision catastrophically. sbh's comment at `ewma.rs:462-466` documents it. This is the kind of detail that separates a correct implementation from a plausible one.

For Memory Pilot, the threshold `d` is the distance from current free bytes to the free-bytes level at which we consider `Critical` — which is itself a derived quantity from the pressure estimator's reference values. Keep the two coupled through a single constant module.

### 2.4 Confidence

sbh composes confidence from three terms (`ewma.rs:295-310`):

```
sample_term    = min(samples / min_samples, 1)
residual_term  = 1 / (1 + residual_ewma / (|rate| + 1))
stability_term = 1 / (1 + 3 * prediction_jitter_ewma)

confidence = clamp(0.3*stability + 0.2*residual + 0.5*sample, 0, 1)
```

`prediction_jitter_ewma` tracks how much the *forecast itself* moved between ticks, normalized by magnitude with a 60 s floor:

```
jitter = |t_now - t_prev| / max(|t_now|, |t_prev|, 60)
```

**This is the single most valuable idea to adopt verbatim.** A forecast that swings 47 min → 2 min → 40 min has low confidence even when each individual estimate "looks" fine — and that is exactly the pathology that makes forecasting tools untrustworthy.

**Adapt:** we add a fourth term:

```
coverage_term = f(empirical coverage of the conformal lower bound over the last window)
```

and re-weight so that confidence drops when calibration degrades (see §4).

### 2.5 Fallback behaviour

Before `min_samples` (start with 5, re-tune), the estimator returns `fallback_active = true` and a rate built from whatever exists. Memory Pilot's rule: `fallback_active` ⇒ **the forecast is not actionable**; it may be displayed with an explicit "warming up" marker but policy must not use it. sbh gates actions on `min_confidence`; we gate on confidence *and* `!fallback_active`.

---

## 3. Burst vs. trend separation

A single step change (an app allocating 2 GB at once) and a slow leak have very different meaning and very different responses. sbh computes a robust burst statistic over a 200-sample rate history:

```
median_rate  = median(|r|)
mad_rate     = 1.4826 * median(|r_i - median_rate|)
upper_bound  = median_rate + 3 * mad_rate
is_outlier   = |r| > upper_bound
burst_prob   = (deviation_factor * w / (deviation_factor + 1))   where
                deviation_factor = max(|r|/median - 1, 0)
                w = max(min(deviation_factor/10, 1), min(burst_duration/5, 1))
```

Guards: requires 30 samples to calibrate; skips when `median_rate < 1` (idle noise); resets duration on any non-burst sample.

**Adaptation:** our tick rate is slower and more variable than sbh's disk loop, so "200 samples" may span hours. Use a **time-based** history cap (e.g. last 30 min) rather than a count. `[Open]` ADR-005.

**Use in policy:**
- A burst ⇒ short-lived, may self-resolve; respond with `Watch` + evidence collection, not escalation.
- A sustained ramp ⇒ leak-suspicion territory; escalate and attribute.
- These two verdicts must never be conflated in the report ("memory jumped" vs "memory keeps growing" are different sentences).

---

## 4. Calibration: making confidence honest

A confidence number that is never checked is decoration. Two mechanisms, both adopted in spirit from sbh:

### 4.1 Conformal-style bound (verified mechanism, not an invented one)

**`[Verified: sbh]`** — `src/monitor/conformal.rs`. sbh does **not** use a two-sided log-space interval; it computes a **one-sided lower bound**, which is the only side that matters when the cost is "we said it was safe and it wasn't".

```
Config: window 500, warmup 30, gamma 0.01, coverage_target 0.90, horizon_cap 7200 s

nonconformity score (relative overestimate of remaining time):
    s_t = clamp((tte_pred - tte_actual) / tte_pred, 0, 1)

quantile:
    k = ceil((n+1) * (1 - alpha));  q = k-th smallest score

bound:
    tte_lo = clamp(tte_pred * (1 - q), 0, tte_pred)

adaptive coverage (ACI):
    err_t   = 1[tte_actual < lo_at_issue]
    alpha  += gamma * ((1 - coverage_target) - err_t);  clamp [-0.5, 1]
```

Four details that are the actual engineering, all worth adopting verbatim:

1. **`k > n` returns `q = 1.0`** (⇒ `tte_lo = 0`), *not* the largest observed residual. After a run of severe misses the tool claims **zero** guaranteed headroom rather than clamping to "the worst we've seen so far, which is probably fine". This is the fail-safe that keeps the bound honest.
2. **Warmup returns `lo = point`** (< 30 resolved outcomes). Before there is evidence, the bound is not widened arbitrarily — but the forecast is separately marked untrustworthy, so a point estimate is never *used* as a bound.
3. **Censored resolution.** If the forecast's own horizon expires without a crossing, it resolves *censored with score 0*, meaning "the true time is at least the prediction". Without this, long safe periods would be silently dropped and the estimator would only ever learn from incidents.
4. **`discard_pending()` when the system materially recovers** (sbh uses >1% of the red threshold). Otherwise a recovery invalidates a forecast that was never actually tested, and counting it as a miss would poison calibration.

**Enrollment gates** (`conformal.rs:256-274`) reject non-finite, non-positive, > horizon, a full pending window, and non-monotone timestamps. sbh's docs note the trap directly: polling faster than `horizon/window` would replace every forecast before its deadline and calibration would never resolve — **a self-inflicted blindness that looks like "no data yet" forever.**

**Only the bound changes behaviour.** sbh consumes it in exactly one place: `calibrated_estimate` sets `seconds_to_threshold = lo` and scales `seconds_to_exhaustion` by `clamp(lo/point, 0, 1)`, which feeds the feedforward term and the predictive policy. The point estimate is what gets displayed; the bound is what gets acted on.

**Adopt:** one-sided lower bound, `k>n ⇒ 0` fail-safe, censored resolution, discard-on-recovery, and the "only the bound acts" rule. **Do not invent** a symmetric log-space interval — it would overstate certainty in the exact direction that matters.

### 4.2 Guardrail on drift

**`[Verified: sbh]`** — read line-by-line in `src/monitor/guardrails.rs`. sbh maintains an **anytime-valid e-process** over forecast-vs-actual observations:

```
penalty_log = ln(1.5)      # a bad observation
reward_log  = ln(2/3)      # a good observation
threshold   = 20           # e-value that raises the alarm (20:1 evidence)

good  -> e_log += reward_log
bad   -> e_log += penalty_log * scale, where
           severity = clamp((ratio - max_rate_error) / (1 - max_rate_error), 0, 1)
           scale    = 0.3 + 0.7 * severity        # barely-bad ≠ catastrophic-bad
e_log = clamp(e_log, -5.0, +3.5)
alarm = exp(e_log) >= 20
```

with `max_rate_error = 0.3` (relative rate error tolerated) and a `coverage_ok` gate on the conformal lower bound.

Three details that are the actual insight, all worth adopting:

1. **The clamp bounds are chosen for responsiveness, not statistics.** `-5.0` prevents "banking" credit so drift is detectable within ~10–15 bad observations; `+3.5` (not 5.0) lets the alarm clear within ~7 good ones. sbh's comment records that a prior cap of 5.0 caused prolonged false alarms on healthy machines.
2. **Burst outliers count as good.** During a burst the EWMA deliberately damps the spike, so predicted-vs-actual diverges *by design*. Counting that as miscalibration made the guard fail permanently on bursty workloads (600+ consecutive breach windows on rustc-heavy machines). A guard that cannot distinguish "our model damped a spike on purpose" from "our model is wrong" is useless.
3. **Severity-scaled penalty.** A ratio of 0.31 against a 0.30 threshold must not cost the same as a ratio of 1.0.

**Memory Pilot's use:** guardrail alarm ⇒ `forecast_degraded` ⇒ policy raises the confidence required to act, widens advisory language ("estimate unreliable"), and records the degradation. Two independent guards: one over rate/ETA calibration, one over action outcomes (`../04-safety/action-authorization.md` §6 uses the same e-process shape for the breaker, which is a nice symmetry — the same statistical object answers "is our model drifting?" and "is our actuator failing?").

**Adaptation:** replace the byte-rate ratio with our headroom-rate ratio; keep the shape and the responsiveness-clamp reasoning. Note also sbh's **directional** error rule: only *under*-prediction (actual worse than predicted) is penalized; over-prediction is the safe direction and is not counted as a miss (`guardrails.rs:124-147`). Adopting a symmetric error would punish the tool for being cautious, which is backwards.

### 4.2b False-alarm accounting that distinguishes intervention from error

**`[Verified: sbh]`** — `control.rs`/`predictive.rs` `PredictionScorecard` (`:773-867`). Four outcomes per prediction:

| Outcome | Meaning |
|---|---|
| `Realized` | predicted danger occurred, we did not act |
| `Intervened` | predicted danger, we acted, so the prediction was not tested |
| `FalseAlarm` | predicted danger, did not occur |
| `Inactive` | prediction expired unused |

FAR = `FalseAlarm / (Realized + Intervened + FalseAlarm)`. **`Intervened` is not a false alarm** — a tool that acts and thereby prevents the event must not count its own success as a failure. sbh records this because the naive denominator punished exactly the behaviour the product wants.

It also feeds back: when FAR exceeds 30 %, `dynamic_min_confidence` rises by up to +0.20 (capped at 0.95), so a tool that has been crying wolf becomes quieter **automatically** rather than needing the user to reconfigure it.

**Adopt all of it.** This is the mechanism that makes "we got better" measurable instead of asserted, and it is the most likely thing to be forgotten when implementing from scratch.

**Adaptation:** our v1 has almost no interventions, so `Intervened` will be rare — but the accounting must exist from day one, because the moment a provider is added (ADR-006), the FAR definition becomes load-bearing.

### 4.3 Minimum-calibration rule

`MP-REQ-024`: no forecast-driven action while the last N calibration windows have coverage below `target - tolerance`. The action path must be able to ask "is the forecaster currently trustworthy?" and get `false`.

---

## 5. What we refuse to forecast

| Refused | Why |
|---|---|
| "This process will reach X GB at T" | attribution × forecasting produces confident nonsense; we report growth *rate* of a contributor, not a promise |
| Exact time of the macOS dialog | the OS's victim selection and compression behaviour are not predictable at that resolution |
| Whether a process is leaking | see `leak-suspicion.md`; forecasting and leak classification are separate |
| Anything from a `fallback_active` estimator | not enough data |

---

## 6. Output contract

```jsonc
{
  "v": 1,
  "series": "headroom_bytes",            // or swap_used_bytes, compressor_bytes
  "horizon_s": 900,

  "rate_bps": 1.23e6,
  "acceleration_bps2": -4.1e3,
  "trend": "rising",                     // rising | flat | falling
  "burst_probability": 0.12,

  "eta_point_s": 812,                    // point estimate; DISPLAY ONLY. null when Infinity
  "eta_lower_bound_s": 410,              // the only value policy may act on.
                                         //   null when no calibrated bound exists

  "confidence": 0.78,
  "confidence_terms": {"sample":1.0,"residual":0.71,"stability":0.83,"coverage":0.9},
  "sample_count": 34,
  "fallback_active": false,

  "calibration": {
    "state": "calibrated",               // warming | calibrated | degraded
    "coverage_empirical": 0.91,
    "coverage_target": 0.90,
    "resolved_count": 120,
    "pending_count": 2,
    "alpha": 0.11
  },

  "actionable": true                     // policy gate: true iff bound is usable
}
```

### 6.1 Three distinct numbers, never conflated

| Field | Meaning | Who may use it |
|---|---|---|
| `eta_point_s` | best estimate from the rate model | **display only** |
| `eta_lower_bound_s` | conformal one-sided lower bound at target coverage | **policy** — the only input to any decision |
| `confidence` | heuristic index from §2.4 | display, ordering, and as a secondary gate |

The single most important invariant in this contract: **`eta_point_s` must never be substituted for `eta_lower_bound_s` when the bound is absent.** A missing bound means "we cannot guarantee anything", not "use the point estimate".

### 6.2 `actionable` — the single gate policy consults

`actionable` is computed as:

```
actionable =
      !fallback_active
   && calibration.state == "calibrated"
   && eta_lower_bound_s != null
   && confidence >= ACTION_CONFIDENCE_FLOOR
```

**Warmup rule (explicit, because it is easy to get wrong):** during `calibration.state == "warming"` (fewer than 30 resolved outcomes), the reference implementation returns the point estimate as the bound (`lo = point`). We **keep that computation for display continuity but set `actionable = false`**. A bound that has never been tested against reality is not a bound; policy must not consume it. This is recorded as MP-REQ-024 and tested as T-FC-6b.

### 6.3 `calibration.state` transitions

| State | Condition | Effect |
|---|---|---|
| `warming` | `resolved_count < 30` | `actionable = false` |
| `calibrated` | `resolved_count ≥ 30` and coverage within tolerance and no drift alarm | normal operation |
| `degraded` | coverage below `target − tolerance`, or e-process alarm (§4.2) | `actionable = false`; advisories widen to "estimate unreliable"; recorded as `forecast_degraded` |

`degraded` is sticky until the guard's recovery condition (3 consecutive clean windows) is met — it does not clear on a single good sample.

### 6.4 Schema requirements

Required: `series`, `rate_bps`, `confidence`, `confidence_terms`, `sample_count`, `fallback_active`, `calibration{state, resolved_count}`, `actionable`.
Nullable: `eta_point_s`, `eta_lower_bound_s`, `calibration.coverage_empirical` (null while warming).
**Forbidden by construction:** any object with `actionable == true` and `eta_lower_bound_s == null` (schema `if/then` constraint + constructor check).
SI-5 forbids emitting a bare ETA in any form — there is no schema-valid forecast without `confidence`, `confidence_terms`, and `calibration`.

---

## 7. Tests

| Test | Proves |
|---|---|
| T-FC-1 | Pure ramp: `t` recovers the analytic answer within 5% |
| T-FC-2 | Constant rate: `a≈0`, `t = d/v` |
| T-FC-3 | Deceleration strong enough to reverse: `t = ∞` (not a negative or huge finite number) |
| T-FC-4 | Numerical stability: small negative accel, large distance — no catastrophic cancellation (compare float64 to a high-precision reference for a handful of adversarial inputs) |
| T-FC-5 | Jitter term: alternating forecasts reduce confidence monotonically |
| T-FC-6 | `fallback_active` true before `min_samples`, and no action can be planned |
| T-FC-7 | Burst classification on synthetic burst vs ramp |
| T-FC-8 | Conformal coverage measured on replay ≈ target ± tolerance |
| T-FC-9 | Degraded coverage ⇒ `forecast_degraded` and action gate closed |
| T-FC-10 | Clock jump backward does not produce a negative or infinite rate (sbh's `checked_duration_since` guard) |
| T-FC-11 | Sleep gap ⇒ estimator resets to warmup rather than emitting a spike |

---

## 8. Open questions

- **Q-4:** Is headroom = free pages enough, or must it include inactive pages? (Inactive pages are technically reclaimable, but reclaiming them has cost and latency. Including them makes the forecast optimistic.)
- **Q-5:** Separate forecasters per series vs. one multivariate model. Start with separate; keep them independent until proven otherwise.
- **Q-6:** What horizon is useful to the user? 15 min is actionable for a human; 2 min is actionable for the daemon. Emit both with the same math at different thresholds.
