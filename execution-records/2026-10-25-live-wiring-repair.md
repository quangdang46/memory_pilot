# Execution record — 2026-10-25: live-wiring repair

**Trigger.** Owner report: "nhiều đợt memory leak nhưng mp không có tác dụng"
(repeated leak waves, Memory Pilot had no effect).

**Verdict on the report:** correct, and the code confirmed it. The daemon had
been running for 12 h writing 17 406 system samples and 8 413 decisions while
performing **zero actions** and producing **zero attributions**. It also
reported a pressure state that was false.

Every defect below was reproduced on the target machine (Mac mini M4,
darwin 25.4.0) before being fixed, and every fix carries a test that fails
without it.

---

## 1. Why it had no effect

### 1.1 The action layer was not constructed

`cmd/mempilotd/main.go` built the policy engine and then discarded it:

```go
eng := policy.NewEngine(policy.Mode(cfg.Mode))
_ = eng // policy wired via runPipeline decision records; executor lands with providers (Phase 4)
```

`executor`, `providers`, and `attribution` were referenced only from
`_test.go` files. The integration tests that "passed" exercised a graph the
daemon never built. Consequences: no action could be planned, and no
`action_result` record has ever existed in a real ledger.

### 1.2 Attribution was never fed

The process collector returned full per-process data; the daemon wrote only
the count:

```go
lg.Append(map[string]any{"kind": "processes", "coverage": snap.Coverage, "n": len(snap.Processes)})
```

The ledger contained **zero** records with a per-process resident size, so
"which workload grew?" was unanswerable after the fact. In its place the
daemon emitted, every 5 s during pressure:

> `pressure elevated (Critical); grouped attribution pending Phase 3 wiring`

### 1.3 `mp run` swallowed the command

```go
$ ./bin/mempilot run echo SHOULD_NOT_PRINT
[ok] headroom sufficient
```

`run()` parsed flags, called `status`, printed a verdict, and exited 0. No
`exec`, no spawn, no registration. The managed-launch point — the whole premise
of ADR-014 — did not exist, so the owned-workload registry could never be
populated and L2 had no legitimate target.

### 1.4 Design boundary (unchanged, and correctly so)

For a process the daemon did not spawn, the product only reports. That is the
intended product, not a defect. The defect was that the reporting half did not
exist either.

---

## 2. Defects that made it report falsehoods

### 2.1 Pressure latched at Critical forever

`SampleSystem` never set `MonotonicMs`; it was 0 on every sample. The estimator
computes its 15 s dwell from that field, so `dwelled` was always false and only
the `immediate` escalation path (Critical) could ever fire. Published state
could rise and never fall.

Reproduced: 1 Critical sample followed by 50 Normal samples → still `Critical`.
Live evidence: kernel level returned to 1 at 23:40 and `memory_pressure`
reported 39 % free, while `mempilot status` still said `Critical (0.34, kernel)`
— for 45 minutes.

Fixed by populating `mach_continuous_time` (survives sleep, so a suspend does
not read as a rate spike) and by making the dwell gate degrade honestly when no
monotonic clock is available instead of silently passing everything.

### 2.2 Forecast claimed actionable with no evidence

Same root cause: `dtS` was always 0, so rate was 0, TTE was 0, and
`Actionable` was true — while `Calibrated` was false and the lower bound was 0.
`mempilot forecast` returned `{tte_s:0, actionable:true, calibrated:false}`,
which violates SI-5.

Also fixed an independent contradiction: `tte = -1` ("no crossing within the
horizon") reported `actionable: true`. No crossing means nothing to act on.

### 2.3 Coverage looked permanently broken

226 of 614 processes were counted as `Denied`. They are root and other users'
processes, which a user-scope daemon was never entitled to read. A number that
is always bad is a number the user learns to ignore. Coverage now separates
`Foreign` (out of scope by design) from `Denied` (real visibility loss); on a
live sample: `Succeeded 416, Foreign 236, Denied 0`.

### 2.4 `observed_mb` was measured per tick, not per window

`ObservedGrowth` was replaced with the newest tick's delta, then compared
against 300 s of accumulated process growth. The ratio was meaningless and
usually read 0. Now measured window-start → window-end (not a sum of per-tick
drops, which would count a sawtooth as growth).

### 2.5 Grouping merged unrelated sessions

`AnchorTable` contained `node`, `python3`, `zsh`. With grouping by name, every
such process became its own anchor, so ten `node` processes from five sessions
merged into one blob, and group labels read `zsh` — true but useless. Anchors
are now terminals/editors/agents only, and only **ancestors** can be an anchor
(§4.3: name matching finds anchors, it never merges leaves).

### 2.6 Quiet time was multiplied by process count

Per-process quiet seconds were summed into the group. A 61-process group
accumulated 11 814 s of "quiet" after ~45 minutes of wall-clock observation,
which turned every large group into a `leak_suspicion` at confidence 0.65.
Fixed by folding activity once per group per tick. Regression test added.

Additionally, a group with no observed activity computed quiet time from the
zero `time.Time`, producing 9 223 372 036 s (~292 years). It now measures from
first sighting.

### 2.7 A 5-minute window could never satisfy a 20-minute rule

`classify` requires ≥ 1200 s of sustained growth; the report window was 300 s.
Every verdict was therefore `unknown / too short` by construction. Attribution
now keeps a long-horizon record across window boundaries, which is what the
"too short" branch was asking for.

### 2.8 Any same-user process could hijack stop authorization

`admit` accepted `{pid: 1, allow_stop: true}` from any local process and
registered PID 1 as an owned, stop-authorized workload. Verified live over the
socket. Fixed: the caller's peer pid must be the registered pid's actual parent,
and the path fails closed when the peer is unknown. The refusal is recorded as
`admission_refused`. Verified live after the fix:

```
PID1 -> refused: caller is not the parent of that pid
self -> refused: caller is not the parent of that pid
```

Legitimate launches still work (`stop_armed: true`, `peer_verified: true`).

### 2.9 `doctor` panicked instead of diagnosing

```go
if st, err := os.Stat(...); err != nil { probs = append(probs, "...: "+st.Name()) }
```

`st` is nil on error. The one command that explains a broken install crashed
when the ledger was missing.

### 2.10 `report` could never find a window

It returned the newest 50 matching records. At a 5 s system cadence that is
under two minutes — less than one window. It now scans by age (45 min), bounded
by a record cap.

---

## 3. What the product does now

Live on the target machine, after the repair:

```
$ mempilot status
mempilotd pid 76642 | uptime 18m6s | mode canary | pressure Normal (0.30, kernel) | self rss 12.4MB

top movers (last window)
  +  2020 MB  wezterm-gui            17 procs  top: rust-analyzer
  +   481 MB  2.1.310                 2 procs
  +   238 MB  node                    4 procs
  +   216 MB  Google Chrome          14 procs  top: Google Chrome H
```

```
$ mempilot report
window 300s | accounted 4430 MB | system headroom fell 739 MB | compressor 3166 MB
  ...
coverage: 416 seen, 236 outside this user (unreadable by design), 0 denied
247 groups grew by a total of 4430 MB; system headroom fell 739 MB. Shared pages
are counted per-process, so process sums can exceed the system delta. Compressed
pages (3166 MB in compressor) belong to no process.
```

Per-tick pipeline (system cadence): estimator → forecast → window close →
attribution → classification → policy → planner → executor → verification
schedule → ledger. Every step writes a record.

`mp run` spawns the workload, registers it with the daemon, propagates its exit
code, and clears the registry entry on exit.

---

## 4. Tests added (all fail on the pre-fix code)

| Test | Guards |
|---|---|
| `TestRecoveryWithoutMonotonicClock` | the Critical latch |
| `TestDwellStillGatesWithClock` | dwell still works when a clock exists |
| `TestLiveSystemSampleHasMonotonicClock` | collector populates the clock (live host) |
| `TestLiveProcessIdentities` | name, ppid, start time, RSS per process (live host) |
| `TestGroupingByAncestryNotName` | two sessions, not one blob |
| `TestGroupingWalksGrandchildren` | anchor reached through the parent chain |
| `TestResetKeepsSeriesBaseline` | window boundary does not erase growth |
| `TestObservedGrowthAcrossWindow` | window-scoped system delta, sawtooth-safe |
| `TestHistorySurvivesWindowReset` | evidence outlives the report window |
| `TestQuietTimeNotMultipliedByProcessCount` | the 61× inflation |
| `TestWindowRecordsAttributionAndClassification` | a window produces a named group |
| `TestSelfReliefExecutedUnderCritical` | actions carry real effect evidence |
| `TestForeignProcessNeverStopTarget` | ST-22 still holds after wiring |
| `TestAdmitRefusesNonParent` | forged ownership is refused and recorded |
| `TestAdmitRefusesWithoutPeerPID` | fails closed without a peer credential |
| `TestObserveModeNeverArmsStop` | observe never arms (ADR-014 §2.7) |
| `TestDoctorReportsMissingLedgerWithoutPanicking` | doctor diagnoses |

`make ci` green: vet, tests, ST-1..ST-4 gates, build.

---

## 5. Honest limits that remain

- **Foreign processes are never acted on.** The daemon reports them; it cannot
  stop them. This is the product, not a gap to close.
- **T1 self-relief frees little** on a machine where the daemon holds ~12 MB.
  The measurable value is the report, not the relief.
- **L2 (managed stop) is wired but unproven live.** Registration, parentage
  verification, identity recording, and arming are exercised end-to-end. An
  actual stop under Critical pressure needs a canary run with a real
  `--allow-stop` workload; it remains calendar-bound, as ADR-014 requires.
- **Self-trim has no buffer pool to release yet.** The aggregation caches it
  would trim are small; it reports `changed:false` honestly rather than
  claiming a release it did not perform.

---

## 6. Addendum — defects found while verifying on-device (same session)

Verification after the first pass found four more defects, each now fixed and
test-guarded:

| Defect | Symptom | Fix |
|---|---|---|
| L2 was unreachable | `pipeline.Decide` did `continue` on `stop_owned` with a comment claiming a later tick would escalate; no later tick ever did | L2 gets its own pass after self-relief; guarded by `TestL2ReachesOwnedAuthorizedTarget` |
| `stopOwnedChild` signalled a group that did not exist | on-device test: `signal failed: no such process` — `mp run` spawns normally, so the child inherits the wrapper's pgid | resolve the target at stop time; group signal only when the recorded pgid is led by that pid; `TestL2StopsRealOwnedProcessOnDevice` asserts the process is gone from the process table |
| LaunchAgent dropped the config | `service install` produced a daemon in built-in `observe` mode — it ran and reported but could never act | plist carries `-config`; install writes a canary config if absent |
| `report` could never find a window | returned the newest 50 records (~2 min at a 5 s cadence) | scan by age with a record cap |

### On-device proof for L2

`internal/pipeline/l2_device_test.go` spawns a real process, registers it as an
owned workload with prior authorization, drives Critical pressure plus a
leak-shaped verdict through the pipeline, and asserts the process is gone from
the process table. The negative twin asserts an un-authorized owned process is
refused and survives. Both pass; `make gates` still reports exactly one signal
call site (ST-1).

### Live classification, verified end to end

A synthetic workload (`mp run --allow-stop -- python3 grow.py`) grew 20 MB →
306 MB over 12 minutes. The daemon, with no knowledge that the workload was
synthetic, reported:

| Elapsed evidence | Verdict |
|---|---|
| 570 s | `unknown` — "quiet period 570s < 600s, window 570s < 1200s — too short" |
| 915 s | `unknown` — "window 915s < 1200s — too short" |
| 1214 s | `leak_suspicion` (0.65) — "sustained 1214s ≥ 1200s with no plateau; activity flat for 1214s ≥ 600s (uncorrelated)" |

In the same windows, actively-working groups classified as `workload_growth`
at 0.87–0.90 confidence ("growth co-moves with activity"). The two behaviours
that were both broken before — false calm and false alarm — are now distinct
and evidence-backed.
