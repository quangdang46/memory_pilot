# The board now warns — and what that claim does and does not cover

The owner's standing request is that the memory-leak warning board never appear
again. An earlier pass made the board *silent when it should be*, which is only
half of the requirement: the other half is that when it should speak, the owner
hears it without going looking. Until today it could not.

## The gap that was measured

An advisory was written to a ledger file and nothing else. Reaching it required
remembering to run `mempilot report`. Verbatim from `report` on the live
machine, before this change:

```
advisory path: 224 verdict(s) — 0 announced, 224 held back
  223x bucket is not advisory-shaped
  1x terminal group
```

That line is correct and it is also the problem: every verdict was held back in
the observed window, and even an *announced* one would only have existed in a
JSONL file. The specification (`docs/06-problem-first/protection-actions.md`
§3.4) says `recommend` mode is **on by default** and must "surface a specific
advisory with the evidence and a one-command action". Nothing surfaced.

## What changed

**Delivery.** When — and only when — an advisory is successfully written, the
daemon hands it to macOS Notification Center through `osascript`. It is
best-effort and runs off the sampling goroutine, so a UI call can never block
the daemon. Every attempt records its outcome:

```json
{"kind":"advisory_notification","delivered":true,"reason":"","group":"…","bucket":"…"}
```

"Delivered" here means *the operating system accepted the notification*. It
does not mean the banner was seen — Focus mode, Do Not Disturb, or notifications
switched off for the calling app can all suppress it after acceptance. The
record says `delivered` and `reason` separately so the two facts cannot be
merged by accident.

**The write path was itself unsafe.** If the ledger failed while writing the
advisory, `recordVerdicts` still marked the group as emitted and rate-limited it
for an hour — an advisory the owner never received, blocking its own
replacement, reported as a success. This is the executor's "advisory recorded"
defect in a third location. It now marks emitted only when the write succeeds;
otherwise the failure is recorded in the heartbeat with its reason and the group
is not rate-limited against a message that was never sent.

## What was proven, and how

`TestTheBoardFiresOnARealLeakUnderRealPressure` drives the daemon's own chain —
`pipeline.ObserveProcesses` → `CloseWindow` → `classify` → `recordVerdicts` — with
a real `ledger.Ledger`, over 1500 s of simulated observation at the pressure
shape of `traces/slow-leak.json` (4.9 GB free falling to 48 MB, Normal → Warn),
with a group that allocates without using CPU. Observed output:

```
--- PASS: TestTheBoardFiresOnARealLeakUnderRealPressure
    board_e2e_test.go:30: advisory: group orphaned-batch grew by 1440 MB —
    unexplained sustained growth (suspicion) (confidence 0.65; basis:
    [sustained 1200s ≥ 1200s with no plateau grew 1440 MB in total
     activity flat for 1200s ≥ 600s (uncorrelated) not in cache-role table
     coverage 1.00 adequate])
```

`TestTheBoardStaysSilentOnAPlateau` runs the same fixture with growth stopping
after 200 s and requires zero advisories. Both pass on the current code.

Before this session the same fixture, run for the trace's own 595 s, could not
have produced a verdict at all: the classifier requires 1200 s of evidence. The
first version of the test was written that way and failed, which is what
established that the observation had to be lengthened rather than the thresholds
weakened.

## What this does not establish — read this before trusting the board

1. **No real leak process was observed.** The leak in these tests is synthetic
   process data fed to the pipeline. A real leak was observed once, on
   2026-10-26, when `leakprobe2` produced a genuine `leak_suspicion` advisory at
   12:42:18. That was before notification delivery existed.
2. **Notification display was confirmed by the owner, manually.** This one is
   closed by asking rather than by instrumenting: the owner was sent test
   notifications through the same `osascript` path the daemon uses — title
   `Memory Pilot — delivery test`, body `BANNER-VISIBLE-CHECK-9931` — and
   reported **"Có, tôi thấy banner"** (yes, I saw the banner). So macOS does
   deliver a banner from `osascript` on this machine; the path is not silently
   swallowed by TCC or Focus.

   What that does not establish: the banner was seen from a *test send*, not
   from an advisory the daemon emitted. The live daemon has been at Normal
   pressure throughout, so its own delivery has never fired. The mechanism is
   proven; the mechanism *in the advisory path, at pressure* is not.
3. **The pressure was simulated by a trace, not raised on the machine.** Driving
   this 16 GB Mac to Warn to observe a real advisory would require the memory
   exhaustion the owner explicitly ruled out.
4. **Thresholds remain chosen, not fitted.** No false-positive corpus exists.

The warning board's logic is now proven to fire on the shape it exists for and
to stay quiet on the shape it must not report. Its *delivery* is implemented and
inspectable but not observed reaching a screen. Closing that last gap needs
either a real Warn event with the owner watching, or a moment arranged
deliberately — and it should be stated as open until one of those happens.

## The advisory now carries an action

The spec's `recommend` mode is "surface a specific advisory with the evidence
and a one-command action". Until this change an advisory ended with its
evidence and left the owner to work out what to do — a notification about a
problem, not a recommendation. Every advisory now ends with:

```
— run `mempilot relief` to see what could be released
```

`mempilot relief` is read-only: it answers "what could be released, how much
would that return, and is that enough" and closes nothing. Printing a `kill`
command here would be the product telling the owner to do the one thing its
design forbids (ADR-012, "Revisit triggers: None"), so a test asserts that no
advisory text contains `kill`, `pkill` or `killall`.

Checked against the old code rather than assumed — the test fails there:

```
--- FAIL: TestAdvisoryCarriesAReadOnlyCommand
    advisory carries no one-command action: "group orphaned-batch grew by 1440 MB —
    unexplained sustained growth (suspicion) (confidence 0.65; basis: [...])"
```

## Where the whole claim now stands

| Claim | Status |
|---|---|
| The board fires on the shape it exists for | **proven** (integration test, real pipeline + ledger) |
| The board stays silent when growth plateaus | **proven** (same fixture, growth stops) |
| A written advisory carries an action | **proven** (test, fails without the fix) |
| A ledger failure cannot swallow an advisory | **proven** (test: no "emitted" when the write fails) |
| macOS shows a banner from this delivery path | **proven by the owner** (test send, seen) |
| The daemon's own advisory reached a screen | **not proven** — needs a real Warn event while the owner is watching |

The last row is the only one open. It cannot be closed by more code: it needs
a real pressure event, and driving this 16 GB machine to Warn deliberately
would require the exhaustion the owner ruled out. The next genuine pressure
episode is the experiment.
