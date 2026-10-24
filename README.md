# Memory Pilot

**A macOS daemon that prevents memory exhaustion for long-running coding-agent workloads — without ever killing your work.**

> Status: **implementation, phases 0–5 built.** Phase 0 spikes measured on the target Mac mini M4 (G-P0 closed); the skeleton, platform collectors, ledger, IPC, pressure model, forecasting, attribution, classification, policy/executor safety surface, and CLI are implemented with ST-1..ST-24 green (G-P1..G-P5 recorded; see `execution-records/`). Remaining are calendar-bound validations (24h run, 72h soak, 7-day baseline, canary) and the owner freeze. The specification set lives in [`docs/`](docs/README.md).

---

## The problem

On a 16 GB Mac running several coding agents, a browser, an IDE and MCP servers, macOS eventually shows:

> *"Your system has run out of application memory."*

Apps are marked `(paused)` and you must choose **Resume** or **Force Quit**. The apps it pauses are not necessarily the cause, and the process using the most memory is usually the one doing your work.

A RAM cleaner, a process killer, or a "free memory" button does not fix this. Killing the biggest process destroys the thing you were protecting.

## The approach

Measure memory pressure the way macOS defines it, forecast when it becomes dangerous, attribute growth to workloads, and relieve pressure only through mechanisms proven safe — while recording everything so any incident can be reconstructed.

**Eight invariants** ([`docs/04-safety/safety-invariants.md`](docs/04-safety/safety-invariants.md)):

| # | Invariant |
|---|---|
| SI-1 | No code path can signal, suspend or terminate a process the daemon did not spawn — absent, not disabled |
| SI-2 | Analysis cannot act; only the executor acts, on a closed allowlist, after re-validating preconditions |
| SI-3 | No filesystem writes outside the daemon's own data directory; no deletion of user data |
| SI-4 | Missing evidence never improves the verdict; a dead collector never looks like calm |
| SI-5 | Uncertainty is always visible — no output can state a forecast or a cause without confidence and evidence |
| SI-6 | Degrade before the host does — sampling, detail, logging and retention shrink under pressure |
| SI-7 | Fail closed — any internal fault demotes to observe-only and stops acting |
| SI-8 | Single writer, single controller — at most one daemon instance per user/host |

## What it will not do

- Kill, pause, freeze or restart your applications
- "Free RAM" to make a number look better
- Hide macOS's out-of-memory dialog
- Move another app's memory to SSD
- Claim a process is leaking

Reasoning for each is in [`docs/00-product/goals-and-non-goals.md`](docs/00-product/goals-and-non-goals.md) and the ADRs.

## Repository layout

```
docs/                     the specification set (start at docs/README.md)
  ├── 00-product/         scope contract (read first), charter, goals, requirements, scenarios, roadmap
  ├── 01-research/        audits of the two reference projects, traceability
  ├── 02-architecture/    layers, macOS APIs, module contracts, runtime, storage, CLI, security
  ├── 03-algorithms/      telemetry model, pressure estimator, forecasting, attribution, leak suspicion
  ├── 04-safety/          invariants, authorization, protection, failure recovery
  ├── 05-testing/         strategy, replay corpus, fault injection, budgets, safety suite
  ├── adr/                decision records tagged Proposed / Verified / Rejected
  ├── appendix/           glossary, FAQ, constants, JSON schemas
  └── spec-completion-checklist.md   what "spec done" means, and what remains
.tmp/                     shallow clones of the reference repositories (not part of the product)
```

## Key references

| Project | What we took |
|---|---|
| [`storage_ballast_helper`](https://github.com/Dicklesworthstone/storage_ballast_helper) | daemon architecture, EWMA/conformal forecasting, evidence ledger, and its working macOS memory-pressure integration |
| [`process_triage`](https://github.com/Dicklesworthstone/process_triage) | evidence-based reasoning, process identity, protection patterns, expected-loss framing |
| Apple developer docs | memory pressure semantics, `DISPATCH_SOURCE_TYPE_MEMORYPRESSURE` |

Both reference projects are read for **design ideas only**; no source is copied. Audit notes: [`docs/01-research/`](docs/01-research/).

## Next step

Long-run validation — 24h daemon run (G-P1, in progress), 72h soak (`qh9`), 7-day baseline (`2w1`), canary (`sbf`), pre-release replay (G-P6), and the owner freeze signature (G-FREEZE). Phase 0 spikes and ADR measurements are complete (G-P0 closed).

## License

TBD.
