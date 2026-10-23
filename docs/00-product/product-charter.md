# MP-SPEC-001 — Product Charter

**Status:** Draft (Proposed)
**Version:** 0.1.0
**Last updated:** 2026-10-23
**Owner:** project owner
**References:** sbh (storage_ballast_helper), process_triage (pt), Apple developer docs (see `../01-research/`)

---

## 1. One sentence

Memory Pilot is a **user-level macOS daemon** that watches memory pressure on a small-RAM Mac, **explains** where memory pressure comes from, **forecasts** when it will get dangerous, and **relieves** it only through mechanisms proven safe — so long-running coding-agent sessions survive without the "Your system has run out of application memory … Resume / Force Quit" dialog.

---

## 2. The problem, stated exactly

On macOS, when compressed memory and swap cannot keep up with demand, the system posts a modal dialog: *"Your system has run out of application memory."* Some apps are marked `(paused)` and the user must choose **Resume** or **Force Quit**.

Three facts make this hard, and each must be reflected in the design:

1. **The paused apps are not necessarily the cause.** macOS picks victims; the source of pressure may be elsewhere (a browser helper, an MCP server, an agent subprocess, a runaway terminal scrollback buffer).
2. **Free-RAM percentage is not pressure.** Apple's own guidance bases Memory Pressure on available RAM, swap rate, wired memory and file cache — plus the OS's own compression. A machine at 85% used can be healthy; a machine at 60% used can be thrashing.
3. **A third-party process cannot safely relocate another app's heap.** You cannot copy a live process's heap to SSD and free the RAM behind its pointer graph and expect the app to keep running. Any product claiming this is wrong.

The consequence: the product must **prevent arrival at the dangerous state**, and when it cannot, it must **explain and recommend**, not destroy.

---

## 3. Target user and environment

| Item | Value | Why it matters |
|---|---|---|
| Primary machine | Mac mini M4, **16 GB RAM**, **256 GB SSD** | Small RAM ⇒ pressure arrives fast; small SSD ⇒ swap growth is itself a risk (disk headroom feeds swap) |
| Primary workload | Many concurrent coding-agent sessions (Claude Code, Codex, etc.), terminals, IDE, browser, MCP servers | Sessions are valuable and long-lived; killing one destroys hours of work |
| Secondary | Any macOS 13+ Apple Silicon Mac | Architecture must not be Mac-mini-specific |
| User skill | Technical; comfortable with CLI; wants a daemon, not a menu-bar toy | CLI-first product, JSON output, explainability |
| Language of product docs | Vietnamese for conversation-facing text; English for code, identifiers, and specs | Per project owner |

---

## 3.5 What v1 can actually cause, vs. what it can only enable

This distinction is load-bearing. Without it, "reduce out-of-memory events" quietly becomes a requirement to *find a way to free memory automatically* — which is exactly the path this project rejected.

| Category | What it means | Status in v1 |
|---|---|---|
| **A. The product does this directly** | measure pressure correctly; forecast with confidence; attribute growth to workloads; explain; warn; shrink its own footprint | **Shipped and complete** |
| **B. The product enables this — the outcome depends on the user** | a user sees a warning 20 minutes early and closes an unused browser tab, or postpones a build | **Shipped**; the tool's job is to make the decision easy and well-informed, not to make it |
| **C. The product can only achieve this with a verified provider** | automatic relief of memory held by another application | **Empty in v1** (ADR-006). No provider passes the bar today |
| **D. Unproven — must be measured** | "the frequency of out-of-memory events dropped by X%" | **Open**; MP-REQ-050 baseline harness, measured over 7 days |

**The failure mode this table prevents:** someone reads "success = fewer OOM events" and concludes the team must build an automatic memory releaser to hit it. Category A and B can reduce OOM events with **zero** providers. If that turns out to be insufficient, the honest conclusion is "a third-party daemon cannot fix this workload on 16 GB", not "add a risky provider".

**Corollary for the roadmap:** Phase 5's go/no-go is about whether A+B measurably helps — not about whether C is implemented.

---

## 4. What "success" means (product level)

**MP-SPEC-001-S1 — The dialog stops being routine.**
Over a 7-day observation window with the user's real workload, the number of "out of application memory" events drops substantially versus the same workload without Memory Pilot. *Substantially* is quantified in `requirements.md` (MP-REQ-050) after baseline measurement — we do not invent a number before we have the baseline.

**Attribution note (see §3.5):** this outcome is achievable through categories A and B alone — measurement, forecasting, warning, and the user acting on a good warning. It is **not** evidence that an automatic relief provider was needed or built. If S1 fails with A+B in place, the correct conclusion is that this workload exceeds what a user-space daemon can protect on 16 GB; the wrong conclusion is "therefore add a risky provider".

**MP-SPEC-001-S2 — When it cannot prevent, it predicts.**
When an event still occurs, the evidence ledger shows Memory Pilot had flagged elevated risk with a stated time-to-critical and a named contributor set, before macOS posted the dialog. The forecast's accuracy is scored, not asserted (`../03-algorithms/forecasting.md` §4).

**MP-SPEC-001-S3 — Sessions are never the price paid.**
Zero user-work loss attributable to Memory Pilot. This is enforced structurally (the daemon has no kill/pause/suspend capability at all, see `../04-safety/safety-invariants.md` SI-1..SI-3), not by policy configuration.

**MP-SPEC-001-S4 — The tool costs less than it saves.**
The daemon's own footprint stays inside a published budget (`../02-architecture/performance-budget.md`): idle RSS, CPU duty cycle, log write rate, disk consumption. A memory tool that itself becomes a memory problem has failed.

**MP-SPEC-001-S5 — Every decision is auditable.**
`mempilot explain` can answer, for any past interval: what was observed, what was forecast, what was considered, what was done, and what actually changed.

---

## 5. Product principles (non-negotiable)

**P1 — Diagnose before acting.** No action without evidence that identifies both the pressure and a safe relief path.
**P2 — Never destroy work.** No automatic kill, force-quit, SIGSTOP, suspend, restart, or freeze of any process not owned by the daemon itself. This is an architectural invariant, not a setting.
**P3 — Analysis cannot act.** The forecaster and attributor produce *findings*, never *syscalls*. Only the executor acts, and only on actions the policy engine authorized from a closed allowlist.
**P4 — Missing data is not good news.** A failed sample is `unknown`, never `normal`. Warnings are never cleared by absent evidence.
**P5 — Verify, don't assume.** Success is "measured pressure fell", not "the command exited 0".
**P6 — Fail conservative.** Any internal fault degrades to *observe only*.
**P7 — Explain in the user's terms.** Reports name what grew, when, by how much, and how confident we are — with the confidence visible.

---

## 6. What the product is not

| Not this | Because |
|---|---|
| A RAM cleaner / "free memory" button | Freeing cache to make a number look good does not reduce pressure and can make the system slower |
| A process killer with a memory heuristic | Killing an agent is the exact failure mode we exist to prevent |
| A memory-ballast file on disk | (Investigated: sbh's ballast is disk-specific.) A file on SSD does not become RAM; on a 256 GB disk it makes swap headroom worse |
| A user-space heap migrator | Technically unsafe to do transparently (see §2.3) |
| A GUI dashboard (v1) | CLI + JSONL + reports first; dashboards only after the data model is proven |
| A replacement for macOS's VM subsystem | We steer and observe; we do not reimplement paging/compression |
| A cross-platform product (v1) | macOS first. Cross-platform seams exist in the architecture but Linux/Windows support is out of scope until macOS is proven |

---

## 7. Known, accepted limitations

**L1 — No guarantee against exhaustion.** If the aggregate workload genuinely needs more memory than the machine can provide, and no safely reclaimable resource exists, no third-party daemon can prevent the OS dialog. Memory Pilot's job in that case is to have predicted it, named the contributors, and told the user what to do.
**L2 — Leak vs. legitimate growth cannot always be distinguished from outside.** RSS growth over time is not proof of a leak. The product reports *suspicion with evidence and confidence*, never "this app leaks".
**L3 — The OS warning itself cannot be disabled.** There is no supported public API for a third party to suppress the memory-pressure dialog. We prevent the cause; we do not hide the symptom.
**L4 — Some relief mechanisms are unproven.** Whether `memory_pressure -l warn`-style purge requests actually relieve pressure meaningfully on modern macOS, and at what latency cost, is an open question (ADR-006). Nothing enters the allowlist until measured.

---

## 8. Relationship to the reference projects

| Reference | What we take | What we explicitly do not take |
|---|---|---|
| **storage_ballast_helper (sbh)** — MIT | Daemon lifecycle & supervisor model; EWMA rate estimation + conformal calibration; shadow→canary→enforce rollout; evidence ledger + explain; bounded channels/backpressure; self-monitoring budgets; **its real macOS memory-pressure integration** (Dispatch source + `kern.memorystatus_vm_pressure_level`) | Ballast files; artifact deletion/scanner; disk-pressure tables verbatim; the 5-level disk scale |
| **process_triage (pt)** — MIT | Evidence-based reasoning over process signals; target identity & PID-reuse revalidation; protected-process thinking; expected-loss framing; shadow recommendations; spec-first engineering practice | Any kill/pause/freeze/restart action; /proc-based Linux assumptions; Bayesian killer UX |
| **Apple docs** | Memory Pressure semantics; `DISPATCH_SOURCE_TYPE_MEMORYPRESSURE`; Instruments/heap analysis as the ground truth for "leak" | — |

Rule for every borrowed idea: classify **Adopt / Adapt / Reject / Investigate** and record it in `../01-research/traceability-matrix.md`. No unclassified borrowing.

---

## 9. Naming

| Artifact | Name |
|---|---|
| Product | **Memory Pilot** |
| Repo | `memory_pilot` |
| Binary | `mempilot` |
| Daemon | `mempilotd` (or `mempilot daemon`) |
| Alias | `mp` (optional, user shell alias — **not** installed automatically; `mp` collides with Multipass and an X11 `mp(1)`) |
| Config | `~/.config/mempilot/config.toml` |
| Data | `~/Library/Application Support/mempilot/` |
| LaunchAgent label | `com.mempilot.agent` |

`mempilot` is the canonical invocation. The alias is a convenience that must never be required by docs or scripts.

---

## 10. Related documents

- `goals-and-non-goals.md` — decomposed goals, non-goals with rationale
- `requirements.md` — testable requirements (MP-REQ-xxx) with acceptance criteria
- `scenarios.md` — user scenarios and failure scenarios (MP-SCN-xxx)
- `../02-architecture/system-architecture.md` — module boundaries and data flow
- `../04-safety/safety-invariants.md` — the invariants P2/P3/P4 become
