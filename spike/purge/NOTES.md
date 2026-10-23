# S-6: purge effectiveness — VERDICT: REJECTED for v1 stands (ADR-006 evidence)

Target Mac mini M4, darwin 25.4.0. Program: `main.go`. Reproduce:
`CGO_ENABLED=0 go build -o purge main.go && ./purge`.

## Experiment (ADR-006 §Investigation plan, executed as far as privileges allow)

1. **Purge mechanism availability (measured).** `purge(1)` requires root:
   `Unable to purge disk buffers: Operation not permitted` when run
   unprivileged. A user-scope daemon cannot issue a purge-style request at all
   without an elevation path the security spec (04-safety) will not grant.
   This alone is close to dispositive for v1.
2. **The premise ADR-006 relies on, directly measured.** With a controlled
   +6 GB allocator and *no purge call issued anywhere*, the kernel evicted
   file cache on its own (host_statistics64, same-instant):

   | state | external (file cache) | internal (anon) | compressor |
   |---|---|---|---|
   | baseline | 313 MB | 1546 MB | 1816→… |
   | after warm read (3 GB file set) | 824 MB | 545 MB | +481 MB |
   | after +6 GB allocator | **474 MB (−350 MB)** | 810 MB | +95 MB |
   | after allocator released | 778 MB | 569 MB | −69 MB |

   The OS already reclaims file cache before compressing/swapping — purging
   ahead of it buys nothing the kernel wouldn't take.
3. **Re-warm cost (bounded, measured).** Re-reading the 3 GB warm set after
   the pressure episode took 1.578 s vs 1.623 s warm (≈1.0×) on this SSD —
   i.e., at these sizes the worst-case first-access penalty is sub-second.
   The harm is real but small on Apple Silicon SSDs; it is a cost with no
   offsetting benefit given (2).

## Verdict table vs the provider bar (ADR-006)

| Bar | Result |
|---|---|
| 1. Controlled experiment | Effect of purge itself unmeasurable: no unprivileged mechanism (root required) |
| 2. Cost measurement | Re-warm ≈1.0× at 3 GB scale; eviction happens automatically anyway |
| 3. Repeatability | n/a — mechanism not invocable |
| 4. Bounded risk | Re-warm bounded (<1 s / 3 GB warm set, NVMe) but pointless |
| 5./6. Registration + canary | Not applicable — REJECTED |

**Purge-style relief stays REJECTED for v1** (ADR-006 unchanged); the spike's
measured contribution is: (a) no user-scope invocation path exists, and (b)
the kernel's own eviction already performs the only useful part under pressure.
If a future ADR revisits this, it must first solve the privilege problem —
which the safety model forbids.
