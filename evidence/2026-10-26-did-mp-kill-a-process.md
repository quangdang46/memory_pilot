# "Did Memory Pilot kill my node process?"

Asked on 2026-10-26 about pid 20128, a `node` process that was gone.

**No. Neither Memory Pilot nor its daemon signalled anything, and the structure
of the code makes it impossible for them to signal a process they did not
start.**

Four independent checks, all run against the live machine and its ledger:

## 1. One signal call site in the entire repository

```
internal/executor/stopowned_darwin.go:36:  syscall.Kill(target, syscall.SIGTERM)
```

The ST-1 gate fails the build if a second one appears outside tests. That call
is reachable only when all of the following hold (`executor.go`):

- the decision carries a `TargetID` of the form `owned:<pid>`;
- that pid is in the daemon's own spawn registry;
- the registry entry has a non-empty auth token;
- the daemon's mode is at least canary for `stop_owned_child`.

A process the daemon did not spawn through `mp run` has no registry entry, so
the check returns `target not owned or unauthorized (ST-22/ST-24)` and the
signal is never reached. This is structural, not a policy setting.

## 2. The daemon has never sent a signal

Every `action_result` in all three ledgers, 2026-10-25 to 2026-10-26:

```
emit_advisory      1107
trim_own_buffers      2
rotate_own_ledger     2
```

Zero `stop_owned_child`. The daemon's entire vocabulary of actions is emit a
record, trim its own cache, and rotate its own log.

## 3. pid 20128 was never named as releasable

Zero `relief_recommendation` records contain `20128`. The pid appears only in
system samples and in unrelated classification records for other groups.

## 4. macOS did not kill it either

`log show --last 8h` for jetsam / memorystatus kills: 0 events. So the process
was not the victim of memory-pressure reclamation.

## What the tool *does* do, which may be what was seen

At Warn or above it writes one `relief_recommendation` to the ledger and an
advisory to the IPC `report` surface. Both are text. Nothing is signalled,
nothing is paused, nothing is suspended. The 1107 `emit_advisory` records above
are the entire "action" history: at the two Critical pressure windows on
2026-10-25 the machine was thrashing with 7.7 GB in the compressor and the
daemon's response was to emit an advisory, trim its own buffers, and rotate its
own log.

That is the honest answer to "it doesn't handle anything" — and also the answer
to "did it kill my process". It has no capability to do either.

## The one thing that was wrong, now fixed

The first post-restart L1 run named 33 processes as releasable, all reporting
"no CPU for 5m30s" — the exact age of the daemon, because the quiet clock fell
back to each process's own start time. That is a claim of observation where
there was none. It is now measured from the moment attribution began watching,
so a freshly restarted daemon says what it actually knows:

```
short 2048 MB and nothing can be released safely — no process meets the bar
for being closed (quiet, orphaned, not a terminal or agent)
```

A recommendation is still only text. But a wrong one is still wrong, which is
why it is fixed rather than explained away.
