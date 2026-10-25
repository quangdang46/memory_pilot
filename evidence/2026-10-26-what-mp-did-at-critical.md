# What Memory Pilot actually did when the machine was out of memory

`FINAL-STATUS.md` has said throughout that Critical pressure was never reached
live. That is wrong, and the ledger says so. Read from the three rotated ledgers
covering 2026-10-25:

```
KernelLevel distribution: {1: 8099, 2: 1093, 4: 2}
```

The machine reached **Warn 1093 times** and **Critical twice**, both at 22:59.
This is the wave the owner reported as "vừa có nhiều đợt memory leak nhưng mp
không có tác dụng" — and the ledger shows exactly why it had no effect.

## The two Critical windows, verbatim

```
22:59:07  level 4  free_pages 12723  compressor_pages 502566
    22:58:57  action_result  emit_advisory
    22:59:02  action_result  emit_advisory
    22:59:07  action_result  trim_own_buffers

22:59:17  level 4  free_pages 15800  compressor_pages 524896
    22:59:12  action_result  rotate_own_ledger
    22:59:17  action_result  trim_own_buffers
```

502566 compressor pages is 7.7 GB of compressed memory. The machine was
thrashing, and the daemon's entire response was to emit an advisory, trim its
own buffers, and rotate its own log file. None of those returns a byte held by
any other process.

That is the honest answer to "it doesn't handle anything": at the moment of
maximum pressure, the five available actions were

| action | what it releases |
|---|---|
| `emit_advisory` | nothing |
| `trim_own_buffers` | the daemon's own cache, tens of MB at most |
| `rotate_own_ledger` | nothing |
| `defer_launch` | prevents new demand |
| `stop_owned_child` | only children the daemon started, which is none |

Nothing in that list can touch the process holding the memory, because the
memory is held by applications the daemon did not start. This is a design
property, not a bug: v1 ships detect + recommend only (ST-1/ST-2 fail the build
if a termination primitive appears outside tests).

## What L1 does differently at that same moment

The first live L1 run, at 23:51 on Warn pressure, produced this:

```
39 process(es) are idle but not verified abandoned:
  com.apple.Virtu (pid 85066) (1796 MB)
      holds 1796 MB, no CPU for 5m15s
      parent gone, quiet 5m15s — short of the 10m0s bar
  ...
closing all of them would return ~11564 MB; the shortfall is 3158 MB
```

Wrong in two ways, both now fixed:

1. **Finder, the VM and every browser/IDE helper were candidates.** Nothing
   excluded them. `protectedExe` / `protectedPrefix` now refuse the desktop,
   the window server, virtualisation, Apple system services, and
   browser/IDE helpers, whatever their footprint or idle time.

2. **"Parent gone" was read from `ppid == 1`.** That is how launchd starts every
   service; it is not evidence that anything exited. macOS reparents an orphan
   to pid 1, so the evidence is the transition: `series.hadParent` records that
   a process was once under a parent, and `ParentGone` is set only when it is
   now under launchd. The third state — parent not observed, 25 % of this
   machine is outside coverage — is now stated as unknown instead of guessed.

## Still true, and worth saying plainly

- **It cannot free memory held by VS Code, Chrome, or node.** That memory is
  the working set of work in progress. Nothing user-space can release it
  safely.
- **L2/L3 (graceful quit, force kill) remain unimplemented** and require an ADR
  amendment backed by measured false-positive data. The product charter's
  "without ever killing your work" is the reason that bar is high.
- **Thresholds are chosen, not fitted.** There is still no labelled
  false-positive corpus.

What changed is that the tool no longer claims to handle what it cannot, and
when it does speak it names a process, a number, and whether that number closes
the gap.
