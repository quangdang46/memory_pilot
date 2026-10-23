# SCN-007 — Admission control admits, warns, and defers

Trace: `admission-*` (synthetic; a scripted sequence of launch requests against recorded headroom).

## Setup

- Headroom 600 MB; class baseline for the requested command: p50 1.1 GB, p90 1.8 GB.
- Request 1 arrives with headroom 4 GB → admit.
- Request 2 arrives with headroom 600 MB → **defer** (p50 1.1 GB > 600 MB budget).
- Request 3 is a lightweight class (p50 80 MB) → admit (with the numbers shown).
- The user forces request 2 with `--force`.

## Assertions

| # | Assertion |
|---|---|
| H1 | Request 1 admits without warning |
| H2 | Request 2 defers, and the deferral prints: headroom, class p50/p90 with sample count, trend, cause class, and **all three options** (wait / --force / close suggestion) |
| H3 | Request 3 admits; the message shows its own class numbers, not request 2's |
| H4 | `--force` launches and records an override with the estimate that was overridden |
| H5 | If headroom is `unavailable`, the request admits with `estimate: unknown` — never blocks |
| H6 | If the launch bypasses the managed point, no decision is recorded and no error is raised (coverage limit, not a bug) |
| H7 | The deferred-then-forced launch's actual peak is compared against the estimate and a false-defer record is written if it would have fit |

## Failure conditions

- Deferral without numbers or options.
- Blocking on missing data.
- Silent admission after `--wait` (the user must have asked for retry).
