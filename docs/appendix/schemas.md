# Schemas (normative)

**Status:** Draft
**Version:** 1 (draft)

These are the shapes SI-5 is enforced with. If a field here is marked **required**, the product has **no code path** that can emit the object without it. The prose docs describe intent; this file is the contract.

Conventions:
- All versioned objects carry `"v": 1`.
- Times: `ts` is RFC3339 UTC (display/correlation); `mono` is monotonic nanoseconds (math). Rates use `mono` only.
- Bytes are base-2 integers. Rates are `float64` bytes/second.
- `null` is used only where the schema says `| null`; "unknown" is never encoded as `0` or `""`.

---

## 1. `Metric`

```jsonc
{
  "value": 1234.0,
  "unit": "bytes",              // bytes | pages | percent | bytes_per_sec | ns | count
  "source": "host_statistics64",// api | command | derived
  "quality": "ok",              // ok | degraded | unavailable
  "reason": null,               // required (non-null) when quality != ok
  "mono": 123456789012,         // monotonic ns
  "ts": "2026-10-23T10:30:00Z"
}
```

**Required:** value, unit, source, quality, mono, ts.
**Invariant:** `quality != "ok"` ⇒ `reason` is a non-empty string.

---

## 2. `SystemSample`

```jsonc
{
  "v": 1,
  "ts": "...", "mono": 123,
  "ram_total": {Metric},
  "page_size": {Metric},
  "pages": {"free": {Metric}, "active": {Metric}, "inactive": {Metric},
            "wired": {Metric}, "speculative": {Metric},
            "compressor": {Metric}, "throttled": {Metric}},
  "free_pct": {Metric},
  "compressor_pct": {Metric},
  "compressed_bytes": {Metric},
  "swap": {"total": {Metric}, "used": {Metric}, "free": {Metric}, "rate": {Metric}},
  "kernel_pressure_raw": {Metric},
  "kernel_pressure": {Metric},      // value ∈ "normal"|"warn"|"critical"
  "disk": {"free": {Metric}, "total": {Metric}},
  "event_pressure": {Metric}        // value ∈ "normal"|"warn"|"critical"|"none"
}
```

---

## 3. `PressureState`

```jsonc
{
  "v": 1,
  "state": "watch",                 // normal | watch | prevent | critical | unknown
  "score": 0.41,                    // [0,1], internal ordering key
  "reliable": true,                 // false when Unknown or inputs degraded
  "provenance": "kernel",           // kernel | derived | mixed | none
  "inputs": { /* per-input state + quality */ },
  "reasons": ["free_pct falling 0.8pp/min"],
  "hysteresis": {"changed_at": "...", "previous": "normal", "dwell_s": 22}
}
```

**Required:** state, score, reliable, provenance, inputs.
**Invariant:** `state == "unknown"` ⇒ `reliable == false` and `reasons` non-empty.
**Invariant:** no `state` ⇒ `normal` transition may occur when a `degraded`/`unavailable` input was among those that raised the state (SI-4).

---

## 4. `Forecast`

```jsonc
{
  "v": 1,
  "series": "headroom_bytes",          // headroom_bytes | swap_used_bytes | compressor_bytes
  "horizon_s": 900,
  "rate_bps": 1.23e6,
  "acceleration_bps2": -4.1e3,
  "trend": "rising",                   // rising | flat | falling
  "burst_probability": 0.12,

  "eta_point_s": 812,                  // display only; null when infinite
  "eta_lower_bound_s": 410,            // policy only; null when no calibrated bound

  "confidence": 0.78,
  "confidence_terms": {"sample": 1.0, "residual": 0.71, "stability": 0.83, "coverage": 0.9},
  "sample_count": 34,
  "fallback_active": false,

  "calibration": {
    "state": "calibrated",             // warming | calibrated | degraded
    "coverage_empirical": 0.91,        // null while warming
    "coverage_target": 0.90,
    "resolved_count": 120,
    "pending_count": 2,
    "alpha": 0.11
  },

  "actionable": true                   // the single gate policy consults
}
```

**Required:** series, horizon_s, rate_bps, trend, confidence, confidence_terms, sample_count, fallback_active, calibration{state,coverage_target,resolved_count,pending_count,alpha}, actionable.
**Nullable:** `eta_point_s`, `eta_lower_bound_s`, `calibration.coverage_empirical`.

**Hard invariants:**
1. `actionable == true` ⇒ `eta_lower_bound_s != null` **and** `fallback_active == false` **and** `calibration.state == "calibrated"`. Enforced both by JSON Schema `if/then` and by the constructor (belt and braces — the constructor also covers Go values that never round-trip through JSON).
2. `calibration.state == "warming"` ⇒ `actionable == false`, even though `eta_lower_bound_s` may be populated with the point estimate for display continuity. **A warming bound is not a bound.**
3. There is no schema-valid forecast without `confidence`, `confidence_terms`, and `calibration` (SI-5).
4. `eta_point_s` and `eta_lower_bound_s` are **separate fields, deliberately**. A single `eta` field would invite substituting one for the other, which is the failure this contract exists to prevent.

---

## 5. `Attribution`

```jsonc
{
  "v": 1,
  "window": {"start": "...", "end": "...", "duration_s": 1200},
  "grouping_rule": "nearest_anchor:v1",
  "coverage": {
    "processes_seen": 512, "processes_denied": 37,
    "processes_estimated_total": 600,
    "coverage_ratio_system": 1.31,
    "note": "process sums include shared pages; may exceed system delta"
  },
  "system_delta_bytes": 2400000000,
  "compressor_delta_bytes": 410000000,
  "groups": [{
    "id": "anchor:iTerm2:3",
    "label": "iTerm2 (session 3)",
    "growth_bytes": 1450000000,
    "growth_rate_bps": 1208333,
    "accelerating": true,
    "process_count": 14,
    "top_contributor": {"identity": "(5012,1729...)", "name": "node", "growth_bytes": 980000000},
    "targetable": true,
    "confidence": 0.82,
    "confidence_terms": {"denied": 0.94, "coverage_deviation": 0.92, "grouping_instability": 1.0},
    "evidence": ["14 processes in group grew ≥ 10 MB", "..."],
    "alternative_explanations": ["shared page accounting inflation"]
  }],
  "large_but_stable": [{"label": "VS Code", "resident_bytes": 2100000000, "delta_bytes": 12000000}]
}
```

**Required:** window, grouping_rule, coverage, system_delta_bytes, groups[] (each with id, growth_bytes, confidence, confidence_terms, evidence, alternative_explanations).
**Invariant:** `coverage.note` is always present (it is a constant sentence, not optional prose).

---

## 6. `Classification`

```jsonc
{
  "v": 1,
  "group_id": "anchor:iTerm2:3",
  "classification": "leak_suspicion",   // workload_growth | cache_or_retrievable |
                                        // subprocess_accumulation | leak_suspicion | unknown
  "confidence": 0.64,
  "basis": ["grew 1.8 GB over 47 min with no plateau", "..."],
  "alternative_explanations": ["internal cache with an undocumented ceiling", "..."],
  "what_would_change_our_mind": ["a plateau of ≥ 15 min", "..."],
  "recommended_next_step": "run Instruments (Leaks/Allocations) against PID 5012 if you want certainty"
}
```

**Required:** classification, confidence, basis, alternative_explanations, what_would_change_our_mind.
**Invariant:** `alternative_explanations` must have ≥ 1 element for `leak_suspicion`; `what_would_change_our_mind` must have ≥ 1 element for any classification other than `unknown`.
**Invariant:** no field may contain the assertion form "is/are leaking" (renderer + schema-level string check, ST-17).

---

## 7. `Decision`

```jsonc
{
  "v": 1,
  "id": "dec-20261023T103000-7f3a",
  "mode": "observe",
  "input_refs": {"state_id": "...", "forecast_id": "...", "attribution_id": "..."},
  "candidates": [{"action": "RequestCooperativeRelease", "provider_id": "...",
                  "expected_effect_bytes": [1e8, 4e8], "cost": 0.3, "risk": 0.1}],
  "chosen": null,
  "no_action_reason": "no_safe_relief",   // present when chosen is null
  "refused": [{"action": "...", "reason": "mode_demoted"}],
  "required_mode": "canary",
  "created_at": "..."
}
```

**Required:** id, mode, candidates, created_at, and exactly one of `chosen` / `no_action_reason`.
**Invariant:** a decision with `required_mode` above the current mode must materialise as `no_action_reason: "mode_insufficient"` — never as an execution.

---

## 8. `ActionResult`

```jsonc
{
  "v": 1,
  "action_id": "...",
  "decision_id": "...",
  "class": "RequestCooperativeRelease",
  "provider_id": "...",
  "mode": "canary",
  "preconditions": [{"name": "no_protected_group_affected", "ok": true, "observed": "..."}],
  "executed_at": "...",
  "duration_ms": 140,
  "outcome": "succeeded",       // succeeded | refused | failed | timed_out
  "refusal_reason": null,
  "expected_effect_bytes": [1e8, 4e8],
  "verification": {
    "window_s": 120,
    "pressure_before": {PressureState},
    "pressure_after": {PressureState},
    "observed_effect_bytes": 2.2e8,
    "verdict": "effective"      // effective | partial | ineffective | unverified
  }
}
```

**Required:** action_id, decision_id, class, preconditions, outcome, verification.
**Invariant:** `verification.verdict == "unverified"` is the default and is required when no complete measurement exists (P5). `outcome: "succeeded"` alone never implies success.

---

## 9. `StateFile` (heartbeat, read by `status`/`doctor`)

```jsonc
{
  "v": 1,
  "pid": 1234,
  "started_at": "...",
  "uptime_s": 3600,
  "mode": "observe",
  "degradation": 0,
  "pressure": {"state": "watch", "score": 0.41, "provenance": "kernel", "reliable": true},
  "forecast": {"eta_s": 812, "confidence": 0.78, "trustworthy": true},
  "self": {"rss_bytes": 41200000, "cpu_pct": 0.2, "goroutines": 9},
  "last_sample_mono_ns": 123, "last_sample_wall": "...",
  "ledger": {"segments": 3, "bytes": 8123456, "drops": 0},
  "config_hash": "sha256:..."
}
```

---

## 10. Schema enforcement points

| Schema | Validated at emit | Validated at read (ledger) |
|---|---|---|
| Metric | ✅ collector | ✅ ledger reader |
| SystemSample | ✅ | ✅ |
| PressureState | ✅ | ✅ |
| Forecast | ✅ (constructor returns error) | ✅ |
| Attribution | ✅ | ✅ |
| Classification | ✅ | ✅ |
| Decision | ✅ | ✅ |
| ActionResult | ✅ | ✅ |
| StateFile | ✅ | ✅ |

Reading an unknown **major** version ⇒ exit 12. Reading an unknown **minor** field is tolerated and preserved (forward compatibility).

---

## 11. Not yet specified

- `Incident` record (needs SCN-004 evidence shape finalised).
- `Advisory` body (depends on the renderer work).
- `Episode` record (leak-suspicion repeat memory).

These are deliberately left until the corresponding algorithms are implemented; specifying them now would produce plausible-looking schemas that the implementation would silently diverge from.
