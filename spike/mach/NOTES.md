# S-1: Go mach calls without cgo — VERDICT: YES (cgo-free platform layer confirmed)

Measured on darwin/arm64 (darwin 25.4.0, macOS 26), go1.26.4, `CGO_ENABLED=0`.
Method: `purego` (github.com/ebitengine/purego v0.8.4) Dlsym on
`/usr/lib/libSystem.B.dylib` + `purego.SyscallN`. No cgo, no C toolchain.
Reproduce: `CGO_ENABLED=0 go build -o mach main.go && ./mach` in this directory.

## host_statistics64

- Flavor is `HOST_VM_INFO64 = 4` (per `<mach/host_info.h>`), **not** 10.
  A probe across flavors 1..30 confirms: flavor 10 returns `KERN_INVALID_ARGUMENT`
  for every count.
- The count is an ABI **revision selector**, not a buffer size. Kernel revisions
  on darwin 25: `REV0 = 24`, `REV1 = 38` (matches `<mach/host_info.h>`
  `HOST_VM_INFO64_REV0/1_COUNT`). Our Go struct `VmStatistics64Rev1` (through
  `total_uncompressed_pages_in_compressor`) is 38 slots and every field Memory
  Pilot reads lives in it.
- Boundary scan (empirical, this host):
  - count 20/22/23 → rejected (kern 5)
  - count 24..90 → accepted (the kernel accepts count ≥ the oldest known
    revision; rejection fires for counts *below* 24 and for unknown flavors)
  - Go's runtime `syscall.RawSyscall` cannot reach mach traps; the libc gateway
    functions (`mach_host_self`, `host_statistics64`, `mach_port_deallocate`) via
    purego is the reliable path.
- Cross-check vs `/usr/bin/vm_stat` (same instant): compressor pages match to
  within timing jitter (396300 vs 396299). `vm_stat` "Pages free" uses different
  accounting than raw `free_count` (observed 4393 vs raw free 16655 with
  speculative 12386); our collector must document which convention it reports.

## proc_listpids / proc_pidinfo

- The raw `proc_info` BSD syscall (336) is NOT an exported symbol; use libproc's
  public `proc_listpids(PROC_ALL_PIDS=1, 0, buf, size)` and
  `proc_pidinfo(pid, flavor, arg, buf, size)`. Both resolve via Dlsym.
- `PROC_PIDTASKINFO (4)`: the kernel copies `sizeof(proc_taskinfo)` (96 bytes
  observed) — the buffer must be at least that; a 24-byte buffer returns 0.
  Offsets 0/8 are `pti_virtual_size` / `pti_resident_size`.
- Returned pid count at measure time: ~500.

## Timings (feeds S-5)

- `proc_pidinfo(PROC_PIDTASKINFO)`: ~2.9 µs/call ⇒ full-system sample at
  N=600 ≈ 1.7 ms of kernel time per cycle — trivially inside any budget.

## Exit items

- ADR-001: the mach-read path is viable cgo-free ⇒ platform layer stays cgo-free;
  pressure polling via `host_statistics64` VERIFIED (see S-3 for decode values).
- S-2 (cgo dispatch-source latency): NOT-APPLICABLE — no cgo bridge exists in
  this design; close citing this note.
