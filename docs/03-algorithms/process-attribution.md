# MP-ALG-004 — Process & Workload Attribution

**Status:** Draft
**Version:** 0.1.0
**Reference:** process_triage evidence model (`crates/pt-telemetry`, `specs/telemetry-schema.md`), sbh `blame`/snapshot-diff thinking

Attribution is the part of the product that turns "your Mac is slow" into "this workload grew 2.1 GB in 20 minutes; here are the two processes, and here is what we do *not* know".

It is also the part most likely to be wrong in a convincing way. Hence every attribution carries coverage and confidence.

---

## 1. The question, precisely

> Between `t0` and `t1`, system headroom fell by `Δ` bytes. Which processes/groups account for how much of `Δ`, and how confident are we?

Note what this is **not**:
- not "which process uses the most memory" (a large, flat process is not a contributor);
- not "which process is a leak" (see `leak-suspicion.md`);
- not "which process should die" (never asked — SI-1).

---

## 2. Process identity over time

A PID is not an identity. Between two samples, PID 5012 may be a different process.

**Identity tuple (adopted, adapted from pt `specs/target-identity.md`):**

```
identity = (pid, start_sec, start_usec)
```

Optional strengthening: `exe_path` (if readable) and `ppid` at first sighting.

**Rules:**
1. A new `(pid, start_sec, start_usec)` is a **new series**. It never inherits another series' history, even if the PID matches.
2. If a series disappears between samples and does not return, mark it `ended_at` and stop accumulating deltas. Its total contribution stays in the window.
3. If a series disappears and a new one with the same PID appears, that is a *birth*, not a continuation. Report both.
4. Never compute a delta across an identity change. This is the classic double-counting bug.

**Why we care even though we never signal:** attribution stability. Without this rule, a short-lived build process recycled into a long-lived agent would appear as a huge sudden growth in "the agent".

---

## 3. Delta accounting

For each identity, at each process sample:

```
delta_i = resident_now - resident_prev          (same identity only)
```

Then reconcile with the system:

```
observed_growth   = max(0, headroom_prev - headroom_now)   # bytes
accounted_growth  = Σ max(0, delta_i)                       # positive deltas only
release           = Σ max(0, -delta_i)                      # frees, tracked separately
coverage_ratio    = accounted_growth / observed_growth       # may exceed 1 (see §3.1)
```

### 3.1 Why coverage_ratio is not "share"

Shared pages are counted in every process that maps them; compressed pages belong to no process. So:

- `coverage_ratio > 1` is normal and means **double-counting of shared pages**.
- `coverage_ratio < 1` means either growth outside visible processes (kernel, compressed pages, other users) **or** incomplete visibility.

**Therefore:** never normalize process deltas to sum to 100%. Present them as *raw growth in bytes* ranked, with an explicit note:

> "42 processes grew by a total of 3.1 GB; system headroom fell 2.4 GB. Shared pages are counted per-process, so process sums can exceed the system delta."

This sentence is the honest version of a blame report. sbh's `blame` command and pt's scoring both avoid the "sums to 100%" fallacy; we must too.

### 3.2 Compression is invisible to process attribution

When RAM is compressed, the compressor holds, say, 1.2 GB of physical pages that represent perhaps 3 GB of logical data. No process shows that 1.2 GB. Attribution must:
- report `compressor_bytes` as a **system-level** contributor line, separate from processes;
- never pretend to attribute compressed pages to a process.

This is a common source of "which app is using this?" confusion and is worth stating in `report` output whenever `compressor_pct` is non-trivial.

---

## 4. Workload grouping

A coding-agent session is not one process. It is: terminal → shell → agent → subprocesses (node, python, ripgrep, test runners) → possibly a browser/MCP server.

### 4.1 Grouping algorithm

```
1. Build the parent/child forest from (pid, ppid) at each process sample.
2. Roots = processes whose parent is 1/launchd, or whose parent is not visible,
   or whose parent exited (reparented).
3. Assign each process to its nearest "group anchor" ancestor, where an anchor is:
     a. a process in the user's configured workload list (e.g. Terminal, iTerm,
        VS Code, a specific agent binary), or
     b. a process with a stable, group-worthy name from the built-in anchor table, or
     c. the root itself.
4. Group id = (anchor identity).  Group label = anchor name (+ session index if repeated).
5. Processes that change parent keep their original group for the window
   (reparenting is recorded as an event, not a regrouping).
```

### 4.2 Instability is the enemy

Grouping that changes between samples makes reports meaningless. Rules:

- Group membership is **sticky within a window**; recompute only at window boundaries, or when a process's anchor identity changes (i.e. it was reparented *and* the anchor is gone).
- Group ids are stable strings recorded in the ledger, so `explain` can reconstruct them exactly.
- The report states the grouping rule that was applied ("grouped by nearest ancestor named `iTerm2`").

### 4.3 Why not group by executable name alone

Ten `node` processes in five sessions would merge into one blob. Parent lineage separates them. Name matching is used only to *find anchors*, never to merge leaves.

---

## 5. Contribution ranking

Rank groups by:

```
score(group) = Σ_{i ∈ group} max(0, delta_i)  over the window
```

with tie-break on `peak_delta_rate` (bytes/s) so a burst outranks a slow drift of the same magnitude — the burst is the more actionable finding.

Report per group:
- total growth in bytes
- number of processes
- growth rate (bytes/s) and whether accelerating
- start time of the growth
- the single largest contributor PID (for drill-down, not for blame)

### 5.1 What never happens

- A group is never labeled "the cause". It is labeled "grew by X".
- A process with high absolute RSS and zero delta is never in the ranked list — it goes into a separate "large but stable" section (which is often the most useful section, because it tells the user *not* to worry about it).

This "large but stable" section is a deliberate anti-feature against the RAM-cleaner mindset: it teaches the user that big ≠ problem.

---

## 6. Evidence record

Every attribution result is stored with:

```jsonc
{
  "window": {"start":"...","end":"...","duration_s":1200},
  "grouping_rule": "nearest_anchor:v1",
  "coverage": {
    "processes_seen": 512,
    "processes_denied": 37,
    "processes_estimated_total": 600,
    "coverage_ratio_system": 1.31,
    "note": "process sums include shared pages; may exceed system delta"
  },
  "system_delta_bytes": 2400000000,
  "compressor_delta_bytes": 410000000,
  "groups": [
    {
      "id": "anchor:iTerm2:3",
      "label": "iTerm2 (session 3)",
      "growth_bytes": 1450000000,
      "growth_rate_bps": 1208333,
      "accelerating": true,
      "process_count": 14,
      "top_contributor": {"identity":"(5012,1729...)", "name":"node", "growth_bytes": 980000000},
      "confidence": 0.82,
      "evidence": [
        "14 processes in group grew ≥ 10 MB",
        "growth began 3 min after a test runner started",
        "no comparable growth in other sessions with similar activity"
      ],
      "alternative_explanations": [
        "shared page accounting inflation if many subprocesses fork a large parent"
      ]
    }
  ],
  "large_but_stable": [
    {"label":"VS Code","resident_bytes":2100000000,"delta_bytes":12000000}
  ]
}
```

Requiring `evidence[]` and `alternative_explanations[]` in the schema is SI-5 applied structurally.

---

## 7. Confidence model (first version)

Deliberately simple, additive, and explainable:

```
confidence = base
           * (1 - denied_fraction)
           * (1 - min(0.5, |coverage_ratio_system - 1| * 0.25))
           * (1 - 0.3 * grouping_instability)
```

- `base` = 0.95 when the process sample is fresh and complete.
- `denied_fraction` = denied / total attempted (we cannot see them, so we cannot rule them out).
- coverage deviation penalty: a coverage ratio far from 1 means shared-page or invisible-growth distortion.
- `grouping_instability` ∈ [0,1] from the fraction of processes whose group changed within the window.

Output must always include the terms, so a user can see *why* confidence is low. (A single opaque number would violate the spirit of SI-5 even if it technically satisfies the schema.)

**Explicit non-claim:** this is not a statistical posterior. It is a heuristic index, named `confidence`, defined as above, and labelled as heuristic in the docs so nobody mistakes it for a probability.

---

## 8. Tests

| Test | Proves |
|---|---|
| T-AT-1 | PID reuse between samples does not create a false growth delta |
| T-AT-2 | A large flat process never outranks a small grower |
| T-AT-3 | Shared-mapping inflation is reported, not normalized away (`coverage_ratio > 1` path) |
| T-AT-4 | Compressed memory appears as a system line, not assigned to a process |
| T-AT-5 | Grouping is stable across a reparent event within a window |
| T-AT-6 | Denied PIDs reduce confidence and appear in coverage |
| T-AT-7 | Report always contains the shared-page caveat when sums exceed the system delta |
| T-AT-8 | Replay of a synthetic 3-session scenario attributes to the right groups |

---

## 9. Open questions

- **Q-7:** How often should grouping recompute? Every window is simple; per-sample is more accurate but unstable.
- **Q-8:** Should group ids include a session index (as in the example) or only the anchor? Session index helps humans, harms stability. Start with anchor + ordinal as recorded evidence, not as identity.
- **Q-9:** Is `physical_footprint_bytes` worth the extra per-PID call? If yes, deltas become much cleaner for shared-mapping cases. (Ties to ADR-002.)
