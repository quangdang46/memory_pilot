# Session Lifecycle — long-running workload protection

**Status:** Draft
**Date:** 2026-10-23
**Layer:** core capability (A-class), with adapter enrichment optional

Long-running workloads are the point of the machine. Most of the product's preventable loss comes from sessions that are **still holding memory but no longer working**. This module is how mp identifies and addresses that, app-neutrally.

---

## 1. Scope, and what is app-neutral about it

| App-neutral (core) | Adapter-enriched (optional) |
|---|---|
| detect a process tree that has been quiet for a long time | know it is a *coding agent* session |
| detect that the agent process in a tree has **exited** (only the shell remains) | infer "the shell is back at a prompt" (needs a terminal adapter) |
| detect that a tree's owner has exited while children remain | know the app's own lifecycle events |
| recommend "this group is idle; closing it would free N MB" | recommend "close it, then `claude --resume <id>`" |
| track whether the recommendation was followed | — |

**The core never depends on knowing what the application is.** It sees: identity, tree, footprint history, CPU/I/O, and parent-child changes. Every recommendation is derivable from those.

---

## 2. The signals, and their honest strength

| Signal | Strength | Why it can be wrong |
|---|---|---|
| No CPU (process + tree) for ≥ N min | medium | waiting on a network call, a build, or a human |
| No I/O for ≥ N min | medium | compute-bound work in progress |
| No child spawns | weak | a single long tool call |
| Agent process in the tree has exited (observable from the core: the pid is gone, its parent shell remains) | **strong — the agent is done running** | — |
| Tree root's parent exited, tree now reparented to launchd | **strong — orphaned** | intentional detach (`nohup`, `&`) |

**Core vs adapter on this row, stated precisely:** the core can observe "the agent process no longer exists" from process identity alone — that needs nothing app-specific. What the core *cannot* observe is "the shell is sitting at an interactive prompt" — that is a statement about the shell's own state (is it reading a line, running a child, piping output), which process-tree inspection does not expose. Seeing the prompt string requires reading the terminal's content, which only a terminal adapter can do (Q-16). So "agent exited" is core; "shell is back at a prompt" is an adapter-level refinement of the same event, not a synonym for it.

**The key finding:** the two strong signals both mean the memory has **already** been released (process gone) or is genuinely orphaned — neither identifies a *running idle session*. The common case, "agent running but idle", is only ever detectable at medium strength.

**Consequence:** in that common case, mp may **recommend**, never act. The recommendation must state the evidence strength so the user can judge.

---

## 3. What "idle" can and cannot mean

| Case | Memory held? | Recoverable? | mp's role |
|---|---|---|---|
| Agent process exited (shell may still be open — core cannot tell) | no | n/a | nothing to do — record the session end |
| Agent running, no activity 40 min | yes | depends on the agent's resume support | **recommend**, with the cost stated |
| Agent running, a tool call pending | yes | no | do nothing; it is working |
| Agent's children orphaned after exit | yes | no | T3 detect (orphan path) |
| Agent waiting for human input | yes | yes (the session is intact) | this is the *safest* close candidate if the adapter can see the prompt state |

The last row is worth calling out: **a session waiting at a prompt is both idle and perfectly resumable** — the conversation is saved, nothing is executing. An adapter that can see "agent showing its input prompt" can raise the recommendation's confidence substantially. Without it, the core sees only "quiet", which is weaker.

---

## 4. Resume awareness — adapter only

`[Verified on this machine]` Claude Code persists conversations as JSONL under `~/.claude/projects/<encoded-project>/<session-id>.jsonl` and supports `claude --resume <session-id>`. This makes close-then-resume lossless **for the conversation**.

**Scope of the claim — exactly what "lossless" covers:**

| Preserved by `--resume` | **Not** preserved |
|---|---|
| the conversation transcript (messages, tool results already recorded) | a tool call in flight at the moment of close |
| the working directory's saved session id | uncommitted edits a running tool had not yet written |
| the model's context as reconstructed from the transcript | background/child processes the session spawned (they are separate pids) |
| | in-memory state of a long-running program the agent had started |

So "resumable" is a statement about the **conversation**, never about the **in-flight work**. A recommendation that attaches `claude --resume` must not be read as "nothing will be lost" — anything executing at close time is lost even when the conversation comes back. This is why §3's row "agent waiting for human input" is the safest candidate: nothing is executing. A session mid-tool-call is *not* made safe by resume support.

**And this is a property of that agent, discovered through its CLI.** It is not a general macOS fact, and the core must not assume it.

Rules:

1. The core emits `RecommendCloseSession{group, footprint, idle_evidence, recoverable: unknown}`.
2. An adapter that has verified a resume mechanism may set `recoverable: true` and attach the exact command.
3. Without an adapter, `recoverable` is `unknown` and the advisory text **must not imply** the work can be recovered.
4. A recommendation that says "you will lose the session" is still useful — the user decides whether it was worth anything.
5. An adapter's resume claim must be tested against the real app; a wrong resume claim is worse than a vague one.

---

## 5. Recommendation content

```jsonc
{
  "kind": "close_session_recommendation",
  "group_id": "tree:4211",
  "footprint_bytes": 1450000000,
  "idle_evidence": [
    "no CPU for 47 min (tree)",
    "no I/O for 47 min (tree)",
    "no child spawned for 52 min",
    "agent process exited at 09:41 (shell remains)" // core-observable; adapter-only addition: "shell at interactive prompt"
  ],
  "idle_strength": "medium",                        // strong | medium | weak
  "recoverable": "unknown",                         // unknown | true | false
  "resume_command": null,                           // adapter-provided only
  "would_free_estimate_bytes": 1450000000,
  "confidence": 0.62,
  "alternative_explanations": [
    "waiting on a network or model response",
    "inside a long-running tool call with no output"
  ]
}
```

Note the `alternative_explanations` field: it is required for the same reason as in `leak_suspicion`. A recommendation that cannot name how it might be wrong is a guess dressed as advice.

---

## 6. Following up

| Event | Recorded | Purpose |
|---|---|---|
| user closed the recommended session | yes, correlated | measure whether recommendations are followed |
| pressure fell afterwards | yes | weak causal evidence (other things changed too) |
| user re-opened via resume | yes, if visible in the tree | validates the `recoverable` claim |
| user ignored the recommendation and nothing bad happened | yes | **calibration: this was not urgent** — lowers future aggressiveness for that class |
| user ignored it and the machine crashed | yes | raises the class's risk weight |

This loop is what turns the recommendation from a heuristic into a calibrated signal over a few weeks.

---

## 7. Anti-patterns

| Don't | Why |
|---|---|
| Close a session automatically | destroys in-flight tool state even when the conversation survives |
| Call a quiet process "idle" | quiet ≠ done; state the strength |
| Recommend closing the *largest* session first | the largest is usually the most productive |
| Recommend by count ("you have 9 sessions") | cost varies 10× per session |
| Imply resumability without an adapter | the user may trust it and lose work |
| Nudge repeatedly | a recommendation that appears every tick is noise; rate-limit and escalate wording only with severity |

---

## 8. Relationship to the ladder

| Severity | Session recommendations |
|---|---|
| L1 | none |
| L2 | one advisory listing the top idle candidates, if any |
| L3 | the same list, ranked by `footprint × idle_strength`, with recoverability shown |
| L4 | the dominant idle candidate specifically, with the numbers |
| L5 | the dominant candidate(s) plus the explicit statement that nothing safe remains |

Never more than one recommendation message per severity transition (rate-limited), so the user is not spammed during a slow climb.

---

## 9. Open questions

- **Q-16:** Can an adapter reliably observe "agent is waiting for input" without screen-scraping a terminal? (Terminal emulator content APIs vary; this is the highest-value adapter fact.)
- **Q-17:** Does `claude --resume` require the same working directory, or is the project path encoded in the session id enough? Affects how the recommendation is phrased.
- **Q-17b:** Given `--resume` restores a *conversation* but not in-flight tool state, should a recommendation that carries a resume command also state explicitly that any running tool call will be lost? The current rule (§4) says yes; it needs a test asserting the advisory text never implies otherwise.
- **Q-18:** What is a defensible `idle_window` default — 30, 45, or 60 minutes — measured against real sessions that were genuinely working while quiet?
