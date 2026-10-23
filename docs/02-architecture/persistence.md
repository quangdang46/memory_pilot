# MP-ARCH-005 — Storage & Persistence

**Status:** Draft
**Version:** 0.1.0
**Related:** ADR-003 (JSONL), ADR-009 (horizons), SI-3 (containment)

---

## 1. Layout

```
~/Library/Application Support/mempilot/          # MP_DATA_ROOT
├── config.toml                # user config (or ~/.config/mempilot/config.toml)
├── state.json                 # heartbeat / last-known state (atomic replace)
├── control.sock               # IPC (mode 0600)
├── daemon.lock                # single-instance lock (PID + start time + boot id)
├── ledger/
│   ├── ledger-000001.jsonl
│   ├── ledger-000002.jsonl
│   └── ...
├── episodes/
│   └── episodes.jsonl         # compact episode memory (append-only)
└── diag/
    ├── startup.log            # pre-logger crash capture
    └── incident-<id>.json     # on-demand evidence bundles
```

Nothing is written outside `MP_DATA_ROOT` except the launchd stdout/stderr logs in `~/Library/Logs/mempilot/` (chosen by launchd, not by us) and the LaunchAgent plist.

---

## 2. Record types

All ledger records: `{"v":1,"type":...,"ts":...,"mono":...,...}`.

| Type | Frequency | Content |
|---|---|---|
| `daemon_start` / `clean_shutdown` / `unclean_start` | per lifecycle | version, config hash, gap |
| `sample_sys` | 10 s (hot) | pressure-relevant system metrics, quality flags |
| `sample_proc` | 30 s (hot, top-K) | group-level deltas + top contributors |
| `pressure_transition` | on change | from/to, reason, inputs, score, provenance |
| `forecast` | every N samples | rate, ETA, interval, confidence terms, calibration state |
| `attribution` | per window | groups, coverage, confidence terms |
| `classification` | per window | bucket, evidence, alternatives |
| `decision` | per evaluation with an outcome | mode, candidates, chosen, reason, refusals |
| `action` | per execution | full record (`../04-safety/action-authorization.md` §7) |
| `verification` | after window | verdict + measured effect |
| `advisory` | rare | what the user was told |
| `fault` | rare | class, worker, containment step |
| `degradation` | on change | axis, from, to, reason |
| `observation_gap` | on restart/skip | from, to, reason |
| `incident` | on critical transition | window bounds, preserved flag |
| `segment_end` | per rotation | record count, time bounds |

---

## 3. Write path

- Single writer goroutine; batches up to 64 records or 250 ms.
- `fsync` every 64 records or 5 s.
- Rotation at 8 MiB; `segment_end` written before reopening.
- On write error: retry twice, then mark degraded, switch to capped stderr, demote mode, and attempt to reopen on the next successful tick.
- Never allocate unbounded buffers during failure: if the queue is full, drop samples (counted), never events.

---

## 4. Read path

- Startup: read the last segment's tail (last 1 KiB) to find the last record; validate; detect unclean shutdown.
- Index: for each segment, read first and last line to build `(seg, first_ts, last_ts)`; in memory only.
- Queries scan the relevant segments; with a 72 h hot tier the worst case is a few MB.

---

## 5. Retention

Hourly sweep:

```
hot (ledger-*.jsonl): keep segments covering the last 72 h, min 4 segments
warm (derived): 30 d of 1-min aggregates in dedicated segments
cold: 1 y of 1-h aggregates + episodes + calibration
```

Deletion only of segments matching `ledger-\d{6}.jsonl` (and the warm/cold equivalents) **inside** the ledger directory, older than the horizon. A file that does not match the pattern is never deleted (ST-13).

---

## 6. Config

TOML, strict parsing:
- unknown keys → error (with the key path);
- values validated with ranges and cross-field checks;
- `mempilot config validate` runs the same code as startup;
- config hash recorded in every `daemon_start` so history can be reinterpreted across config changes.

Sensitive? No secrets are stored. No credentials, no tokens. If a future provider needs a secret, it does not go in this file.

---

## 7. State file (heartbeat)

Written every 5 s, atomically (temp + rename):

```json
{"v":1,"pid":1234,"started_at":"...","uptime_s":3600,
 "mode":"observe","degradation":0,"pressure":{"state":"watch","score":0.41,"provenance":"kernel"},
 "forecast":{"eta_s":812,"confidence":0.78,"trustworthy":true},
 "self":{"rss_bytes":41200000,"cpu_pct":0.2,"goroutines":9},
 "last_sample_mono_ns":..., "last_sample_wall":"...",
 "ledger":{"segments":3,"bytes":8123456,"drops":0}}
```

`doctor`/`status` treat it as stale after 30 s (6× cadence).

---

## 8. Migration

- `v` is the record schema version; a major mismatch ⇒ exit 12 with instructions (never rewrite silently).
- `mempilot migrate` performs explicit, backed-up, forward-only migration: copy segments to `ledger.bak-<version>/`, rewrite, verify counts, then swap. No in-place destructive rewrite without the backup.

---

## 9. Crash-safety summary

| Failure | Outcome |
|---|---|
| kill -9 mid-write | last line truncated; reader drops it; `unclean_start` recorded |
| disk full | degrade, capped stderr, no buffer growth |
| partial rename (state.json) | previous file remains valid (atomic replace) |
| lock file stale | holder PID/start-time checked; stale lock reclaimed with a recorded event |
| power loss | same as kill -9; fsync cadence bounds data loss to ≤ 5 s |
