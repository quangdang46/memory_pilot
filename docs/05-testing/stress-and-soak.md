# MP-TEST-004 — Stress & Soak

**Status:** Draft
**Version:** 0.1.0
**Machine:** Mac mini M4, 16 GB, 256 GB, dedicated (not the owner's daily machine) where possible

---

## 1. Soak (72 h)

Purpose: prove stability and boundedness over the horizon the user actually cares about.

**Workload:** the "realistic" profile, driven by a script:

- 3 long-lived pseudo-agent processes doing periodic CPU + allocation work with stable footprints,
- 1 browser-like process with a moderate, growing cache,
- 1 churning build-like process spawning/killing children,
- normal desktop apps,
- one scripted 20-min pressure event per 24 h.

**Assertions:**

| Property | Method | Threshold |
|---|---|---|
| No RSS trend | OLS slope on hourly means, 95 % CI | CI contains 0 |
| No goroutine leak | count at hour N vs hour 1 | ≤ +2 after warmup |
| No FD leak | `lsof` count | stable ±2 |
| Ledger bounded | segment count, total size | ≤ budget |
| Sampling adherence | recorded cadence vs. schedule | p95 within +20 % |
| No forbidden action | audit log | zero |
| CPU/IO budget | 5-min averages | within `performance-budget.md` |
| Clean restart at hour 48 | kill -9, let launchd restart | recovery within 10 s; gap recorded; no double-write |
| Incident handling | the scripted pressure event | predicted ≥ 3 min ahead; advisory emitted; no action beyond allowlist |

**Deliverable:** `bench/results/soak-<date>.json` + a short narrative of anything anomalous. The narrative matters: a green number with an unexplained spike is not green.

---

## 2. Stress (short, brutal)

| Stress | Setup | Assert |
|---|---|---|
| Massive process count | 2 000 short-lived processes churning | process sampling stays within budget (P-factor adapts); coverage reported; no queue growth |
| Extreme pressure | allocator takes the machine to Critical for 10 min | daemon steps down to Minimal, stays responsive, records everything; its own RSS does not grow |
| Rapid oscillation | pressure toggled every 20 s for 10 min | no action churn (rate limits), no state flapping (hysteresis), advisories coalesced |
| Ledger saturation | 100× normal event rate via test hook | write path drops detail first, never events; disk bounded |
| IPC flood | 1000 req/s from a test client | rate limiter holds; daemon unaffected |
| Config reload storm | reload every second | no leak, no partial state |

---

## 3. Recovery drills

Unlike faults (injected), these are operational:

- unplug network — must be a non-event (no network code);
- sleep/wake ×20 — estimator resets cleanly each time, no spurious advisories;
- `launchctl bootout`/`bootstrap` ×10 — clean start each time, no duplicate processes;
- uninstall/reinstall preserving data — ledger read back, gap recorded.

---

## 4. Reporting

Each stress/soak run produces a one-page report with: machine, macOS build, binary version + commit, workload description, table of assertions, and any deviations with an explanation. Deviations are not deleted from the report; they are explained or filed as issues.

---

## 5. Failure of this suite

If a soak fails, the release is blocked. The fix must be followed by a full re-run, not a partial one — soak failures are almost always slow leaks, and partial runs hide them.
