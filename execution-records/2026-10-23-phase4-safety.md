# Execution record — Phase 4 policy/executor/safety (2026-10-23)

- **Policy (3r5)**: `internal/policy` — expected-benefit candidates from the
  closed allowlist; observe ⇒ advisory+self-relief only; L2 requires canary+,
  leak-suspicion verdict, AND an owned, pre-authorized target token
  (no owned target ⇒ no L2 candidate exists — ST-22 by construction).
  Planner orders by benefit per unit cost (L2 weighted 8×, deliberately scarce).
- **Executor + actionguard (fe5)**: `internal/executor` — serialized execution;
  revalidates mode, staleness (ST-6), TOCTOU (ST-7), rate limits (ST-8,
  StopOwned capped 2/h + 15min gap), 3-strike breaker; refuses malformed
  foreign targets (ST-22 unit test: "5012", "owned:5012", injection strings).
  ST-5/6/7/8/22/24 unit tests green.
- **Single signal call site (ST-1)**: `internal/executor/stopowned_darwin.go`
  — SIGTERM to our OWN child's process group only; the ST-1 gate script now
  enforces "exactly one syscall.Kill in exactly that file" and passes.
- **Providers (o1x/hk0/9gj/pjy/u6m)**: T1 trim + rotate; T2 defer; T4
  cooperative FRAMEWORK with no default adapter (Check errors when none
  registered); L2 documented as executor path, not a provider; T5 advisories
  with stated strength and no reversibility promise without verified adapter.
- **T3**: detect+recommend only — no executor path (default case refuses
  "kind outside allowlist").
- **Verifier (ST-10)**: unverified by default; verdict only after window.
- **ST-19/20 (ung)**: monotone degradation ladder tested (budgets only
  shrink); hard-limit exit at RSS>400MB.
- **Forbidden scenario (ST-22, integration)**: `internal/pipeline` —
  enforce-mode pipeline against synthetic Critical with foreign 4GB workload:
  full escalation recorded, no L2 candidate planned, only self-actions
  possible, decision+transition+advisory records emitted.
- **Structured logging (7eb)**: mempilotd now runs the full pipeline per tick
  and appends pressure_transition / decision / advisory records — every
  decision reconstructible from the ledger alone.

Caveats recorded honestly: the full-daemon ST-22 (launchd process-level, real
signal-trap child) is covered here at the integration level; the FI-1..14
fault-injection matrix is partially covered (ST-6/7/8/9-style revalidation +
breaker + demotion tests). Process-level FI expansion and the 72h soak remain
open calendar work (beads qh9, j7b stay open until those runs exist).
