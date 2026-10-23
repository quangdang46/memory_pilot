# Research & Feasibility Matrix

**Status:** Draft — every row marked `[Verified]` was checked on this machine today; `[Open]` needs a prototype.
**Date:** 2026-10-23

Each candidate mechanism is rated on: does it exist, does it work, what does it cost, what can go wrong.

---

## 1. Admission control — the strongest lever

**What it is:** before a session starts, estimate whether the machine can hold it; refuse or defer if not.

### 1.1 How a launcher can intercept session starts

| Approach | Feasible | Cost | Coverage | Verdict |
|---|---|---|---|---|
| **Managed wrapper** (`mp run claude …`) | `[Verified]` trivially | none | only sessions the user starts through it | **Adopt as primary** |
| Shell function / alias replacing `claude` | `[Verified]` | none | interactive shells that source the profile | **Adopt as convenience** |
| WezTerm/Terminal config hook (`spawn_command`) | `[Open]` config-format dependent | low | terminals configured to use it | Investigate |
| PATH shim earlier than the real binary | `[Verified]` | low | all invocations in that shell | Consider; fragile with version managers |
| Blocking at `exec` via an LSM / EndpointSecurity | not permitted to a user-space daemon | — | — | **Reject** |

**Honest limitation:** if the user types `claude` in a shell that never sourced the wrapper, **admission control does not apply**. This must be stated in the product, not hidden. The mitigation is that the managed path is *easier* than the unmanaged one.

### 1.2 What to estimate before admitting

| Signal | Available now | Notes |
|---|---|---|
| Current headroom (`free + reclaimable`) | `[Verified]` | from the collector |
| Growth rate of the running session set | `[Verified]` | from the forecaster |
| Baseline footprint of a session for this project | `[Open]` | requires history keyed by project path; ~2 weeks of data |
| Cost of the *proposed* command (`claude` vs `claude` on a huge repo) | `[Open]` | at best a weak prior; do **not** pretend to precision |
| User's own live estimate ("roughly one session ≈ 1.2 GB here") | `[Verified]` via history | the realistic v1 answer |

**Design rule:** if we cannot estimate, we do **not** block. An admission controller that guesses and refuses wrongly is worse than none. Default when uncertain = admit, warn, and record.

### 1.3 The decision rule

```
budget       = headroom_now − safety_reserve
expected     = p50 footprint of a session in this project (fallback: global p50)
worst_case   = p90 footprint

if expected > budget:                     defer   (strong: it probably won't fit)
elif worst_case > budget:                 admit with warning (it might fit)
else:                                     admit
```

`defer` must offer: *wait N minutes*, *start anyway (recorded)*, *start with a lower-cost profile*. **Never a silent refusal** — a launcher that just fails to launch is indistinguishable from a broken tool.

**Verdict: feasible, high effect, low risk.** This is the only mechanism that prevents RC-1 rather than observing it.

---

## 2. Session lifecycle — the largest *recoverable* consumer

### 2.1 Can a session be closed and later resumed?

`[Verified on this machine]`

| Fact | Evidence |
|---|---|
| Claude Code persists sessions as JSONL | `~/.claude/projects/<encoded-project>/<session-id>.jsonl` — 4 993 such files present |
| A specific session can be resumed | `claude --resume <session-id>` / `claude -r` |
| Resume can fork instead of reusing | `--fork-session` |
| Sessions can continue in the background | `--resume <id>` continues "in the background under the same ID" |
| Sessions can be listed | `claude agents`, and the `/resume` picker |

**Consequence:** RC-2 (idle sessions holding memory) is **not** in the irrecoverable class. Closing an idle session and resuming it later preserves the conversation. The only loss is: in-flight tool state, background processes the session spawned, and any unsaved editor context.

This single verified fact converts the product's most common scenario from "watch it happen" into "recommend a reversible action with a proven resume path".

### 2.2 How to tell a session is idle

| Signal | Strength | Counter-example |
|---|---|---|
| No child process activity (CPU, I/O) for N min | medium | waiting on a long build that is itself idle |
| No terminal output for N min | medium | long-running single tool call |
| No foreground process on the tty | weak | detached background work |
| Parent shell prompt is at `$` (i.e. the agent exited) | **strong** | — |

**The last one is the only strong signal.** If the agent process has exited and the shell is back at a prompt, the session is definitively not running. Its memory has already been released — so this case consumes nothing. That means:

> **Idle-but-running sessions cannot be distinguished from working-but-quiet sessions with high confidence.** The product may *recommend* closing them, never act.

### 2.3 Verdict

| | |
|---|---|
| Detection | partially feasible (medium confidence) |
| Action | **recommendation with a resume command**, not automatic |
| Risk | low — the recommendation is reversible and the user decides |
| Effect | large when the user follows it, zero when they don't |

---

## 3. Orphaned child reaping — the only conceivable automatic remediation

### 3.1 What would have to be true

1. The parent identity `(pid, start_sec, start_usec)` is **gone** — not merely idle.
2. The child's ancestry now terminates at launchd, not at a live session.
3. No live session claims the child (no matching session id in its environment, if readable).
4. The child has done no I/O and consumed no CPU for a long window.
5. The child is not a launchd-managed service.

### 3.2 Why each check is weak

- (1) + (2) are necessary but not sufficient: an agent that **detached** a long build intentionally looks identical.
- (3) requires reading another process's environment — `KERN_PROCARGS2`; sbh's source notes Apple platform binaries withhold env even from root. `[Open]` for our targets.
- (4) is the same weakness as §2.2: quiet ≠ done.

**No combination of these checks proves "safe to kill".** The best achievable is "very likely abandoned", which is a *recommendation* grade of evidence.

### 3.3 Verdict

| Stage | Allowed | Evidence required |
|---|---|---|
| v1 | **detect and report only** | none beyond observation |
| future | act, with explicit user opt-in, per-process confirmation | a measured false-positive rate on real traces |

**This is ADR-013's subject.** The point of building detection now is to accumulate the only data that could ever justify the next stage.

---

## 4. Cooperative release (ask an app to free memory)

| Candidate | Exists? | Notes |
|---|---|---|
| Generic "release your heap" API on macOS | **No** | does not exist |
| Claude Code API to shrink its own context | `[Open]` | `/compact` exists as a user command; no documented programmatic hook found in `--help` |
| `memory_pressure -l` / purge | exists | frees file cache; no evidence it reduces pressure; **excluded** (ADR-006) |
| App-specific APIs (browsers, IDEs) | varies | per-integration cost, per-integration verification |

**Verdict:** remains `Investigate`. Not a v1 mechanism. The honest answer is that this lever is currently empty.

---

## 5. What is *not* feasible, restated so it stops being re-proposed

| Idea | Why not |
|---|---|
| Pre-reserve RAM as a ballast pool | reserving reduces headroom; releasing returns only what you took — net zero, plus you took it exactly when it was scarce |
| Move another process's heap to SSD | pointer graph belongs to that process (ADR-011) |
| Suppress the macOS dialog | no public API |
| Force the kernel to compress more | not exposed |
| Nice/renice a session to reduce its memory | priority affects scheduling, not allocation |

---

## 6. Summary ranking by (effect × feasibility) / risk

| Rank | Mechanism | Effect | Feasibility | Risk | v1? |
|---|---|---|---|---|---|
| 1 | **Admission control** | high | high | low (defer only) | **Yes — must be** |
| 2 | **Session-close recommendation with resume** | high (when followed) | high | none (user decides) | **Yes — must be** |
| 3 | Orphan detection | medium | medium | none if observe-only | **Yes — observe only** |
| 4 | Cooperative release | unknown | low | medium | No |
| 5 | Automatic orphan reaping | medium | medium | **high** | **No** (ADR-013) |
| 6 | Anything touching another process's memory | — | none | — | No |
