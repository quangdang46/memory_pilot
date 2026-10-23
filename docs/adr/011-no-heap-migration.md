# ADR-011 — Rejected: transparent heap migration and "SSD as extra RAM"

**Status:** **Rejected (firm)**
**Date:** 2026-10-23
**Related:** `../00-product/goals-and-non-goals.md` NG-4, NG-5

---

## Context

The owner raised an idea worth taking seriously: *"when RAM is about to fill, put that memory away, and release it back when it's needed — like a stack."* This is a genuinely interesting direction, and an honest answer has to distinguish the parts of it that are real from the parts that are not.

The idea decomposes into five mechanisms. Four of them already exist in the kernel and should be used; one does not exist and cannot be built safely in user space.

| Mechanism | Who can do it | Status for us |
|---|---|---|
| Page compression | XNU compressor | Already active; we observe it |
| Paging to swap | XNU VM | Already active; we observe it |
| Reclaim of clean file-backed pages | XNU VM | Already active; we observe headroom |
| App-cooperative release (an app frees its own caches) | the app, via its own API | Possible for specific apps; ADR-011b territory |
| **Transparent relocation of another process's anonymous heap** | nobody, in user space | **impossible to do safely** |

The last one is what "put that memory away and give it back later" would mean for the memory that actually causes pressure (anonymous, dirty, pointer-graph-bound pages).

---

## Why it cannot be done safely

1. **Pointers.** Anonymous memory is reachable from registers, stacks, globals, and untracked data structures inside libraries. Moving a region requires rewriting every reference. Only the owning process's runtime can do that.
2. **Kernel-enforced semantics.** `madvise`, `mlock`, and copy-on-write expectations are per-process. A third party cannot remap another process's anon pages to a file-backed store and preserve semantics.
3. **Atomicity and faulting.** Even if a page could be evicted, the faulting path back in would need to be handled synchronously inside the victim's threads. That is `userfaultfd`'s job on Linux — and even there, only for regions the process itself registered, and it is not available on macOS.
4. **Failure mode.** A partial relocation corrupts the victim. The cost of being wrong is unbounded: data corruption or a crash, in a process whose state is the user's work.

What *is* possible on macOS: the process can be **suspended** (`SIGSTOP`); its pages remain resident. That saves no memory and destroys responsiveness — the worst of both. It is also forbidden here (SI-1).

---

## Decision

**Rejected.** Memory Pilot will not attempt to relocate, evict, or compress another process's memory.

The design instead:
- observes and forecasts kernel-managed movement (compression, swap),
- relieves pressure only through mechanisms that a subsystem *offers* (a provider asking an app to shrink its own cache),
- tells the user when nothing safe remains.

---

## What the owner's idea *does* legitimately point at

Three things worth building, all within our constraints:

1. **Headroom accounting that understands reclaimability.** Distinguishing "free now" from "reclaimable soon" is exactly what makes a forecast useful. This is in `MP-ALG-003` (and remains `[Open]` — we start conservative at zero reclaimable and only add vetted sources).
2. **Provider abstraction for cooperative release.** If an app exposes a documented "release caches" API, a provider can use it — with measurement, preconditions, and a canary gate. That is the coherent version of "put memory away".
3. **Predictive relief before the cliff.** Starting relief 10 minutes early, when the system is still responsive, is the realistic analogue of "put it away before it's full".

---

## Consequences

**Given up:** the spectacular version of the idea. The product will never reduce a process's footprint by force.

**Gained:** we never corrupt anyone's process, and we never have to explain a data-loss incident caused by our own cleverness.

## Revisit triggers

None for the macOS user-space case. If Apple ever ships a public API for cooperative per-process memory externalization, item 2 above becomes the path — and it would still be the process's own choice, which is the correct shape.
