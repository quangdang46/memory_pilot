# ADR-001 — Memory-Pressure Signal: Hybrid (dispatch events + kernel level + polling)

**Status:** Verified (Phase 0 evidence: `spike/mach/NOTES.md` S-1, `spike/pressure/NOTES.md` S-3, 2026-10-23)
**Date:** 2026-10-23
**Related:** MP-REQ-003, MP-REQ-004, MP-REQ-005, `../02-architecture/macos-integration.md`

---

## Context

We need to know, promptly and honestly, when the machine's memory situation changes. Three candidate sources exist, and they answer different questions:

| Source | Answers | Latency | Cost | Availability |
|---|---|---|---|---|
| `sysctl kern.memorystatus_vm_pressure_level` | "What does the kernel currently think?" | none (read is instant) | ~10 µs | Documented indirectly; values verified in a working implementation (sbh) |
| `DISPATCH_SOURCE_TYPE_MEMORYPRESSURE` | "Tell me when it changes" | event-driven | ~0 when idle | Public Apple API; requires dispatch |
| Sampling `vm_stat`/`host_statistics64` | "What are the raw numbers?" | one poll interval | ~10 µs | Always |

sbh uses all three: dispatch events wake the loop, `kern.memorystatus_vm_pressure_level` provides the authoritative level (decoding **XNU dispatch masks 1/2/4**, not a 0-based enum — verified empirically on the target Mac mini M4 (S-3): `kern.memorystatus_vm_pressure_level` returned 2 (WARN) throughout +12 GB induced load while `kern.memorystatus_level` sat in its warning band; any value outside {1,2,4} decodes to UNKNOWN (never calm). Host VM stats come from `host_statistics64(HOST_VM_INFO64=4, rev1 count=38)` — pure-Go via purego, no cgo (S-1)., and `vm_stat` numbers feed the derived fallback when the level is unreadable.

---

## Options

**A. Polling only.** Simplest, pure Go, no cgo. Latency = poll interval; misses the kernel's own transition semantics; the cursor may detect pressure later than the OS does.

**B. Dispatch events only.** Lowest latency, but no data — events carry only a level. Also: what happens if the subscription silently dies? A daemon that believes it is subscribed but is not will never warn.

**C. Hybrid.** Dispatch as a *wake-up*, kernel sysctl as the *authority*, polling as *liveness and fallback*.

**D. Listen to `NSProcessInfo`/Foundation memory warnings.** Requires Objective-C runtime interop, higher cost, and is the app-facing API, not the system-wide one. Rejected.

---

## Decision

**Option C**, with this precise contract:

1. **Liveness loop**: sample `kern.memorystatus_vm_pressure_level` + VM stats at the current cadence regardless of events. If the dispatch source is silent for `max(3 × cadence, 30 s)`, log `dispatch_source_idle` (expected when stable) and treat polling as authoritative.
2. **Event path**: on a dispatch event, immediately re-read the kernel level and VM stats, then run the tick out of band. Events never *carry* state; they only trigger a read. (sbh's `memory_pressure_from_dispatch_event` does exactly this and falls back to an event-only struct if the read fails — we copy the re-read, and we diverge by marking state `degraded` rather than constructing a partial state from a level-only event.)
3. **Subscription failure is non-fatal**: record it, fall back to polling at the same cadence, surface in `doctor`.
4. **Kernel level wins over derived estimates.** Derived estimates are used only when the sysctl read fails, and are tagged `provenance: derived`.

---

## Evidence

`[Verified: sbh]` the three-source approach works on macOS: sbh's implementation is in production, handles subscription failure with a polling fallback thread, caps sleep slices at 500 ms to drain events, and decodes 1/2/4 correctly.

`[Open]` for Memory Pilot:
- **E1:** Is `kern.memorystatus_vm_pressure_level` readable without privileges on the target macOS? (sbh reads it without root.)
- **E2:** Event latency distribution under real pressure on M4.
- **E3:** Go + cgo dispatch: callback → channel latency, and whether the callback thread blocks anything.
- **E4:** What does the level do during `pmset sleep`/wake? Does the subscription survive?

---

## Consequences

**Easy:** prompt reaction; authoritative level; graceful degradation.
**Hard:** two code paths (event vs poll) must produce identical state for the same inputs — this is a test requirement.
**Risk:** a silent subscription failure producing false confidence. Mitigated by (a) explicit idle logging, (b) polling as the always-on path, (c) `doctor` checking subscription health.

## Revisit triggers

- E3 shows cgo dispatch costs more than it saves vs. a 1 s poll.
- The sysctl turns out to be unreliable across macOS versions (would shift authority to `NSProcessInfo` or to a derived model).
