# MP-ARCH-007 — CLI Surface

**Status:** Draft
**Version:** 0.1.0
**Related:** MP-REQ-062..069, ADR-007

Conventions borrowed from pt (`specs/cli-surface.md`) and sbh: every read command supports `--json`, exit codes are stable and documented, stdout is data and stderr is diagnostics, and no command is interactive by default (so it is usable from scripts and by agents).

---

## 1. Commands

```
mempilot status [--json] [--watch]
mempilot report  --last <dur> [--json] [--top N]
mempilot forecast [--json] [--horizon <dur>]
mempilot explain <--last | --at <ts> | --id <id>> [--json] [--level 0-3]
mempilot processes [--json] [--sort growth|rss] [--top N]
mempilot policy status [--json]
mempilot policy set <key> <value>
mempilot policy protect <pattern> | unprotect <pattern>
mempilot service install | uninstall | restart
mempilot doctor [--pal | --service | --config | --ledger] [--inject <scenario>]
mempilot config path | show | validate | diff
mempilot record --name <n> --duration <d> --out <dir>
mempilot replay --trace <file> [--compare-baseline] [--out <file>]
mempilot version [--verbose]
```

`mempilotd` (daemon) takes `--config`, `--foreground`, `--log-level`. It is normally started by launchd.

---

## 2. `status` output (human)

```
Memory Pilot 0.1.0                      mode: observe   daemon: healthy (up 3h12m)

Pressure   WATCH      score 0.41   source: kernel
           free 6.2%   compressed 2.1 GB   swap 1.4 GB (+38 MB/min)   disk 21 GB

Forecast   critical in ~14 min (range 9-24 min)   confidence 0.78 (rising)
           headroom falling ~140 MB/min, accelerating

Top movers (last 15 min)
  +1.42 GB   iTerm2 (session 3)   14 procs   ↑ accelerating
  +0.61 GB   Google Chrome        22 procs
  +0.18 GB   VS Code               9 procs

Large but stable
   2.10 GB   VS Code      (+12 MB)    3.40 GB  Docker Desktop  (+0 MB)

Budget     RSS 41 MB / 60   CPU 0.2% / 0.3%   ledger 8.1 MB
```

Rules visible in that example:
- every forecast line carries confidence and a range;
- "large but stable" is always shown when non-trivial;
- the mode and health are on the first line;
- the budget line is always visible (the tool holds itself to the same standard).

## 3. `report` output

Grouped growth over the window, with the coverage footnote:

```
Window 10:00–12:00 (2h)   system headroom fell 2.4 GB   compressed +410 MB

GROWTH BY WORKLOAD
  iTerm2 (session 3)       +1.45 GB   (14 procs, top: node +980 MB)
  Google Chrome            +0.61 GB   (22 procs)
  ...
  system/compressor        +0.41 GB   (not attributable to a process)

Coverage: 512 seen, 37 denied, ~600 estimated.
Process sums include shared pages and may exceed the system delta.
```

## 4. `explain` levels

| Level | Content |
|---|---|
| 0 | one-line conclusion |
| 1 | conclusion + top evidence lines |
| 2 | + forecast/attribution detail and confidence terms |
| 3 | + raw sample window and full decision record (for debugging) |

## 5. Exit codes — two namespaces, never interchangeable

There are **two independent exit-code namespaces**. They overlap numerically on `3` and `4` by coincidence, not by design. Any doc, script, or log that refers to "exit code N" must say which namespace it means.

### 5.1 `CLI` process exit codes (`mempilot`)

| Code | Meaning | Notes |
|---|---|---|
| 0 | ok | |
| 1 | warning condition present (pressure ≥ Prevent) | deliberately script-visible; **not** an error |
| 2 | usage / bad arguments | |
| 3 | config error | CLI side: cannot parse or validate the config it was asked to read |
| 4 | daemon unreachable or another instance holds the lock | |
| 5 | internal error in the CLI process | |

Canonical source: this table. `failure-recovery.md` and `constants.md` reference it.

### 5.2 Daemon self-exit codes (`mempilotd`)

These are the codes the **daemon** returns to launchd when it terminates itself. They appear in `LaunchAgent` logs and in `doctor` output — **never** as the exit status of `mempilot`.

| Code | Meaning | Effect |
|---|---|---|
| 0 | clean shutdown requested by user | `KeepAlive{SuccessfulExit:false}` does **not** restart |
| 3 | config invalid at startup (fail closed, no goroutines started) | no restart loop; `doctor` explains |
| 4 | another instance holds the lock | |
| 10 | own memory budget exceeded for 3 consecutive checks | self-protective exit; launchd restarts after ThrottleInterval |
| 11 | unrecoverable internal fault | |
| 12 | ledger schema major incompatible | refuses to touch history |

Canonical source: `../04-safety/failure-recovery.md` §2.

### 5.3 Why the namespaces must stay separate

A script checking `$? -eq 3` after `mempilot status` means "config error". A `doctor` report showing daemon exit `3` means "the daemon refused to start because of config". These are *related* but the code paths, the remediation, and the log location differ. Merging them into one table would produce a document where the same number means two things and nobody can tell which.

**Rule:** `mempilot` never returns 10–12. `mempilotd` never returns 1–2 or 5 (there is no "usage error" or "CLI internal error" for a daemon launched by launchd; a daemon that cannot start exits 3).

## 6. JSON contracts

- `status --json` mirrors `state.json` plus a fresh sample if the daemon is reachable.
- `report --json` mirrors the `attribution` record.
- `explain --json` returns the decision/incident record plus referenced events.
- Every JSON payload has `"v": 1`.

## 7. Scriptability rules

- No command prompts. (There are no destructive commands to confirm.)
- `--json` output is stable; human output may change between minor versions.
- Durations accept `30s`, `15m`, `2h`, `7d`.
- Timestamps accept RFC3339 and `-15m` relative forms.

## 8. `doctor` checks

| Check | Failure message must say |
|---|---|
| LaunchAgent installed & loaded | the exact `launchctl` command to fix |
| daemon process alive (lock held) | whether the lock is stale |
| socket reachable & owned by user | the expected path and mode |
| ledger writable & schema compatible | which segment and why |
| clock sane (monotonic vs wall) | the observed jump |
| page size and total RAM plausible | the values |
| `vm_stat` cross-check | the mismatch in pages |
| Full Disk Access | "not required for memory telemetry" (explicit) |
| version match CLI vs daemon | how to restart |
