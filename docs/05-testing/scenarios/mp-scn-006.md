# SCN-006 acceptance — Planner must act when a valid action exists

**Why this scenario exists.** `NoAction` is always available and always cheap. A planner that minimizes cost will therefore choose it *every time* unless the cost model explicitly values expected relief. That failure mode is invisible to a test suite that only checks "we did not act when we shouldn't have" — the whole suite passes while the product never relieves anything.

This is the **positive** counterpart to MP-SCN-002 B6 (which asserts we refuse when nothing safe exists).

Trace: `planner-positive` (synthetic, with a stub provider).

---

## Setup

- Pressure state: `Prevent`, rising; `eta_lower_bound_s` = 240 s → **`actionable == true`**.
- Mode: `canary` (provider promoted after measured canary evidence).
- One registered provider `stub-reclaim` with:
  - `StatusProven` (effect measured in a prior experiment),
  - `Expected.EffectRange = [80 MB, 400 MB]`,
  - all preconditions satisfiable in the fixture,
  - `no_protected_group_affected == true`,
  - rate limits fresh (no recent invocations),
  - breaker closed.
- Rationale from the spec (`action-authorization.md` §7.2): the expected relief (≥ 80 MB against a 240 s time-to-critical at ~1 MB/s) exceeds the action's cost by a wide margin.

## Assertions

| # | Assertion |
|---|---|
| F1 | `Decision.chosen != null` and names `RequestCooperativeRelease` with `provider_id == "stub-reclaim"` |
| F2 | `Decision.no_action_reason == null` |
| F3 | An `ActionResult` is produced with `outcome == "succeeded"` |
| F4 | Verification is scheduled; after the window, `verdict ∈ {effective, partial}` (the stub returns a real measured delta) |
| F5 | If the same fixture is replayed with the provider's `EffectRange` set to `[0, 0]` (i.e. **no** expected benefit), the planner chooses `NoAction` with `no_action_reason == "no_safe_relief"` — proving the choice is benefit-driven, not "always act" |
| F6 | If the same fixture is replayed with `mode == "observe"`, the planner chooses `NoAction` with `reason == "mode_insufficient"` — proving the mode gate still binds |

## The critical distinction the test encodes

F1 and F5 together are the actual test. F1 alone could be passed by a planner that always acts; F5 alone could be passed by a planner that never acts. **Only the pair proves the planner is doing expected-benefit reasoning.**

## Failure conditions

- The planner never selects the provider even when benefit is unambiguous (**the silent-no-op bug**).
- The planner selects the provider when expected benefit is zero (over-eager).
- The planner selects it in `observe` mode (mode gate broken).
- `verdict` is reported without a measurement.

## Scope — what this test does and does not prove

| It proves | It does **not** prove |
|---|---|
| The policy engine and planner reason about expected benefit rather than defaulting to `NoAction` | That any real macOS relief mechanism exists |
| The mode gate (`observe` blocks the action) is enforced | That Memory Pilot can free another app's memory |
| A chosen action's outcome and verification are recorded separately | That v1 ships working relief |

**Read this scenario as a unit test of the decision layer**, not as product readiness. `docs/adr/006-relief-actions.md` records that no cross-application provider is enabled in v1, and `action-authorization.md` §2.2 states the rule that a provider enters the active set only after design + measurement + safety verification. Passing SCN-006 moves none of those three.

The distinction matters because a green planner test is easy to misread as "the relief feature works". It works as *logic*; there is nothing yet for it to call.

## Implementation note

This scenario requires a **stub provider** in the test harness that declares a spec and returns a synthetic measured effect. It does not require a real relief mechanism — which is deliberate: the planner can and must be tested before any real provider exists, and shipping the planner untested until the riskiest phase would defer the bug to the worst moment.

See `test-strategy.md` §2.3 (replay) and `action-authorization.md` §7.2.
