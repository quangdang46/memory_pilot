# Execution record — Phase 1 components (2026-10-23)

Machine: Mac mini M4 (target), darwin 25.4.0, go1.26.4, CGO_ENABLED=0.

## Evidence per bead

- **Skeleton (yxg)**: repo tree `cmd/{mempilot,mempilotd}`, `internal/*`,
  `platform/contract`, `internal/platform/{macos,other}`; Makefile with
  `ci = vet + test + gates + build`; build gates ST-1..ST-4 exist BEFORE the
  code they guard (scripts/gates/*.sh, all pass — verified below).
  ST-1 (no signal capability): pass. ST-2 (sealed actions): pass +
  `internal/model/actions_seal_test.go`. ST-3 (layering): pass.
  ST-4 (no network): pass (unix socket only).
- **host_statistics64 collector (7ez)**: `internal/platform/macos` —
  HOST_VM_INFO64=4, rev1 count 38, missing read ⇒ quality=missing (SI-4).
  Verified live in smoke run: ledger sample with KernelLevel=2,
  compressor=449967 pages, quality "good".
- **hybrid pressure signal (oxe)**: sysctl decode 1/2/4 (S-3), fail-safe
  unknown; `kern.memorystatus_level` co-read; absent sysctls are `ok=false`
  not zero. (Dispatch events: S-2 N/A; polling liveness per ADR-001.)
- **process inventory (l29)**: `Processes()` returns identity tuple (pid,
  start, exec placeholder), phys_footprint (S-7 source), coverage counts
  Attempted/Succeeded/Denied per MP-REQ-032; 403/586 readable observed in S-5.
- **ledger (x4a)**: `internal/ledger` — version header, JSONL append + fsync,
  rotation at 64 MB, keep-8 containment inside data dir (ST-12 test),
  crash-tail salvage (TestCrashTailTolerance), ReadAll replay.
- **state store + lock (dm1)**: `internal/store` — atomic write, path escape
  rejection (ST-11 test incl. "/etc/hosts", "..", "/abs"); SI-8 flock in
  `internal/supervisor.SingleInstance` (second instance fails loudly).
- **IPC (ihl)**: `internal/ipc` — unix socket, LOCAL_PEERCRED (SOL_LOCAL)
  peer-uid check (fixed during development: SOL_SOCKET returns zeroed cred on
  darwin), version envelope, closed op set, 10 s deadline. Verified live:
  `mempilot status` → healthy JSON, cli_exit=0.
- **config (14e)**: single-sourced defaults; hard floors enforced (system ≥1 s,
  process ≥5 s per ADR-004); observe|enforce mode validation; exit 3 on
  invalid construction before goroutines start.
- **self-monitor (ung)**: honest self-RSS via proc_pid_rusage (same API as for
  others); monotonic degradation ladder (120/200/400 MB → reduced/minimal/
  unhealthy) — budgets only shrink (SI-6); baseline matches S-4 (RSS 4.05 MB,
  CPU 0.0036 % in live smoke).
- **launchd (anw)**: plist generator (Standard, Nice 10 per ADR-008 Q-12),
  Install/Uninstall via bootstrap/bootout, Doctor hook.
- **disk headroom (gk9.1)**: statfs boot-volume free/total (S-3 feeds swap
  risk); failure is ok=false, never zero (SI-4).

## Smoke run (evidence)

`bin/mempilotd -data-root /tmp/mp-smoke` + `bin/mempilot status`:
`{"healthy":true,"rss_mb":4.05,"cpu_pct":0.0036,"goroutines":7}` — ledger
received real system samples within one tick.
