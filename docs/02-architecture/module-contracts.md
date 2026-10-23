# MP-ARCH-002 — Module Contracts

**Status:** Draft
**Version:** 0.1.0

Contracts are expressed as Go interfaces because that is the shape the implementation must honor, and because an interface is the only contract a compiler can check. Types are shown without full field lists; the canonical definitions belong in code and must be kept in sync with this doc (spec-code parity gate).

---

## 1. Kernel packages (importable by all layers)

### `internal/clock`

```go
type Clock interface {
    Now() time.Time            // wall, for display/correlation
    Monotonic() time.Duration  // for all rate math
}
```

Fake clock in tests. **Rule:** no package may call `time.Now()` directly outside this package (lint rule).

### `internal/model`

Value types shared across layers: `Sample`, `SampleQuality`, `PressureState`, `Forecast`, `Attribution`, `Classification`, `Decision`, `ActionResult`, `Event`. No behavior, no I/O.

### `internal/store`

```go
type Store interface {
    // Paths are resolved against the data root; escapes rejected (SI-3).
    WriteAtomic(name string, data []byte) error
    Read(name string) ([]byte, error)
    DataRoot() string
}
```

---

## 2. Platform layer

### `platform`

```go
type Source interface {
    Name() string
    SampleSystem(ctx context.Context) (SystemSample, error)
    // Returns partial results and per-field quality; never silently defaults.
}

type ProcessSource interface {
    Processes(ctx context.Context) (ProcessSnapshot, error)
}

type PressureEvents interface {
    Subscribe(ctx context.Context) (<-chan PressureEvent, SubscriptionHandle, error)
}

type Platform interface {
    Source
    ProcessSource
    PressureEvents
    Info() PlatformInfo   // page size, total RAM, macOS version
}
```

**Contract requirements:**

- Every method honors `ctx` cancellation and returns within a caller-provided deadline.
- Errors are typed (`ErrUnsupported`, `ErrDenied`, `ErrUnavailable`) so the caller can distinguish "not permitted" from "broken".
- `ProcessSnapshot` includes `Attempted`, `Succeeded`, `Denied` counts — coverage is part of the return value, not an afterthought (MP-REQ-032).
- No method mutates system state.

`platform/macos` implements this with build tag `darwin`; `platform/other` returns `ErrUnsupported` so the binary builds elsewhere and fails loudly at runtime.

---

## 3. Telemetry layer

### `collector`

```go
type Collector interface {
    // Runs the two loops; emits to the sink. Owns cadence and backoff.
    Run(ctx context.Context, sink Sink) error
}

type Sink interface {
    System(Sample)              // non-blocking; drops on backpressure with a counter
    Processes(ProcessSnapshot)
    Fault(Fault)
}
```

- The collector owns the two-cadence schedule (ADR-004) and the per-source failure backoff.
- It never interprets; it only collects, stamps quality, and forwards.
- Drops are counted and surfaced (`sampling_drops`), never silent.

### `selfmonitor`

```go
type Monitor interface {
    Start(ctx context.Context)
    Budget() Budget              // read by every worker before doing work
    Healthy() bool               // false until the first successful full cycle; false after a fault
    Snapshot() SelfSnapshot      // RSS, CPU, goroutines, ledger size
}
```

**Contract:** `Budget` shrinks monotonically with pressure escalation (SI-6); `Healthy()` is the executor's gate (SI-7).

---

## 4. Intelligence layer

### `pressure`

```go
type Estimator interface {
    Update(s model.SystemSample, evts []model.PressureEvent) model.PressureState
}
```

Pure: no I/O, no clock reads (timestamps come in the sample).

### `forecast`

```go
type Forecaster interface {
    Update(s model.Sample) model.Forecast
    Trustworthy() bool           // false when fallback_active or calibration degraded
}
```

`Forecast` includes rate, acceleration, ETA (+interval), confidence + terms, burst state, calibration state. `Trustworthy()` is what policy consults — a forecaster that is warming up must not be able to influence actions.

### `attribution`

```go
type Attributor interface {
    // ProcessSnapshot stream in, Attribution out at window boundaries.
    Observe(s model.ProcessSnapshot, sys model.SystemSample)
    Window() (model.Attribution, bool)   // ok=false until a full window has elapsed
}
```

### `leaksuspect`

```go
type Classifier interface {
    Classify(attr model.Attribution, hist Episodes) model.Classification
}
```

Returns a bucket + evidence + confidence + alternatives; schema forbids a bare claim (SI-5).

---

## 5. Policy & action layer

### `policy`

```go
type Engine interface {
    Evaluate(in model.EvaluationInput) model.Decision
}
```

`EvaluationInput` = pressure state + forecast + attribution + classification + current mode + counters. `Decision` = either `NoAction{reason}` or `PlannedActions{[]Candidate, required_mode}`.

**Contract:** `policy` never performs I/O. It may read only its injected config.

### `planner`

```go
type Planner interface {
    Plan(candidates []model.Candidate, state model.PressureState) []model.ScheduledAction
}
```

Orders by expected benefit per unit of cost and risk; respects rate limits and breakers (as inputs, not as side effects).

### `actionguard` / `executor`

```go
type Executor interface {
    Execute(ctx context.Context, a model.ScheduledAction) model.ActionResult
}
```

- Re-validates everything (`../04-safety/action-authorization.md` §4).
- Serialized: one action at a time.
- Never returns `succeeded`; returns `executed` + a separate verification verdict.

### `providers`

```go
type Provider interface {
    Spec() ProviderSpec
    Check(ctx context.Context) error           // preconditions
    Apply(ctx context.Context, budget int64) (Applied, error)
    Measure(ctx context.Context) (EffectMeasurement, error)
}
```

### `verification`

```go
type Verifier interface {
    Schedule(a model.ActionResult, window time.Duration)
    Result(actionID string) (model.Verdict, bool)
}
```

---

## 6. Interface layer

### `ipc`

```go
type Server interface{ Serve(ctx context.Context, h Handler) error }
type Handler interface{ Handle(ctx context.Context, req Request) (Response, error) }
```

Closed op set; versioned envelope (ADR-007).

### `reporting`

```go
type Renderer[T any] interface {
    Render(v T) (string, error)   // human
    JSON(v T) ([]byte, error)
}
```

Renderers **cannot be constructed** from values lacking confidence/evidence (SI-5).

---

## 7. Wiring rules

1. `cmd/mempilotd` constructs the graph; no package self-registers.
2. Construction validates config; failure ⇒ exit 3 before any goroutine starts.
3. Every long-lived goroutine receives a `context.Context` from the supervisor and a `Budget`.
4. Shutdown order: stop intake → stop collector → flush ledger → close IPC → exit.

---

## 8. Contract tests

Each interface has a shared **conformance test suite** that every implementation (real, fake, fault-injected) must pass:

- `platformentest`: deadline honored, errors typed, no mutation, quality flags consistent.
- `ledgertest`: crash-tail tolerance, rotation, retention, path containment.
- `providertest`: preconditions honored, verification produces a verdict, no side effect on `Check`.
- `ipctest`: version rejection, rate limit, peer-UID check.

The fake implementations used in unit tests are the same ones the fault injector wraps — so a fake that drifts from the contract breaks tests, not production.
