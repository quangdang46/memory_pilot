# MP-SAFE-005 — Failure Containment & Recovery

**Status:** Draft
**Version:** 0.1.0

The governing rule: **when in doubt, get quieter and smaller, and stop acting.** SI-7.

---

## 1. Fault taxonomy

| Fault class | Example | Containment | Recovery |
|---|---|---|---|
| Source fault | `vm_stat` read fails | metric `unavailable`; other sources continue | retry with backoff; clear on success |
| Worker fault | panic in attribution goroutine | worker supervisor catches, records, does not restart in a tight loop | restart with exponential backoff (1s, 5s, 30s, 5m, cap 15m); 3 faults in 10 min ⇒ disable that worker for the session, demote to observe |
| Pipeline fault | tick overruns budget | skip tick, record `gap` | next tick; if consecutive skips > 5, reduce cadence and record degradation |
| Storage fault | ledger write fails / disk full | switch to stderr fallback (capped), demote to observe | recover on successful write; never grow buffers |
| IPC fault | socket bind fails | daemon runs without IPC, records it; CLI reports unreachable | retry bind every 30 s; `doctor` explains |
| Internal invariant fault | nil map, schema mismatch on read | exit with a documented code; launchd restarts | on start, detect unclean shutdown, re-validate state |
| External fault | host OOM event | record; the product's job is the record | post-incident `explain` |
| Config fault | invalid TOML / protection pattern | **refuse to start** (fail closed) | user fixes; `doctor` names the field |
| Version fault | schema major mismatch | refuse to write; observe-only until migration | run `mempilot migrate` |

---

## 2. Degradation ladder

Every step is recorded as an event with the reason. Steps are taken in order and are **not** skipped.

```
0  Normal                 all features, normal cadence
1  ReducedDetail          process sampling every 2nd tick; top-N smaller
2  ReducedCadence         base interval ×2; skip non-essential series
3  Minimal                system VM stats + kernel pressure + ledger only
4  Silent                 ledger only (no IPC writes beyond status)
5  Exit                   documented exit code; launchd restarts after ThrottleInterval
```

Entry conditions:

- steps 1–2: host pressure ≥ Prevent **or** own RSS > warn threshold **or** consecutive overruns;
- step 3: host pressure = Critical **or** own RSS > hard threshold;
- step 4: ledger failing repeatedly;
- step 5: own RSS above hard limit for ≥ 3 consecutive checks, or an unrecoverable internal fault.

**Daemon self-exit codes** (namespace 2 of 2 — the CLI's own exit codes are a *separate* namespace, defined in `../02-architecture/cli-surface.md` §5.1; the two are not interchangeable):

| Code | Meaning |
|---|---|
| 0 | clean shutdown requested by user |
| 3 | config invalid |
| 4 | another instance holds the lock |
| 10 | own memory budget exceeded (self-protective exit) |
| 11 | unrecoverable internal fault |
| 12 | ledger schema incompatible |

---

## 3. Crash-only design

The daemon holds no state that cannot be rebuilt. On start:

```
1. acquire lock (or exit 4)
2. read config; validate; on error exit 3 (fail closed)
3. open ledger; validate schema major; on mismatch exit 12
4. detect unclean previous shutdown (last record not a clean-shutdown marker)
     -> record `unclean_start` event with the previous run id
5. rebuild ephemeral state: last sample, EWMA seeds = empty (warmup),
   rate history = read back the last N aggregated points if available
     -> if history exists, seed the estimator but mark confidence reduced until
        it has been updated min_samples times in this run
6. bind socket; register pressure subscription; start workers
7. write `daemon_start` with version, config hash, gap since last record
```

**Explicit gap reporting:** if the previous record is older than `2 × cadence`, write `observation_gap{from,to,reason:"daemon_not_running"}`. Reports over that interval must show the gap. Never interpolate across it.

---

## 4. Ledger crash safety

- One JSON object per line, newline-terminated, writes via a single writer goroutine.
- `fsync` cadence: every `N` records or `T` seconds, whichever first (start: 64 records / 5 s).
- On read, a truncated final line is discarded with a warning; the rest is valid.
- Rotation: current segment `ledger-<seq>.jsonl`; rotate at size cap; write a `segment_end` record first; never delete the newest segment.
- Retention enforced by a separate slow path (hourly), never in the write path.

`[Adopt from sbh: dual logger degrades gracefully if one backend fails; here we deliberately use only JSONL in v1 to keep one writer path (ADR-003).]`

---

## 5. Restart storms

launchd `ThrottleInterval = 60` bounds restarts. In addition:

- On start, read the last N `daemon_start` records; if ≥ 3 within 10 min, enter step 3 (`Minimal`) immediately and record `restart_storm`.
- Persisted action counters survive restart (they live in the ledger, not memory), so a crash loop cannot reset the action budget.

---

## 6. Recovery validation

After any fault, before leaving a degraded step, the daemon must pass a **health check**:

1. one complete sample with all required metrics `ok`;
2. one successful ledger write after the fault;
3. no new fault for `recovery_quiet` (start: 60 s).

Only then does it step back down. This prevents oscillation between degraded and normal.

---

## 7. Tests (see `../05-testing/fault-injection.md`)

FI-1 source failure per metric; FI-2 worker panic; FI-3 unclean restart; FI-4 disk full; FI-5 clock jump; FI-6 socket bind failure; FI-7 config invalid; FI-8 schema mismatch; FI-9 restart storm; FI-10 budget breach exit; FI-11 recovery oscillation guard.
