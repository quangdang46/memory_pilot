# Why the warning board kept appearing, and what the product is actually for

## The goal, from the design documents

> **"A macOS daemon that prevents memory exhaustion for long-running
> coding-agent workloads — without ever killing your work."**
> — `docs/00-product/product-charter.md`

The complaint was that Memory Pilot had "no effect" and kept showing warnings.
Read against the design, that is not a bug in the classifier. It is the
difference between the tier that was built and the tier that was specified.

## What was specified

`docs/06-problem-first/process-relief-engine.md` defines four levels:

| Level | Action | Condition |
|---|---|---|
| L0 | Observe | default |
| L1 | **Recommend, with relief estimated** | suspicious or abandoned |
| L2 | Graceful quit | abandoned + opt-in + gates |
| L3 | Force kill | L2 failed + still eligible |

> "v1's deliverable is **a verified recommendation list with relief estimates**,
> not a killer."

And the sentence L1 owes the owner:

> "closing these three verified orphans frees ~400 MB; the gap is ~2 GB; this
> will not suffice."

## What was actually built, until now

Only L0 and a bare advisory. Measured on the owner's machine over 45 minutes:

```
98 advisories, every one at pressure Normal
most naming groups that were working:
  Code          3146 MB growth  — 62% CPU, compiling
  Google Chrome 2405 MB growth  — loading pages
  node           697 MB growth  — session helpers
```

Every one of those had `release` close to `growth` (Code: 3146 in, 1444 out).
They were workloads doing their job, and the tool was narrating them back.

The five available actions were `emit_advisory`, `trim_own_buffers`,
`rotate_own_ledger`, `defer_launch`, `stop_owned_child` — none of which can
release memory held by an application the daemon did not start. So "it does not
handle anything" was accurate: it had nothing to handle with.

## What was built in this session

**L1, as specified.** `internal/relief`:

- Three numbers kept separate, never merged into a score: abandonment
  confidence, interruption risk, expected relief. The design is explicit that
  merging them "produces exactly the wrong action in both cases".
- Every candidate carries its evidence. A number without a reason is a claim.
- The honest sentence: relief, gap, and — when relief cannot close the gap —
  "this will NOT cover it, and nothing else can be released safely". The design
  calls a kill list that "solves" an unfixable gap the most dangerous output the
  module can produce.
- Detect and recommend only. ST-1/ST-2 still fail the build if a termination
  primitive appears outside tests.

**The advisory gate now goes through it.** Growth alone is not news while the
machine is comfortable; a group is announced at most once an hour. The verdicts
still reach the ledger and `report` — only the interruption is removed.

## What this does not do

- **It does not free memory held by VS Code, Chrome, or node.** Nothing can,
  safely: that memory is the working set of work in progress. The design's L2/L3
  exist for a different class of process — verified orphans, where the parent is
  gone and nothing is using the memory. Those still require an ADR amendment
  backed by measured false-positive data.
- **It does not prevent leaks in other applications.** No user-space tool can.
  What it can do is tell the owner, once, which abandoned process is holding
  memory and how much closing it would return.
- Critical pressure, threshold fitting, and the 72-hour soak remain unproven, as
  `FINAL-STATUS.md` has said throughout.

## The honest summary

Before: a tool that reported work as a problem, 98 times in 45 minutes, with
nothing it could do about it.

Now: a tool that stays quiet while the machine is comfortable, and when it does
speak, says what could be released, how much that would return, and whether that
is enough.
