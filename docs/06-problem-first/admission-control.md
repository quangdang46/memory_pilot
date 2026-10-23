# Admission Control — the T2 mechanism

**Status:** Draft
**Date:** 2026-10-23
**Why this document exists:** T2 is the only mechanism that prevents pressure rather than observing or reporting it, without being able to destroy work. It is therefore the product's primary lever and deserves its own specification.

---

## 1. What it does

Before a workload starts, decide whether the machine can hold it.

```
launch request → estimate → admit | admit+warn | defer
```

Nothing already running is touched. Nothing is killed. The worst outcome is that a *request* is delayed, which is reversible by running it again.

---

## 2. Launch points

An admission decision can only be made at a point that sees the request. Three kinds, in order of reliability:

| Point | How | Coverage | User setup |
|---|---|---|---|
| `mp run <cmd> [args…]` | explicit wrapper | every launch through it | none — it is just a command |
| Shell function named for a common command | `claude() { mp run claude "$@"; }` | interactive shells sourcing the profile | one line in a profile |
| Terminal profile spawn hook | template's `spawn_command` | windows/tabs created by that profile | config edit |

**Design rule:** `mp run` is the canonical interface. Anything else is sugar that must expand to the same call, so there is exactly one implementation of the decision.

### 2.1 Coverage limit, stated in the product

If a launch does not pass through a point, **no admission decision is possible.** This is not a defect to be worked around; it is the boundary of what a user-space daemon can do. It must appear in:

- the install message ("sessions started outside `mp run` cannot be gated"),
- `mp doctor` ("admission coverage: 4 of 7 launches today went through a managed point"),
- `mp status` when relevant.

The product's credibility rests on not pretending otherwise.

---

## 3. Estimation

### 3.1 What we can know before a launch

| Input | Source | Reliability |
|---|---|---|
| current headroom | collector | high |
| current growth rate | forecaster | high (with confidence) |
| typical footprint of this class | history keyed by command + project path | **medium** — accumulates over time |
| typical footprint globally | history | medium |
| expected footprint of *this specific* command | not knowable | — do not pretend |

### 3.2 Estimator

```
class_key   = (executable basename, project root if determinable)
samples     = historical peak footprints for class_key
p50, p90    = quantiles (fallback: class_key → global)
budget      = headroom_now − safety_reserve
```

`safety_reserve` starts at 15 % of RAM and is tuned by measurement.

**Minimum sample rule:** a class needs ≥ 5 historical sessions before its own p50/p90 are used. Below that, fall back to global; below global's minimum, fall back to `unknown` and admit with a warning.

This prevents the worst admission bug: confidently refusing a lightweight command because a heavy one shares its basename.

### 3.3 Guards against bad estimates

| Guard | Behavior |
|---|---|
| No history at all | admit + warn (`estimate: unknown`) |
| Forecast not `actionable` | use headroom only, not the forecast |
| Headroom metric `unavailable` | **admit** — never block on missing data |
| Class baseline older than 30 days | discard, fall back to global |
| Project path not determinable | use global baseline |

**Rule: uncertainty never causes a refusal.** A launcher that blocks on ignorance is indistinguishable from a broken launcher.

### 3.4 Unknown estimate at high severity

The rule above holds even at L4/L5. An `unknown` class estimate does not become a reason to defer just because the system is already under pressure — that would punish the exact moment (many new short-lived tool calls) where blocking on ignorance is most damaging. Instead, severity changes what accompanies the admit, not whether it happens:

| Severity | `estimate: unknown` behavior |
|---|---|
| L1–L2 | `ADMIT_WITH_WARNING`, warning is informational |
| L3 | `ADMIT_WITH_WARNING`, warning states current headroom and the trend explicitly |
| L4–L5 | `ADMIT_WITH_WARNING`, warning is the most prominent line in the output, and the event is flagged for calibration review regardless of outcome |

The only thing that can turn an unknown-estimate launch into `DEFER` is headroom itself failing a known-estimate guard (§3.3) on a *different* basis — e.g., current headroom already below the critical floor from the collector, independent of the class estimate. Unknown never substitutes for "known to be too large."

---

## 4. The decision

```
if estimate == unknown:                    ADMIT_WITH_WARNING
elif p50 > budget:                         DEFER
elif p90 > budget:                         ADMIT_WITH_WARNING
else:                                      ADMIT
```

### 4.1 What DEFER must print

A deferral is only acceptable if the user can act on it in one step.

```
mp: deferring launch — this machine does not have room right now

  headroom      480 MB
  this class    ~1.1 GB p50 / 1.8 GB p90  (n=23 sessions, this project)
  trend         +140 MB/min over the last 6 min; critical in ~9 min
  cause         aggregate demand — 4 sessions growing, no single outlier

  options:
    wait             retry automatically in ~10 min
    mp run --force   start anyway (recorded; may push the system to critical)
    close session    iTerm2 tab 3 (1.4 GB, idle 47 min)
                     resume later:  claude --resume 171faef2-77ad-…   [from adapter]
```

The last line appears **only when an adapter has verified the resume mechanism**. Without an adapter, that line is absent and the deferral must not imply recoverability.

### 4.2 `--force`

Always available, always recorded. It is not a failure of the product; it is the user overriding an estimate they can see. The record matters because it feeds estimate quality ("we said 480 MB headroom and the user forced; pressure hit critical 4 min later").

### 4.3 Auto-retry

`wait` schedules a re-evaluation. If conditions improve, the launch proceeds **only if the user asked for that** (`--wait`); otherwise the user re-runs. Silent deferred launches that fire 20 minutes later are surprising and wrong.

---

## 5. What T2 must never become

| Anti-pattern | Why it is wrong |
|---|---|
| A hard cap on session count | a session's cost varies by 10× by workload; a count is the wrong unit |
| Blocking when the estimate is missing | punishes ignorance, breaks the tool |
| Silently succeeding after a long wait | the user loses track of what they asked for |
| Killing/stopping an existing session to make room | that is T3 territory and off by default |
| Refusing to start the *first* session on an empty machine | an empty machine has nothing to protect |
| Charging admission decisions against a budget shared with relief actions | different mechanisms, different limits |

---

## 6. Interaction with severity

| Severity | T2 behavior |
|---|---|
| L1 | inactive (no reason to gate) |
| L2 | active; defer only when `p50 > budget` |
| L3 | active; also defer when `p90 > budget` **and** the trend is worsening |
| L4 | active; defer more readily; warnings become prominent |
| L5 | active; the advisory is the primary output |

T2 is **not** disabled at any severity. It is the one action that remains safe and useful through the whole ladder.

---

## 7. Verification

| Question | How it is answered |
|---|---|
| Did the deferral prevent a crisis? | **unobservable** — counterfactual. Recorded as `prevented (counterfactual)`, never as a measured effect |
| Was the estimate good? | compare predicted p50/p90 against the session's actual peak once it ends; maintain a calibration record |
| Did we block something that would have fit? | same comparison; a rising false-defer rate raises the threshold automatically |
| Did the user override often? | `--force` rate is a direct measure of estimate quality |

**Calibration loop:** every admitted session's actual peak is a labeled sample. Every deferral that the user forced and that did not lead to a crisis is a *false defer*. Both feed the estimator. This is the mechanism by which admission control gets better instead of staying a guess.

---

## 8. Tests

| ID | Scenario | Asserts |
|---|---|---|
| T-AD-1 | headroom ample | admits without warning |
| T-AD-2 | p50 > budget | defers; message contains numbers and options |
| T-AD-3 | p90 > budget ≥ p50 | admits with warning |
| T-AD-4 | no history | admits with `estimate: unknown`, never blocks |
| T-AD-5 | headroom `unavailable` | admits (never blocks on missing data) |
| T-AD-6 | `--force` | launches, records override |
| T-AD-7 | `--wait` | retries, then launches when conditions improve |
| T-AD-8 | class history n < 5 | falls back to global baseline |
| T-AD-9 | deferral → session later measured lighter than predicted | false-defer recorded, threshold adjusted |
| T-AD-10 | adapter absent | deferral text contains **no** resume claim |

---

## 9. Open questions

- **Q-13:** Is `project root` determinable from the launch request reliably enough to key history on it? (cwd at launch is the best signal; `--cwd` flag or shell state.)
- **Q-14:** Should the safety reserve adapt to observed compression/swap behavior rather than being a fixed 15 %? Likely yes after measurement.
- **Q-15:** Does `claude --resume` need the terminal to be the same tab? If not, a resume recommendation is much more useful. (Verify on the target machine.)
