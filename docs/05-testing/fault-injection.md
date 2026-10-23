# MP-TEST-005 — Fault Injection

**Status:** Draft
**Version:** 0.1.0

Every fault in `../04-safety/failure-recovery.md` §1 must have an injection point and an assertion. Injection is compile-time gated (`//go:build faultinject`) plus a runtime guard that refuses to enable injections in a release binary unless `MP_DEV_ALLOW_FAULT=1` **and** the binary is not code-signed for distribution.

---

## Injection mechanism

The platform layer and store are behind interfaces (`Source`, `Ledger`, `Clock`, `IPC`). Fault injection is a middleware implementation of each interface, configured by a scenario file:

```jsonc
// testdata/fault/scenarios/source-loss.json
{
  "name": "source-loss",
  "steps": [
    {"at_s": 0,  "source": "vm_stats", "mode": "ok"},
    {"at_s": 60, "source": "vm_stats", "mode": "error", "error": "EPERM"},
    {"at_s": 180,"source": "vm_stats", "mode": "ok"}
  ]
}
```

Injection modes per source: `ok`, `error`, `slow(ms)`, `partial`, `garbage` (structurally valid but implausible values), `vanish`.

---

## Scenario catalogue

| ID | Scenario | Assertions |
|---|---|---|
| FI-1 | Each source individually: error / slow / garbage | metric marked `unavailable`/`degraded`; no default substitution; state never improves on missing data (SI-4); one rate-limited diagnostic |
| FI-2 | Worker panic (attribution, collector, ledger) | supervisor records fault; no restart storm; capability steps down; ledger shows `worker_fault` |
| FI-3 | Unclean restart | `unclean_start` recorded; `observation_gap` written for the downtime; estimator starts in warmup |
| FI-4 | Disk full during write | fallback to stderr (capped); mode → observe; no buffer growth; recovery when space returns |
| FI-5 | Clock jump (forward 2 h, backward 10 min, sleep 8 h) | rates unaffected (monotonic); backward jump → estimator reset; sleep gap → warmup, no spike |
| FI-6 | Socket bind failure | daemon continues recording; retries with backoff; `doctor` explains |
| FI-7 | Invalid config (unknown key, bad pattern, bad value) | daemon refuses to start with exit 3 and names the field; no partial start |
| FI-8 | Ledger schema major mismatch | exit 12; nothing overwritten |
| FI-9 | Restart storm (5 restarts in 5 min) | `restart_storm` event; starts in Minimal |
| FI-10 | Own RSS over hard limit | exit 10; clean shutdown record; no action in final cycle |
| FI-11 | Recovery oscillation | fault clears; recovery only after quiet window; no flapping (≤1 level change/min) |
| FI-12 | Dispatch subscription dies silently | `dispatch_source_idle` after threshold; polling remains authoritative; no false `Normal` |
| FI-13 | Slow provider action | timeout → `failed`; breaker counts; no partial state claimed |
| FI-14 | Corrupt ledger tail | reader drops the truncated record, warns, continues |
| FI-15 | Process disappears mid-attribution | no negative delta; series marked ended; totals consistent |

---

## Determinism requirements

- All scenarios run against an injected clock; no `time.Sleep` in tests except where the test explicitly exercises wall-clock behaviour (and then with generous tolerances).
- Scenario files are the source of truth; the test harness has no hard-coded fault logic.
- Each scenario asserts on a **golden event sequence** (event type + ordering), not on free-form text. Text output is separately snapshot-tested.

---

## CI placement

Fault injection runs on the macOS runner for FI-1, FI-3, FI-7, FI-10, FI-12; the rest can run anywhere (pure logic + fakes). All are required for a release (`test-strategy.md` §3).
