# ADR-000 — Language & Stack: Go

**Status:** Accepted (unverified for the macOS-specific parts)
**Date:** 2026-10-23
**Deciders:** project owner
**Related:** MP-REQ-079, ADR-001

---

## Context

Both reference projects are Rust (nightly, `#![forbid(unsafe_code)]` in sbh's main crate with a single `sbh_mach` FFI exception). The owner's stated priorities are:

1. a **complete** product, not a thin MVP;
2. speed of development and speed of build;
3. macOS first, with portability as a later property;
4. correctness and safety taken seriously.

Rust's advantages here are real: no GC pauses in a daemon that measures memory, strong FFI for Mach/dispatch, and two proven reference implementations. Its costs are also real: slower iteration, longer builds, and a smaller pool of contributors for a hobby-scale project.

Go's advantages: fast builds, simple concurrency for a multi-worker daemon, trivial single-binary distribution, easy CLI/IPC. Its costs: cgo for macOS APIs (expensive call overhead, complicated cross-compilation), and a GC that must be kept inside a footprint budget.

---

## Options

**A. Rust** — mirror sbh's architecture closely; reuse its FFI knowledge (mach2/libc); no GC to reason about.
Cost: build times (LTO, codegen-units=1), ownership friction on a stateful daemon, and the temptation to port sbh's disk abstractions wholesale.

**B. Go** — fast iteration, simple worker model, easy CLI.
Cost: cgo boundary for dispatch; GC tuning; risk of "just call the shell" shortcuts, which we forbid anyway.

**C. Hybrid (Go core + Rust native helper)** — rejected: two toolchains, two test systems, an IPC boundary for the hottest path, for a project with no measured performance need.

---

## Decision

**Go**, with the following constraints:

1. **No cgo unless a required API has no pure-Go path**, and each use is documented in a build note. The expected exceptions: `host_statistics64` (can be done via `syscall.Syscall` without cgo on darwin — Go's `syscall` package exposes `Syscall`/`Syscall6` and Mach traps are reachable; this must be prototyped) and the dispatch memory-pressure source (cgo almost certainly required).
2. `GOMEMLIMIT` set well below the RSS budget; self-measured RSS is the source of truth, not Go's heap stats.
3. **No `os/exec`** outside the vetted adapter (SI-1, MP-REQ-040).
4. Platform dependencies isolated behind build-tagged interfaces.

**Why not "Rust because the references are Rust":** the hardest parts of this project (what to measure, what a forecast means, what may be done) are language-independent. The language choice affects the schedule, not the design. If a component later proves to need tight control (e.g. a high-frequency sampler), it can be a small Rust helper — but that must be justified by measurement, not by analogy.

---

## Evidence

None yet. The macOS syscall reachability from pure Go (`host_statistics64`, `sysctl`, `proc_pidinfo`, `proc_listpids`) must be prototyped before this ADR can become `Verified`. Specifically:

- **P1:** call `host_statistics64` from pure Go via `syscall.Syscall6` with a hand-defined `vm_statistics64` struct and the rev0 count. Confirm it works on the target machine and returns plausible values.
- **P2:** call `proc_listpids` / `proc_pidinfo` from pure Go. These are direct syscalls on darwin and should be reachable without cgo.
- **P3:** attempt `dispatch_source_create` — expect this to require cgo. Measure the cgo call cost and the callback latency.
- **P4:** measure idle RSS of an empty daemon with a ticker + JSONL writer.

If P1–P2 fail, the "no cgo" constraint weakens for the collector only; the rest of the decision stands.

---

## Consequences

**Easy:** CLI, IPC, config, ledger, testing, cross-platform seam, build times.
**Hard:** precise memory control (must be measured, not assumed); dispatch integration (cgo); resisting shell-out shortcuts (structurally forbidden).
**Cannot do:** rely on RAII-style teardown for the FFI structures; must manage cgo handles explicitly (and that is a leak risk the self-monitor must cover).

## Revisit triggers

- P1/P2 prove impossible without cgo **and** cgo overhead measurably violates the sampling budget.
- The daemon demonstrates GC pause effects on sampling cadence (unlikely at our tick rates, but measure).
- A second local sampler requiring sub-millisecond timing appears.
