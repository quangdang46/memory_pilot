# Reference Audit — process_triage (pt)

**Repo:** `github.com/Dicklesworthstone/process_triage`
**Cloned to:** `.tmp/process_triage`
**License:** MIT
**Size:** 485 Rust files across 8 crates (`pt-core`, `pt-common`, `pt-math`, `pt-config`, `pt-redact`, `pt-bundle`, `pt-telemetry`, `pt-report`) + a Bash wrapper + BATS suite.
**What it is:** a safety-first CLI that identifies abandoned/zombie processes and recommends cleanup actions, using Bayesian inference and an evidence ledger.

**The tension in using it as a reference:** pt's entire decision core exists to choose *whether to kill*. Our product forbids killing (ADR-012). So what we take is not the objective but the **machinery of justification** — how evidence is gathered, how identity is verified, how actions are gated, and how a recommendation is made auditable.

---

## 1. The decision pipeline

`README`/`AGENTS.md` describe: `Collect → Infer → Decide → Act → Report`.

Verified structure in `crates/pt-core/src/`:

| Stage | Module | What it contains |
|---|---|---|
| Collect | `collect/` (`macos.rs`, `pressure.rs`, `protected.rs`, `lineage_collector.rs`, `shared_resource_graph.rs`, `incremental.rs`, `tick_delta.rs`) | per-OS evidence gathering |
| Infer | `inference/` (~40 modules: `posterior.rs`, `belief_state.rs`, `bocpd.rs`, `kalman.rs`, `imm.rs`, `hsmm.rs`, `conformal.rs`, `evt.rs`, `hawkes.rs`, …) | a very large statistical toolbox |
| Decide | `decision/` (~44 modules: `expected_loss.rs`, `blast_radius_estimator.rs`, `cvar.rs`, `dro.rs`, `voi.rs`, `rate_limit.rs`, `pressure_regime.rs`, `sequential.rs`, …) | action selection, risk, rate limiting |
| Act | `action/` (`signal.rs`, `freeze.rs`, `renice.rs`, `cgroup_throttle.rs`, `cpuset_quarantine.rs`, `executor.rs`, `prechecks.rs` 3 340 lines, `recovery.rs`, `recovery_tree.rs`, `supervisor.rs`) | the destructive half |
| Report | `output/`, `report/`, `shadow.rs`, `audit/` | structured output + calibration records |

**Immediate observation for our design:** pt has *far more* statistical machinery than a v1 needs. The module list alone (BOCPD, Kalman, IMM, HSMM, Hawkes, EVT, copulas, CTW, conformal, bandits, Gittins indices, CVaR, DRO) is a warning as much as a resource: it is what accumulates when sophistication is added without a gate. Our ADR-005 exists to prevent this.

---

## 2. Expected-loss decision core (verified)

`crates/pt-core/src/decision/expected_loss.rs`

```rust
pub fn decide_action(
    posterior: &ClassScores,
    policy: &Policy,
    feasibility: &ActionFeasibility,
) -> Result<DecisionOutcome, DecisionError>
```

Mechanism:

1. For each action in `Action::ALL`, **skip if not feasible** (`ActionFeasibility::is_allowed`) — infeasible actions are recorded as `DisabledAction{action, reason}` rather than silently dropped.
2. Compute `expected_loss_for_action(action, posterior, policy.loss_matrix)`; a missing loss entry disables that action with a reason.
3. If nothing is feasible → `DecisionError::NoFeasibleActions` (an explicit error, not a silent "do nothing").
4. Select the minimum-loss action (`select_optimal_action`, returning a `tie_break` flag).
5. Compute an SPRT boundary (`SprtBoundary{log_odds_threshold, numerator, denominator}`) so the decision carries the statistical strength of the evidence, not just the choice.

`DecisionOutcome` also carries: `posterior_odds_abandoned_vs_useful`, `recovery_expectations`, `risk_sensitive` (CVaR), `dro` (distributionally robust), and a `DecisionRationale`.

### What we adopt

- **Feasibility as a first-class filter with recorded reasons** — this is exactly the shape of our `refused(reason)` record (action-authorization §4). Adopt.
- **Missing policy entry disables the action rather than defaulting a loss.** Fail-closed inside the cost model. Adopt.
- **`NoFeasibleActions` as an explicit outcome.** Our `NoAction{reason}` should be equally explicit and recorded, not an empty list.
- **Rationale travelling with the decision.** Adopt: our `Decision` record already includes chosen/considered/refused.

### What we reject

- The loss matrix over *classes of abandonment*. We have no abandonment classes.
- CVaR / DRO. Interesting for risk-sensitive resource allocation; not justified for a v1 with no destructive actions. Recorded as `Investigate`, not adopted.
- SPRT boundaries in the decision output. Our evidence strength is expressed by confidence + calibration coverage; adding a second statistical vocabulary would confuse rather than clarify.

---

## 3. Blast radius (verified)

`crates/pt-core/src/decision/blast_radius_estimator.rs`

```
risk_score = clamp(direct.score * direct_weight + indirect.adjusted_score * indirect_weight, 0, 1)
confidence = evidence_completeness
total_affected = indirect.transitive_affected
```

with `RiskThresholds` → `risk_level`, and a human-readable `summary` built from the components.

### What we adopt (strongly)

The **concept**, renamed. For us the "blast radius" of a relief action is *how many sessions/processes would be disturbed*. Even though our v1 actions are self-only, the provider contract should require a blast-radius declaration so that a future provider cannot be added without answering "what does this touch?" (action-authorization §2, process-protection §5).

Second adoptable idea: **confidence = evidence completeness**. We use the same construction for attribution (§7 of process-attribution): coverage directly caps confidence. It is a simple, honest, defensible choice — much better than a fitted number nobody can explain.

---

## 4. Target identity & revalidation (verified)

`specs/target-identity.md` is one of the better specs in either repo, and it is directly relevant even though we never signal a process.

Key content:

- Identity tuple: `pid`, `start_time_ticks`, `boot_id`, `uid`, `euid` → derived `start_id = "<boot_id>:<start_time_ticks>:<pid>"`.
- Optional strengthening: `exe_inode` + `exe_dev`, `cmdline_sha256` (hash only, never the raw command line — a privacy decision we should copy for any exported artefact).
- **Revalidation protocol**: re-read identity immediately before a destructive action, *after* lock acquisition; on mismatch, abort with `reason=identity_mismatch`, `status=skipped`, `requires_rescan=true`, and include `identity_observed` in the result.
- Locking: per-user lock file, JSON contents (holder pid, session id, expiry, command), `flock`, 10 min TTL, stale detection by PID liveness or expiry.
- Standard reason codes: `identity_mismatch`, `not_running`, `privilege_blocked`, `sudo_required`, `lock_busy`.

### What we adopt

| pt | Memory Pilot |
|---|---|
| `start_id = boot_id:start_ticks:pid` | `identity = (pid, start_sec, start_usec)` — same purpose, darwin-native fields (attribution §2) |
| revalidation right before action | precondition re-check right before execution (action-authorization §4) |
| `identity_observed` in mismatch records | `refused(precondition)` includes the observed value |
| JSON lock file with holder identity + TTL | `daemon.lock` (persistence §1), single-instance (ST-21) |
| `cmdline_sha256` instead of raw cmdline | we collect no cmdline at all; for exported bundles, hash if ever needed |

**One divergence:** pt's identity exists so it can kill the *right* process. Ours exists so a recycled PID doesn't inherit another process's memory history (attribution §2 rule 4). Same tuple, entirely different failure being prevented — which is a good illustration of why the *pattern* transfers even when the *purpose* doesn't.

---

## 5. Protection model (verified)

`crates/pt-core/src/collect/protected.rs` — protection at scan time, *before* inference ("more efficient than scoring first and blocking later, and provides better UX").

Verified rules:

- A `ProtectedFilter` built from `guardrails.protected_patterns`, matched against `comm`, `cmd`, and `user`.
- `BUILTIN_PROTECTED` rules, each with a name and a *reason string*, e.g. terminal multiplexers (`tmux`, `zellij`, `screen`, `wezterm-mux-server`) with the note *"hosts every interactive/agent session on the machine"*; agent coordination services; SSH ControlMaster; mux transports.
- `BUILTIN_FORCE_REVIEW` for AI agent CLIs (`claude`, `codex`, `gemini`, `cursor-agent`, `aider`, `opencode`, `goose`, …), note: *"AI agent CLI session: may be mid-task; kill only after manual review"* — a **softer** class than protected: not forbidden, forced to human review.
- `agent_kind()` recognises which agent CLI a command line runs.

### What we adopt

1. **Every protection rule carries a human-readable reason.** Our `policy status` should print *why* a group is protected, not just that it is. Adopt.
2. **Two protection strengths** — `protected` (never) and `force_review` (requires human). In our product there is no second tier for actions (we have no destructive actions), but the distinction maps onto **reporting**: `<protected>` groups are still reported factually, while `<sensitive>` groups (agent CLIs) get softened language and are never named in an advisory as a thing to close (§2 of process-protection). Adopt the concept.
3. **A built-in rule set with reasons**, rather than only user-configured patterns. We should ship a default list of "session-critical" classes (terminals, IDEs, agent CLIs, mux servers) so a fresh install is safe without configuration.

### What we reject

The output of this machinery — killing. Also `cmd`-based matching: we deliberately do not collect command lines (privacy, telemetry §6), so our anchors must match on `name` + `exe_path` + lineage. That is a real reduction in matching power, and it is the right trade.

---

## 6. macOS collection (verified)

`crates/pt-core/src/collect/macos.rs` (compiles only on darwin).

- Explicitly documents *"since /proc is not available"* and lists the tools it shells out to: `ps`, `lsof`, `netstat`, `sysctl`, `csrutil`, `launchctl`. It also documents SIP effects on `dtruss`/`lsof` and auto-detects tool capabilities at startup.
- **Exception:** identity facts use the public libproc API directly — `proc_pidinfo(pid, PROC_PIDTBSDINFO)` → `proc_bsdinfo`, with the comment *"Exact identity facts … (public `<libproc.h>` API)"* and *"This avoids relying on unstable/private Darwin libc process structs"*.
- `MacBsdInfo { start_us, uid, ppid }` — `start_us` computed as `pbi_start_tvsec * 1e6 + pbi_start_tvusec`, described as *"exact, unlike `ps etime`"*.
- `read_session_stat()` synthesises a Linux-shaped `/proc/<pid>/stat` record from `proc_bsdinfo` + `getsid(2)`, mapping `pbi_status` (1 SIDL, 2 SRUN, 3 SSLEEP, 4 SSTOP, 5 SZOMB) to Linux state chars. Clever: it lets one session-safety code path serve both platforms.

### What we adopt

- **libproc via `PROC_PIDTBSDINFO` for identity** (`pbi_start_tvsec/tvusec`) — cheap and exact; complementary to sbh's `PROC_PIDTASKALLINFO` (which returns the same start fields plus sizes/CPU in a single call, so we should prefer the latter and take identity from it).
- The lesson **"do not rely on unstable/private Darwin structs"** — reinforces defining our own layout where needed (macos-integration §2 ABI pitfall).
- The lesson **"shelling out is a fallback, not an architecture"** — pt shells to `ps`/`lsof`; sbh calls APIs. For a daemon sampling every few seconds, sbh's approach is correct and pt's would be prohibitive (fork+exec per sample). We follow sbh.

### What we reject

`ps`-scrape-based sampling for the hot path; `lsof`/`netstat` collection (not needed for memory pressure).

---

## 7. Pressure sensing — an important negative example (verified)

`crates/pt-core/src/collect/pressure.rs` (Linux): reads PSI (`/proc/pressure/*`), loadavg, `/proc/meminfo`, `/proc/vmstat`, file handles. Its doc comment is exemplary and worth quoting:

> *"A source that cannot be read is reported as unavailable with the reason. It never reads as zero: 'no PSI on this kernel' is not 'no pressure'."*

That sentence is essentially our SI-4, written earlier. Adopt the wording as a design principle.

`crates/pt-core/src/decision/mem_pressure.rs` — its memory-pressure state machine — is a **negative example for us**:

- It thresholds on `utilization = 1 - available/total` (0.80 warn, 0.95 emergency) and optionally PSI.
- Three modes, transition after `transition_count` consecutive signals (default 2).
- Correctly defaults `auto_apply: false` and proposes plans rather than acting.

**Why we do not copy it:** utilization-based thresholds are exactly the "85% RAM means bad" model that macOS's memory-pressure concept exists to replace. On macOS there is no PSI, `available` is not the right denominator (compression and swap change what "available" means), and the whole point of our pressure estimator is a multi-signal state with provenance. pt's module is fine for a Linux daemon making coarse decisions; it would be wrong here.

**Adopt only:** the consecutive-signal requirement (hysteresis/dwell) and the `auto_apply=false` default. Both are in our pressure-estimator §6 and action-authorization §5.

---

## 8. Shadow mode & calibration (verified)

`crates/pt-core/src/shadow.rs` + `pt-telemetry::shadow`:

- Records prediction snapshots (belief, state, identity hash, comm) to local storage for later scoring.
- Tracks `miss_count` per pending observation with a `miss_threshold` (2) — predictions are reconciled against later reality, and misses accumulate.
- Pending state persisted to `pending.json` so a restart does not lose reconciliation.

### What we adopt

The **reconciliation loop**, which our product needs just as much as pt does:

1. A forecast is a prediction with a due date.
2. When the due date passes, compare to reality and record `covered | missed` (feeds the e-process guard, forecasting §4.2).
3. Pending predictions survive restart.

Our equivalent is the calibration record in `persistence.md` §2 and the `forecast` record's follow-up. The persistence detail (`pending.json` with a miss threshold) is a good concrete template for "reconcile measurements with later reality across restarts".

---

## 9. Specs worth imitating

| Spec | Why it matters to us |
|---|---|
| `specs/target-identity.md` | The model for our identity/revalidation section — short, normative, with JSON schemas and error codes |
| `specs/telemetry-schema.md` | Versioned schema discipline for recorded data |
| `specs/cli-surface.md` | Command surface, exit codes, robot/JSON contract |
| `specs/redaction-policy.md` | Redaction for exported data — feeds our security-and-privacy §4 |
| `specs/session-model.md` | Session grouping concepts — relevant to our workload grouping |
| `specs/package-architecture.md` | Crate boundaries and dependency direction — the same discipline as our layering (SI-2) |

The `specs/` directory exists because pt's CHANGELOG claims 40+ specification documents were written. Whether or not that count is exact, the *practice* is the one our project is following deliberately.

---

## 10. Testing practices worth imitating

- **BATS** (`test/*.bats`, 40 files) for wrapper/CLI/E2E behaviour — including CLI contract tests and robot-output tests.
- **Snapshot tests** with a bless workflow (`BLESS=1`) for TUI golden output.
- **Property tests** (`proptest`, plus a `proptest-regressions/` directory — seeds for found failures are committed, which is the right habit).
- **Fuzz targets** in `fuzz/`.
- **Benchmarks** with criterion (`benches/`).
- **Robot-mode determinism** as an explicit design goal ("deterministic snapshot-friendly rendering").

The **`proptest-regressions`** directory is a small but telling practice: a property-test failure becomes a permanent seed. Adopt for our `mathx` and estimator invariants.

---

## 11. What is documented but not wired in

pt's own README/CHANGELOG note that some capabilities are **library-only** — implemented and tested but not connected to the main command path. Examples indicated in the CHANGELOG/specs: several of the advanced `decision/` modules (DRO, CVaR, contextual bandits, fleet FDR) exist as libraries with their own tests. This is not a criticism; it is a fact that must be honoured when reading the repo: **a module existing does not mean a running system uses it.**

This is directly relevant to our reference-audit discipline (the conversation's original instruction was to distinguish exactly this). Rule: for every capability we cite from either repo, state whether it is on the main execution path.

---

## 12. Summary classification

| Idea from pt | Class | Destination |
|---|---|---|
| `ActionFeasibility` with recorded disable reasons | Adopt | action-authorization §4 |
| Explicit `NoFeasibleActions` outcome | Adopt | decision record |
| Rationale travelling with the decision | Adopt | persistence §2 |
| Blast-radius concept (what an action disturbs) | Adopt (concept) | provider contract |
| `confidence = evidence_completeness` | Adopt | attribution §7 |
| Identity tuple + start-time anchor | Adapt | attribution §2 |
| Revalidation immediately before action | Adopt | action-authorization §4 |
| JSON lock with holder identity + TTL | Adopt | persistence, SI-8 |
| `cmdline_sha256` not raw cmdline | Adopt | security-and-privacy §4 |
| Protection rules with human-readable reasons | Adopt | process-protection |
| Two protection strengths (never / human review) | Adapt | process-protection §2 |
| Built-in session-critical rule set | Adopt | process-protection §4 |
| `PROC_PIDTBSDINFO` for exact identity | Adopt | macos-integration §4 |
| "Do not rely on private Darwin structs" | Adopt | macos-integration §2 |
| "Unavailable is never zero" framing | Adopt (wording) | SI-4, telemetry |
| Shadow reconciliation with miss threshold | Adopt | calibration |
| Specs directory discipline | Adopt | this docs tree |
| proptest-regressions / BATS / fuzz / snapshot | Adopt | test-strategy |
| Utilization-threshold pressure model | **Reject** | wrong model for macOS |
| Bayesian posterior as the decision core | **Reject for v1** | ADR-005 |
| BOCPD/Kalman/IMM/HSMM/Hawkes/EVT/copulas/DRO/CVaR/bandits | **Investigate only** | not justified without a measured gain |
| Kill / freeze / renice / cgroup-throttle / quarantine | **Reject** | ADR-012 |

---

## 13. License note

MIT License. Ideas and patterns are read and re-expressed in prose; no source is copied into Memory Pilot.
