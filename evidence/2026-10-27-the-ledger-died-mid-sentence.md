# The ledger died mid-sentence, and the daemon kept answering

On 2026-10-27 the daemon was found alive, responsive, and completely blind. It
had written no ledger record for 31 hours. This is the account of how, why, and
what now prevents it — measured on the live machine, not reasoned about.

## How it looked from outside

Everything an operator would check said the daemon was fine:

```
$ mempilot status
mempilotd pid 8076 | uptime 19h17m33s | mode canary | pressure Normal (0.34,
  kernel) | self rss 92.7MB cpu 0.01% | ledger 40.8MB

$ ps -o pid,time -p 8076
  PID    TIME
 8076    2:28.96        (and 2:28.98 twenty seconds later — not spinning)
```

The IPC answered. The socket was live. The process had been up for nineteen
hours. `ledger 40.8MB` — the size is measured from disk, so it was reporting
files that had not changed since the previous afternoon.

Three checks separated "quiet" from "dead":

| check | finding |
|---|---|
| newest ledger file | `ledger.0006.jsonl`, mtime `2026-10-26 13:17:38` |
| open file descriptors | `lsof -p 8076` showed **no `.jsonl` handle at all** |
| CPU over 20 s | 2:28.51 → 2:28.55 — ticking, not hung |

A process holding no ledger handle cannot be writing a ledger, whatever
`status` computes.

## What the ledger itself recorded

The record trail is exact and needs no inference. Line 8352 of
`ledger.0006.jsonl`:

```
{"at":"2026-10-26T13:17:25.465498+07:00","candidates":["trim_own_buffers",
 "rotate_own_ledger"],"kind":"policy_candidates","reason":"pressure=4 score=0.85"}
{"action":"trim_own_buffers",...,"kind":"action_result","state":"executed"}
```

Line 8353:

```
{"kind":"system","sample":{"At":"2026-10-27T20:38:41.582912+07:00"},...}
```

31 hours, same file, no rotation in between. The daemon came back because it was
restarted by hand; the file it resumed writing was the one it had abandoned.

## The two faults

**`seqOf` never returned a sequence number.** It parsed `ledger.0006` with
`Sscanf(base[:i], "%s.%04d.jsonl", &struct{}{}, &n)`. `%s` is greedy — it
consumes the entire string and the rest of the pattern fails, leaving `n` at 0.
Every reopen therefore began at seq 0, and `openWith` set `l.seq = 0`
regardless of how many ledgers were on disk.

**A failed rotation destroyed the live handle.** `rotateFile` closed `l.f`
first, then opened the replacement with `O_EXCL`. `Open` had correctly resumed
appending to `ledger.0006.jsonl`. Then, at Critical pressure, the policy offered
`rotate_own_ledger`, the executor called `Rotate()`, `rotateFile` closed the
handle and tried to create `ledger.0001.jsonl` — which already existed. `O_EXCL`
failed, `rotateFile` returned the error, and `l.f` was left pointing at a
**closed** file.

Append's guard is `if l.f == nil { return "ledger closed" }`. A closed file is
not nil. So every subsequent write failed with

```
write .../ledger.0001.jsonl: file already closed
```

and all 17 `Append` call sites in the daemon discard that error, five of them
explicitly with `_ =`. The daemon went on sampling, classifying, deciding and
answering queries for 31 hours with nowhere to put any of it.

## Why this is the fourth instance of one defect class

Three earlier defects in this project had the same shape: **silence and
breakage produced identical observations.**

- a dedup that held back an escalation to Critical;
- a rate limiter that refused 193 of 261 advisory attempts, including at
  Critical;
- an executor that reported "advisory recorded" 40 times while zero records
  were written.

Each was fixed by making the silence inspectable. This one is the same defect
one layer down: the record that would have shown the others was itself absent.

## The fix, and the proof it was needed

- `seqOf` reads the digits directly.
- `rotateFile` opens the new file **before** closing the old one, and advances
  `seq` only on success.
- `Append` keeps writing into the current file if rotation fails, and retries
  once through a fresh rotation if the write itself errors.
- `Ledger.OnError` reports the transition into failure and out of it, once per
  episode; `mempilotd` wires it to stderr.

The tests were checked against the old code, because a test that passes either
way proves nothing:

```
=== OLD CODE ===
--- FAIL: TestOldSeqOf
    seqOf = 0, want 6
--- FAIL: TestOldRotateCollision
    ledger permanently dead after explicit Rotate collision:
    write .../keep.0001.jsonl: file already closed
```

That second line is the production failure, verbatim, from a unit test.

The first version of `TestFailedRotationKeepsWriting` **passed against the
broken code**. It forced the collision through the size cap, and `Append`
retries the rotation on the next write while size is still over the cap, so it
recovered by accident. The live path was `Rotate()` called directly by the
executor, which leaves size under the cap forever. The test now takes that path.
That is the second time in this project a regression test had to be rewritten
because it did not discriminate.

End to end on the shipped binary, with a data root seeded to the exact
production state (six ledgers present, seq 0 on reopen):

| run | records in `ledger.0006.jsonl` | files |
|---|---|---|
| before | 0 | 6 |
| run 1 (18 s) | 12 | 6 |
| run 2, after restart (18 s) | 23 | 6 |

Numbering continues from 6, no collision, no new file, nothing lost.

## What this does and does not establish

**Establishes:** the daemon now writes continuously across restarts and across
rotation, and a future ledger failure is reported on stderr instead of becoming
indistinguishable from a quiet period.

**Does not establish:**

- **The 31 hours are gone.** No record exists for them and none can be
  reconstructed. `mempilot report` covers only what was written after the
  restart.
- **Nothing else may have been silently failing.** This investigation started
  because the owner asked for one number to be checked. That every other
  `Append` error was discarded means any other write-path fault in the same
  period left the same non-evidence.
- **The launchd job was not registered when this was found.** The plist was
  absent from `~/Library/LaunchAgents`; the daemon had been running unmanaged
  since its last start, so nothing would have restarted it. `KeepAlive` is only
  worth anything if the job is loaded.

## The second half: `status` was the thing that lied

Making the failure visible on stderr was not enough. The owner does not read
`launchd.err.log`; they run `status`. And `status` printed

```
mempilotd pid 8076 | uptime 19h17m33s | ... | ledger 40.8MB
```

for the entire 31 hours. That number is `Ledger.SizeBytes()`, which globs the
files and sums their sizes on disk. It describes what was written, never
whether writing is still possible — so it read as healthy precisely because the
ledger had died with records in it.

`status` now also carries `ledger_healthy`, `ledger_error` and `ledger_seq` from
a `Health()` that reports the write path rather than the files. When writes are
not landing, the CLI prints above everything else:

```
  WARNING: the ledger is not accepting writes: <reason>
  everything below is computed live; nothing is being recorded
```

### What is verified, and what is not

**Verified live:** the healthy path. The rebuilt daemon reports
`"ledger_healthy":true, "ledger_error":""` and prints no warning, while ledger
bytes continue to grow.

**Verified in tests, at both layers:** the failure path. `Health()` returns
false with the reason after the handle dies while `SizeBytes()` still returns a
nonzero value — the exact trap — and `ledgerWarning` names both the failure and
its consequence.

**Not verified end to end:** a live daemon actually printing the warning. The
attempt was made by filling a 20 MB disk image until `df` reported 94% full;
the daemon went on appending because its records are small and the writes still
fit, so `Health()` correctly stayed true and the warning correctly stayed
silent. That is a valid result — it means the check did not fire when it should
not have — but it is not the same as watching it fire. Reproducing a real
`ENOSPC` inside a daemon whose ledger appends a few KB at a time needs a tighter
quota than a filesystem image provides.

The previous incident was found by a number that looked fine. This one is
closed by a number that now cannot look fine while the thing it describes is
broken — but that sentence has only been proven on the healthy side.
