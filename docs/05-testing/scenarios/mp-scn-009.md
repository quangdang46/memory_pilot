# SCN-009 — Orphan detection, detect-only

Trace: `orphan-synthetic` — a parent spawns children, then exits; the children continue holding memory.

## Assertions

| # | Assertion |
|---|---|
| J1 | After the parent identity is unresolvable (twice, ≥ 60 s apart), children appear in the orphan candidate list with the full evidence set (§3.3 of `protection-actions.md`) |
| J2 | A long-running detached build (`nohup`-style, same tree shape) is **also** listed — but with a lower confidence and a stated alternative ("possibly intentional detach") |
| J3 | No termination occurs in v1 config (T3 act off) |
| J4 | The recommendation names the children, the evidence, and the one-command user action |
| J5 | An intentionally detached process that later does I/O is **removed** from the candidate list (it is working) |

## Failure conditions

- Terminating anything in this scenario (wrong tier for v1).
- Claiming a detached build is definitely an orphan.
- Keeping a process on the list after it shows activity.
