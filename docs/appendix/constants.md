# Constants & Defaults

**Status:** provisional — every value here is a starting point, not a measurement.
**Rule:** once code exists, this table must be **generated from the code** and a test must fail when it is stale (the pattern sbh uses for its "Runtime Constants" table). Until then, this is the single source of truth and other docs cite it rather than restating numbers.

Column **Tags** uses the convention from `../README.md`: `P` = provisional, `V` = verified against a reference implementation, `O` = open (must be measured before use).

---

## 1. Pressure model (`03-algorithms/pressure-estimator.md`)

| Constant | Value | Tag | Notes |
|---|---|---|---|
| `kernel_level` decode | 1→Normal, 2→Prevent, 4→Critical | V (sbh) | all other values ⇒ unavailable |
| `WATCH_TREND_THRESHOLD` | 0.35 (score contribution) | P | re-derive from traces |
| fallback `F_C` (free % critical) | 2.0 | P | sbh disk-tool value; **do not inherit silently** |
| fallback `F_P` (free % prevent) | 5.0 | P | same |
| fallback `F_W` (free % watch) | 12.0 | P | new — sbh has no Watch tier |
| fallback `C_P` (compressor % prevent) | 10.0 | P | |
| fallback `C_W` (compressor % watch) | 6.0 | P | |
| score weights | level 0.50, free 0.20, compressor 0.15, swap 0.10, disk 0.05 | P | |
| `F_ref` (free % reference) | 20 | P | |
| `C_ref` (compressor % reference) | 20 | P | |
| `D_ref` (disk free % reference) | 15 | P | |
| hysteresis | 5 percentage points free %, or 1 level for kernel state | P | |
| `min_dwell` | 15 s (escalation to Critical: 0) | P | |

---

## 2. Forecasting (`03-algorithms/forecasting.md`)

| Constant | Value | Tag | Notes |
|---|---|---|---|
| `base_alpha` | 0.30 | V (sbh) | |
| `min_alpha` | 0.10 | V (sbh) | |
| `max_alpha` | 0.75 | V (sbh) | |
| `min_samples` | 5 | V (sbh) | below ⇒ `fallback_active` |
| rate history | 200 samples / **30 min (time-based)** | V / P | sbh uses count; we adapt to time |
| `BURST_CALIBRATION_MIN` | 30 samples | V (sbh) | |
| burst median floor | 1.0 byte/s | V (sbh) | below ⇒ skip burst counting |
| MAD consistency factor | 1.4826 | V | |
| robust upper bound | median + 3 × MAD | V | |
| confidence weights | sample 0.50, residual 0.20, stability 0.30 | V (sbh) | + coverage term added by us |
| jitter normalizer floor | 60 s | V (sbh) | |
| `coverage_target` | 0.90 | V (sbh) | conformal target coverage |
| `coverage_tolerance` | 0.05 | V (sbh) | |
| conformal window | 500 resolved outcomes | V (sbh) | |
| conformal warmup | 30 resolved outcomes | V (sbh) | below this, bound = point estimate |
| ACI step `gamma` | 0.01 | V (sbh) | adaptive coverage increment |
| conformal `alpha` clamp | [−0.5, 1.0] | V (sbh) | |
| forecast horizon cap | 7200 s | V (sbh) | enroll() rejects longer horizons |
| discard-pending recovery | 1 % of critical distance | V (sbh) | recovery invalidates pending forecasts |
| `min_observations` (guardrail) | 60 | V (sbh) | |
| guardrail window | 500 | V (sbh) | |
| material-rate horizon | 86400 s | V (sbh) | noise floor = max(headroom/86400, 10 B/s) |
| scorecard window | 200 outcomes | V (sbh) | |
| FAR ceiling before confidence rises | 0.30 | V (sbh) | |
| `dynamic_min_confidence` uplift | up to +0.20, cap 0.95 | V (sbh) | |
| `max_rate_error` | 0.30 | V (sbh) | |
| e-process penalty | 1.5 | V (sbh) | |
| e-process reward | 2/3 | V (sbh) | |
| e-process alarm threshold | 20.0 | V (sbh) | |
| e-process log clamp | [−5.0, +3.5] | V (sbh) | responsiveness bounds, see forecasting §4.2 |
| `recovery_clean_windows` | 3 | V (sbh) | |
| noise floor rate | 10 bytes/s | V (sbh, disk) | **re-derive for RAM** |
| `fallback_active` threshold | `confidence < 0.2` or `samples < min_samples` | V (sbh) | |

---

## 3. Sampling (`adr/004-sampling-schedule.md`)

| State | System loop | Process loop | Footprint loop | Tag |
|---|---|---|---|---|
| Normal | 5 s | 30 s | every 4th proc sample | P |
| Watch | 3 s | 20 s | every 3rd | P |
| Prevent | 2 s | 15 s | every 2nd | P |
| Critical | 1 s | 10 s | every sample | P |
| Unknown | 3 s | 20 s | every 3rd | P |

| Other | Value | Tag |
|---|---|---|
| system loop floor | 1 s | P |
| process loop floor | 10 s | P |
| pressure-event wake slice | 500 ms | V (sbh) |
| source failure backoff | ×2 per failure, cap 5 min | P |
| denied-PID retry interval | 60 s | P |
| tick budget | 0.6 × cadence | P |

---

## 4. Attribution & classification (`03-algorithms/process-attribution.md`, `../03-algorithms/leak-suspicion.md`)

| Constant | Value | Tag |
|---|---|---|
| attribution window | 15 min (default), configurable | P |
| top-N groups in report | 5 | P |
| top-N contributors per group | 3 | P |
| `quiet_window` | 10 min | P |
| `min_suspect_window` | 20 min | P |
| `plateau_window` | 15 min | P |
| `min_growth_for_suspicion` | 200 MB | P |
| `corr_threshold` (Spearman) | 0.5 | P |
| `denied_fraction_max` | 0.2 | P |
| confidence base | 0.95 | P |
| "large but stable" floor | ≥ 500 MB and \|delta\| < 20 MB over the window | P |

---

## 5. Policy & actions (`04-safety/action-authorization.md`)

| Constant | Value | Tag |
|---|---|---|
| default mode | `observe` | P |
| per-provider min interval | 15 min | P |
| per-provider hourly cap | 2 | P |
| global hourly action cap | 6 | P |
| ineffectiveness backoff | ×2, cap 24 h | P |
| breaker: consecutive failures | 2 | P |
| breaker cooldown | 24 h | P |
| decision `max_age` before execute | 30 s | P |
| verification window (default) | 120 s | P |
| action intent fsync | before every perform | P |
| unresolved intents at startup | counted as `unknown_crash_window`, charge budget | P |
| canary promotion evidence | ≥ 20 invocations, no adverse events, verified effect | P |

---

## 6. Resources (`adr/008-resource-budget.md`, `05-testing/performance-budget.md`)

| Constant | Value | Tag |
|---|---|---|
| idle RSS target | 35 MB | P |
| idle RSS budget | 60 MB | P |
| loaded RSS budget | 120 MB | P |
| RSS warn | 120 MB | P |
| RSS hard exit | 400 MB (3 consecutive checks) | P |
| Go memory limit | 250 MB | P |
| idle CPU | 0.3 % | P |
| loaded CPU | 2 % | P |
| ledger write rate | ≤ 1 KB/s idle, 4 KB/s loaded | P |
| ledger/day | ≤ 15 MB | P |
| ledger total | ≤ 500 MB | P |
| FD count | ≤ 20 idle / 32 loaded / 64 hard | P |
| goroutines | ≤ 25 idle / 40 loaded / 100 hard | P |
| startup → first sample | ≤ 2 s | P |

---

## 7. Persistence (`02-architecture/persistence.md`, `adr/003`, `adr/009`)

| Constant | Value | Tag |
|---|---|---|
| segment rotation | 8 MiB | P |
| fsync cadence | 64 records or 5 s | P |
| hot tier | 5–10 s resolution, 72 h | P |
| warm tier | 1 min, 30 d | P |
| cold tier | 1 h, 1 y | P |
| state.json write | 5 s | P |
| state.json stale | 30 s | P |
| ledger schema version | `1` | P |
| incident snapshot window | ±15 min | P |

---

## 8. IPC (`adr/007`)

| Constant | Value | Tag |
|---|---|---|
| socket path | `<data>/control.sock`, mode 0600 | P |
| max connections | 8 | V (sbh) |
| request rate limit | 10 req/s | V (sbh) |
| max line | 64 KiB | V (sbh) |
| IO timeout | 5 s | V (sbh) |
| protocol version | 1 | P |

---

## 9. Daemon lifecycle (`04-safety/failure-recovery.md`)

| Constant | Value | Tag |
|---|---|---|
| worker restart backoff | 1s, 5s, 30s, 5m, cap 15m | P |
| worker disable threshold | 3 faults / 10 min | P |
| restart storm threshold | 3 starts / 10 min | V (sbh, 3/300 s) |
| recovery quiet | 60 s | P |
| heartbeat | 5 s | P |
| stall threshold | 30 s (6 × heartbeat) | P |
| launchd `ThrottleInterval` | 60 s | V (sbh) |
| launchd `KeepAlive` | `{SuccessfulExit: false, Crashed: true}` | V (sbh) |
| launchd `Nice` | 5 | V (sbh) |
| launchd `LowPriorityIO` | true | V (sbh) |

**Daemon self-exit codes** (canonical: `../04-safety/failure-recovery.md` §2): `0` clean, `3` config, `4` instance, `10` budget, `11` internal, `12` schema.

**CLI process exit codes** (canonical: `../02-architecture/cli-surface.md` §5.1): `0` ok, `1` pressure-warning, `2` usage, `3` config, `4` daemon-unreachable, `5` internal.

The two namespaces overlap numerically at `3` and `4` by coincidence. Never conflate them.

---

## 10. CLI (`02-architecture/cli-surface.md`)

| Exit code | Meaning |
|---|---|
| 0 | ok |
| 1 | pressure ≥ Prevent (script-visible warning) |
| 2 | usage |
| 3 | config |
| 4 | daemon unreachable |
| 5 | internal |

Reserved: 10–12 (daemon self-exit).

---

## Maintenance

When a constant changes:
1. change it here,
2. change every doc that cites it by ID (grep the constant name),
3. once code exists, the generated-table test will fail until this file matches the code — that is the intended direction of enforcement.

---

## 10. Process relief engine (`06-problem-first/process-relief-engine.md`, SCN-010)

All provisional. The engine ships detect+recommend in v1; L2/L3 need false-positive data.

| Constant | Value | Tag |
|---|---|---|
| graceful-quit wait | 10 s | P |
| candidate kill budget per class | 3 / hour | P |
| expected-relief cap per incident | 2 × remaining gap | P |
| respawn-loop stop | 2 recreations in 10 min | P |
| orphan evidence review window | 10 min quiet | P |
| re-verify gap (identity) | two reads ≥ 60 s apart | P |
| `no_action_reason` for blocked L2 | `policy_prohibits` | P |
