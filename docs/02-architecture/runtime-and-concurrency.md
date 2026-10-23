# MP-ARCH-003 — Runtime & Concurrency

**Status:** Draft
**Version:** 0.1.0

---

## 1. Rules (non-negotiable)

1. **Every goroutine has an owner and an exit condition.** No `go func()` without a `ctx` and a bounded lifetime.
2. **No unbounded channels.** Every channel has a capacity chosen from a documented rationale.
3. **No unbounded queues.** A full queue means *drop and count*, not *grow*.
4. **The tick is not a queue.** If a tick overruns its deadline, it is skipped and recorded. Queuing stale ticks produces wrong rates and unbounded memory.
5. **No `time.Sleep` in production code paths** except inside the scheduler, which uses a timer + select on `ctx.Done()` and the pressure-event channel.
6. **No locks held across I/O.** State is owned by the tick goroutine; other goroutines communicate by channel.
7. **Panics are contained at goroutine boundaries** and converted to faults (SI-7).

---

## 2. Goroutine inventory

| Goroutine | Count | Owner | Exit |
|---|---|---|---|
| supervisor (main loop) | 1 | `main` | ctx cancel / signal |
| system sampler | 1 | supervisor | ctx cancel |
| process sampler | 1 | supervisor | ctx cancel |
| pressure event pump | 1 | supervisor | ctx cancel / subscription close |
| analysis worker | 1 | supervisor | ctx cancel |
| ledger writer | 1 | supervisor | ctx cancel after flush |
| self-monitor | 1 | supervisor | ctx cancel |
| IPC accept loop | 1 | supervisor | ctx cancel / listener close |
| IPC connections | 0..8 | accept loop | per-connection ctx, 5 s idle timeout |
| pressure adapter (cgo/bridge) | 1 | supervisor | cancel of dispatch source |

Total steady state: 8 goroutines. Budget 25 (headroom for IPC). Hard alarm at 100 (`../05-testing/performance-budget.md`).

---

## 3. Channels

| Channel | Capacity | Producer → Consumer | Full policy |
|---|---|---|---|
| `systemSamples` | 4 | sampler → supervisor | replace-oldest (a stale system sample is useless) |
| `processSnapshots` | 2 | sampler → supervisor | drop-newest, count |
| `pressureEvents` | 16 | pump → supervisor | coalesce to highest level, keep newest timestamp |
| `ledgerRecords` | 1024 | all → writer | drop-oldest, count `ledger_drops`; **events are never dropped** — if the queue is full, the writer is broken and we demote |
| `ipcRequests` | 16 | connections → handler | reject with `busy` |
| `faults` | 16 | any worker → supervisor | drop-oldest, count |

**Rationale for `systemSamples` replace-oldest:** a consumer that falls behind wants the *latest* state, not history. This is the same reasoning sbh applies to its monitor loop.

**Rationale for never dropping events:** losing an event means losing the audit trail — worse than losing a sample.

---

## 4. Tick protocol

```
for {
  select {
  case <-ctx.Done():            return
  case ev := <-pressureEvents:  sampleNow("event", ev)
  case <-systemTimer.C:        sampleNow("schedule", nil)
  case <-processTimer.C:       sampleProcesses()
  }
}
```

`sampleNow`:

```
deadline := now + tickBudget
sys := source.SampleSystem(ctxWithDeadline(deadline))
state := estimator.Update(sys, pendingEvents)
fc := forecaster.Update(sys)
if processSnapshotAvailable && windowElapsed:
    attr, ok := attributor.Window()
    if ok { cls := classifier.Classify(attr); ... }
decision := policy.Evaluate(input)
if decision.HasActions && executorReady {
    for _, a := range planner.Plan(decision.Candidates, state) {
        res := executor.Execute(ctx, a)     // synchronous, serialized
        verification.Schedule(res, window)
        ledger.Record(res)
    }
}
ledger.Record(sample, state, fc)
reschedule timers per current state (ADR-004)
```

**Deadline discipline:** `tickBudget = 0.6 × min(cadence)`; overrun ⇒ record `tick_overrun`, skip analysis (not sampling) for that tick.

---

## 5. Shutdown

```
SIGTERM/SIGINT →
  cancel root ctx
  → samplers exit
  → analysis drains current tick
  → ledger writer flushes + fsyncs + writes clean_shutdown
  → IPC listener closes
  → cgo dispatch source cancelled, adapter joins
  → exit 0 (within 5 s; else exit 1 and record)
```

A second SIGTERM forces immediate exit; the ledger tolerates it (crash-only design).

---

## 6. Go-specific memory discipline

- `debug.SetMemoryLimit(250 MB)`; `GOGC` default. Re-check with measurement.
- Reuse buffers for JSON encoding (`sync.Pool` with a documented max retained size).
- Avoid `map[string]any` on hot paths; use typed structs.
- Cap all caches (`group history`, `identity table`) with explicit eviction.
- `runtime.ReadMemStats` every 30 s in the self-monitor; do not call it per tick.
- **cgo handle discipline:** if the dispatch bridge allocates C memory or stores Go pointers in C, there must be an explicit free path and a test that runs the subscribe/cancel cycle 1000 times and asserts no growth.

---

## 7. Testing concurrency

- `go test -race` on everything; the race detector is a release gate.
- A deterministic scheduler for unit tests: the supervisor accepts an injected clock and channel set, so scenarios can be stepped manually.
- Deadlock detection: every wait has a timeout in tests (5 s), so a deadlock fails fast instead of hanging CI.
- A "goroutine census" test: start/stop the daemon 100 times, assert the goroutine count returns to baseline each time.
