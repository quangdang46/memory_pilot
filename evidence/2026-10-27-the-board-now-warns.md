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

## A limit worth stating: the board cannot fire in its first 20 minutes

The classifier requires 1200 s of continuous evidence before it will call
anything a suspicion, and the attribution history that supplies it lives in
memory. A restart therefore starts the clock again.

Measured over the whole ledger (19 208 system samples, 2026-10-25 to 2026-10-27),
splitting at any gap longer than 60 s — the only way to tell a real daemon
outage from a temporary loss of evidence quality, since `from: Unknown` is
produced by both:

| run | length | could produce a verdict |
|---|---|---|
| 2026-10-25 11:25 → 18:55 | 7.50 h | yes |
| 2026-10-25 18:56 → 18:57 | 0.02 h | no |
| 2026-10-25 18:58 → 18:59 | 0.00 h | no |
| 2026-10-25 19:01 → 19:20 | 0.31 h | no |
| 2026-10-25 19:21 → 2026-10-26 13:17 | 17.93 h | yes |
| 2026-10-27 20:38 → 21:42 | 1.06 h | yes |

**3 of 6.** The three short runs were caused by me restarting the daemon while
editing it, not by anything the product did — so this is not evidence that the
daemon restarts too often. It is evidence that the 1200 s requirement, which is
deliberate anti-false-positive design, means a fresh start is blind for its
first twenty minutes and cannot be argued down without a false-positive corpus
that does not exist.

A first attempt at this analysis counted `pressure_transition` records whose
`from` was `Unknown` and concluded 42 of 64 restarts could never produce a
verdict. That was wrong: transitions at 11:17, 11:23, 11:30 and 11:33 on one
continuous run are minutes apart and cannot be restarts. The corrected count,
from gaps between samples, is 5 outages in two days — and the conclusion
reverses from "the board is structurally unable to fire" to "the board is blind
for its first twenty minutes after a restart."

## The relief board was silent at the exact moments that mattered

The 14 Critical transitions on 2026-10-26 all produced `relief_recommendation`
rows written to the ledger, but none were delivered to the user. At
11:18:01 (pressure Prevent, sliding toward Critical), the daemon logged:

```
closing 5 abandoned workload(s) would return ~601 MB
  /Users/.../node (pid 64747): 120 MB, idle by its own CPU record, parent gone
      to release: kill 64747
  ... (4 more)
shortfall is 3181 MB — this will NOT cover it, and nothing else can be released safely
```

That recommendation was logged, not shown. The user would have had to run
`mempilot report` to see it — which is exactly what the request was trying to
avoid.

It is delivered now. Only when Warn or above, after being written to the
ledger, and as a short banner body carrying the total and the command (the full
multi-line recommendation is too long for Notification Center without pushing
the action out of view). Below Warn there is no notification — a comfortable
machine does not need interrupting.

TestReliefRecommendationIsDeliveredNotJustLogged drives `emitReliefIfChanged`
with the same inputs as that Critical moment and requires a delivery record to
appear. It fails against the old code ("BOARD DID NOT SPEAK") and passes
now.

## Closed: the daemon warned the owner during a real Warn event

The link every earlier section called open is now observed. On 2026-10-28 at
09:39:21 the machine reached Warn (kernel_level 2 — the kernel's own reading,
not a simulated one) while the daemon was running unattended. The daemon:

1. found six abandoned processes holding 884 MB between them,
2. wrote the recommendation naming each pid and the command to release it,
3. delivered a notification two seconds later, recorded as `delivered: true`.

From the live ledger, verbatim:

```
2026-10-28T09:39:21  relief_recommendation  kernel_level=2  relief=884 MB  shortfall=3152 MB
  closing 6 abandoned workload(s) would return ~884 MB
     (pid 39241): 283 MB, idle by its own CPU record, parent gone
        to release: kill 39241
    /Users/.../node (pid 64747): 120 MB, idle by its own CPU record, parent gone
        to release: kill 64747
    ... and 4 more
  shortfall is 3152 MB — this will NOT cover it, and nothing else can be released safely

2026-10-28T09:39:23  advisory_notification  group=relief  delivered=true  reason=(none)
```

No human invoked anything. The earlier occurrence at 08:22:06 is the same pair,
produced while pressure was held at Warn.

This closes the last open row:

| Claim | Status |
|---|---|
| The daemon's own advisory reached a screen | **observed** — written and delivered at Warn, 2026-10-28 09:39 |

What it still does not establish: that a *human looked at* the banner at that
moment. Delivery is recorded by the daemon; whether the banner was seen is only
knowable by asking, and at the time of this run nobody was watching. Delivery
from this exact code path was separately confirmed by the owner from both a
shell and a launchd context, so the mechanism is not in question — only the
coincidence of a real event and an attentive human, which is not something the
code can arrange.
