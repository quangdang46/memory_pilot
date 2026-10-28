# ADR-015 — Owner-invoked termination helper (`mpguard`)

**Status:** Accepted
**Date:** 2026-10-28
**Related:** ADR-012 (amends its practical consequence, not its decision), SI-1, ST-1

---

## Context

ADR-012 decided that the product has **no process-control capability**, and it
anticipated this exact request:

> "If the owner later wants a manual 'terminate this process' utility, it is a
> **separate product** with its own spec, its own safety analysis, and its own
> binary. It will not be a flag on `mempilot`."

The owner has now asked for it. The stated goal, in their words: keep the
machine from freezing overnight while unattended, so long-running coding-agent
sessions in WezTerm survive; the other applications on the machine are not
wanted.

Two facts made the request concrete rather than hypothetical:

1. On 2026-10-26 the machine reached Critical pressure fourteen times. Each
   time the daemon produced a correct recommendation naming abandoned
   processes and the command to release them, and each time nothing happened,
   because nothing can happen: the owner was asleep.
2. A dry run of the helper on the live machine found roughly 5.5 GB held by
   processes that had been idle for 13–107 hours — abandoned `tsx server.ts`,
   `next-server`, a ZCode helper, Telegram. These are exactly the processes
   whose loss costs nothing.

So the capability is wanted, and the danger ADR-012 identified is real but
narrow. Both can be honoured at once, because they apply to different actors.

## Decision

**A second binary, `mpguard`, is added. `mempilotd` keeps ADR-012 exactly as
written.**

The distinction that makes this safe is not cosmetic:

| | `mempilotd` | `mpguard` |
|---|---|---|
| When it runs | always, unattended, for weeks | when a human types the command |
| Who decides | a policy engine on its own judgement | the owner, reading the list first |
| Blast radius of a mistake | unbounded in time, silent | one invocation, printed, interruptible |
| Mode | never signals a foreign process | terminates only what it names, on request |

The prohibition ADR-012 makes is about the *product acting alone*. A person
running a command and reading its output is a different relationship, and it is
the one ADR-012 named as legal.

## What `mpguard` will not do

Each line is enforced by a test, not by intent:

- **Never a process it cannot identify.** Measured: Claude Code's current build
  (pid 58684, `claude --resume …`) makes `proc_pidpath` return 0, so its only
  identity is the version directory `2.1.310`. A process with no readable image
  path is skipped, because killing what cannot be checked against the
  protection lists is the one mistake with no recovery.
- **Never a coding agent.** `claude`, `codex`, `opencode`, `cursor-agent` and
  eleven others, matched on the name *and* every path segment, and with a
  `.exe` suffix stripped — measured, the running OpenCode binary is
  `…/bin/opencode.exe` and an exact-name list let it through.
- **Never the terminal.** `wezterm-gui`, `iTerm2`, `Terminal`, `tmux`,
  `Alacritty`, `kitty`, `sshd`. This is the session the whole exercise protects.
- **Never the desktop or the kernel.** Finder, Dock, WindowServer, loginwindow,
  launchd, the virtualisation host, and anything under `/System/`,
  `/usr/libexec/`, `/usr/sbin/`, `/Library/Apple/`.
- **Never something that is working.** At least 10 minutes of estimated idle
  *and* at least one hour of life. Measured: a 120 s idle bar selected a
  rust-analyzer at "49m of 50m life" and a Chrome helper at "10m of 12m life" —
  young processes that have not done much yet, which is not the same as a leak.
- **Never on missing evidence.** A process whose CPU accounting could not be
  read is skipped (SI-4). Absent data has never authorised an action in this
  product and does not start now.
- **Never without `--yes`.** The default is a printed list and the sentence
  "nothing was killed".

## Consequences

**Given up.** The clean claim "this repository contains no capability that can
terminate a foreign process" becomes "the *daemon* contains no such
capability". That is a weaker, more precise claim, and it must be stated that
way everywhere it appears.

**Gained.** The owner gets the thing they actually asked for — an unattended
machine that survives the night — without the daemon acquiring judgement over
which sessions matter.

**Costs and remaining risk.**

- The helper's decisions are only as good as its idle estimate, which is
  derived from lifetime CPU rather than a per-process "last ran" clock. It
  under-reports idleness for anything that ran hard early and stopped, which
  errs toward not killing.
- Killing a GUI application's main process still loses that application's
  unsaved state. The helper ranks orphans first and never selects an
  unidentifiable process, but it cannot know what is in a window.
- `mpguard` is not run automatically and is not scheduled. Nothing in this
  decision makes it act unattended.

## Revisit triggers

- `mpguard` ever terminates a process the owner did not expect, in a way the
  rules above should have prevented.
- A request to run it on a schedule. That would reintroduce the unattended
  actor ADR-012 rejects and needs a new decision, not a flag.
