# SCN-005 acceptance — "What is eating my RAM?"

Command: `mempilot report --last 2h`

## Setup

- 2 h of steady state: one 3.4 GB Docker daemon (flat), one 2.1 GB IDE (flat), a browser with episodic growth, two agent sessions with moderate growth.

## Assertions

| # | Assertion |
|---|---|
| E1 | Ranking is by **growth over the window**, not absolute RSS |
| E2 | Docker (3.4 GB, +0 MB) appears under "large but stable", never in the growth ranking |
| E3 | Every growth line states bytes and the window |
| E4 | The coverage footnote is present when process sums exceed the system delta |
| E5 | The report renders in < 300 ms for 2 h of data |
| E6 | `--json` returns the same numbers with `v: 1` and full coverage/confidence fields |
| E7 | No line contains "leak" or a recommendation to quit an app |

## Failure conditions

- A flat process in the growth list.
- Missing coverage disclaimer when applicable.
- Human and JSON outputs disagree.
