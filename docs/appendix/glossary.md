# Glossary

Terms used precisely throughout the specs. Where a term has a colloquial meaning that differs, that is called out.

| Term | Definition |
|---|---|
| **Memory pressure** | macOS's own notion of memory scarcity, influenced by free RAM, swap rate, wired memory and file cache. **Not** "percent of RAM used". |
| **Kernel pressure level** | The value of `kern.memorystatus_vm_pressure_level`: dispatch masks **1 = normal, 2 = warn, 4 = critical**. Anything else is treated as unavailable. |
| **Headroom** | The bytes we forecast depletion of. Starts as free pages; may later include vetted reclaimable memory. Never includes swap. |
| **Compressed memory** | RAM the compressor holds, representing a larger logical amount. Physical bytes = `compressor_page_count × page_size`. Invisible to per-process attribution. |
| **Swap** | Disk-backed store. `swap_used` is a **latency** signal; swap total is **not** memory capacity. |
| **Resident memory (RSS)** | Pages resident in a process's address space. Counts shared pages per process; excludes compressed pages. Deltas are meaningful; sums are not. |
| **Physical footprint** | Apple's per-process accounting (Activity Monitor's "Memory"). Closer to "what this app costs"; requires an extra per-PID call. |
| **Identity** | `(pid, start_sec, start_usec)` — a stable key across PID reuse. Never use a bare PID as an identity. |
| **Workload group** | Processes sharing a session anchor (terminal/IDE/agent) via parent lineage. The unit of attribution and of policy. |
| **Contribution / delta** | Change in a process's or group's memory over a window. The unit of attribution ranking. |
| **Attribution** | Assigning growth to groups with coverage and confidence. Never "blame" in the sense of fault. |
| **Classification bucket** | One of `workload_growth`, `cache_or_retrievable`, `subprocess_accumulation`, `leak_suspicion`, `unknown`. |
| **Leak suspicion** | A classification meaning "growth unexplained by observed activity, with alternatives acknowledged". **Never** a claim that a leak exists. |
| **Burst** | A step change in rate, detected via median/MAD over rate history. Distinct from a sustained trend. |
| **Trend** | Sustained rate, positive (consuming) or negative (releasing). |
| **Forecast** | ETA to a threshold, with interval and confidence. Never emitted without both. |
| **Calibration / coverage** | Empirical frequency with which the forecast interval contained the truth. Target 0.9. |
| **Confidence** | A heuristic index in [0,1] built from named terms (sample count, residual, stability, coverage). Not a probability; documented as heuristic. |
| **Fallback active** | The estimator does not have enough data to be trusted. Forecasts in this state are not actionable. |
| **Pressure state** | `normal`, `watch`, `prevent`, `critical`, `unknown` (wire/enum form, always lowercase). A pressure scale, not a disk scale. It is rendered capitalized in human output (`Normal`, `Watch`, …) but **never stored or compared in that form**. |
| **Observe mode** | Default policy mode: self-only actions + advisories. |
| **Canary mode** | Limited live trial of a provider with post-hoc comparison. |
| **Enforce mode** | Full allowlisted provider budget. Earned by evidence. |
| **Provider** | A named, registered mechanism that claims it can return memory. Has preconditions, expected effect, verification, and a status (`unproven` until measured). |
| **Capability degradation** | Reducing what the daemon does (detail, cadence, features) under fault or pressure. |
| **Confidence degradation** | Reducing what the daemon claims (quality flags, trustworthy=false) when data or calibration is weak. Independent of capability. |
| **Observation gap** | A period with no samples, explicitly recorded and never interpolated. |
| **Ledger** | Append-only JSONL record of samples (aggregated), events, decisions, actions, outcomes. |
| **Evidence** | The specific observations that support a conclusion, always attached to it. |
| **Invariant** | A property of the artifact that cannot be disabled by configuration (SI-1..SI-8). |
| **Forbidden action** | Any process signaling/suspension/termination. Absent from the code, not merely disabled. |
| **sbh** | `storage_ballast_helper`, a disk-pressure daemon used as a reference. |
| **pt** | `process_triage`, an evidence-based process classifier used as a reference. |

---

## Naming conventions (normative)

Four distinct namespaces share similar words. Keeping them apart is what makes the contradiction pass mechanically checkable.

| Concept | Wire / code form | Human output form | Notes |
|---|---|---|---|
| Pressure state | `normal` `watch` `prevent` `critical` `unknown` | `Normal` `Watch` `Prevent` `Critical` `Unknown` | lowercase everywhere except display |
| Policy mode | `observe` `canary` `enforce` | `observe` `canary` `enforce` (lowercase) | modes are not capitalized even in prose |
| macOS kernel level | `1` `2` `4` (masks) or `normal` `warn` `critical` | as returned | **`warn`**, never `warning` — matches the kernel's own vocabulary |
| Degradation step | `0`..`5` | `normal`, `reduced detail`, … | numeric on the wire; see `failure-recovery.md` §2 |

**Rules:**
1. Any JSON field containing a state name uses the lowercase wire form. Schema `enum` values are lowercase.
2. Human renderers capitalize. A test asserts the renderer never leaks lowercase into a sentence's first word and never stores the capitalized form.
3. The macOS kernel level is `warn`; our pressure state is `prevent` at the same severity. They are **different vocabularies for different things** — do not map the words, map the severity.

## Units

| Quantity | Unit | Note |
|---|---|---|
| Memory | bytes (base-2) | render as MB/GB with the unit stated |
| Rate | bytes/second | never "MB/min" in stored data |
| Time (math) | monotonic nanoseconds | |
| Time (display) | RFC3339 UTC | |
| Pressure score | 0..1 | internal ordering key, not a user-facing percentage |
| Confidence | 0..1 | heuristic index |

| **Long-running workload protection** | The product's objective for sessions that run for hours: detect anomalies, prevent new demand, recommend closures, and act only where proven safe. Distinct from leak detection (a sensor) and from the relief engine (a tiered actor). |
| **Abandonment confidence** | P(the process is not needed), one of three separate numbers in the relief engine. Drives the gate. |
| **Interruption risk** | What breaks if we are wrong about abandonment. Drives the default (recommend, not act). |
| **Expected memory relief** | Bytes we gain if we are right, with uncertainty. Drives priority, never permission. |
| **Managed launch point** | Any entry point that sees a launch request before it runs (`mp run`, shell function, terminal profile hook). The only place admission control can act. |
| **Orphan (verified)** | A process whose parent identity is unresolvable (twice, ≥ 60 s apart), whose tree ends at launchd, quiet for ≥ 10 min, not a service, and unclaimed by any adapter or session. A finding, not an action. |
