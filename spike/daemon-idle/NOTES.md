# S-4: idle cost of an empty daemon — baseline numbers

Target Mac mini M4 (16 GB), darwin 25.4.0, go1.26.4, CGO_ENABLED=0.
Program (`main.go`): 5 s ticker + JSONL writer (no collectors, no policy),
self-measures RSS (`proc_pidinfo PROC_PIDTASKINFO`), heap (`ReadMemStats`),
goroutines, FDs (`proc_pidinfo PROC_PIDLISTFDS`). Runs 300 s default;
`/usr/bin/time -l` wraps for kernel-level rusage. Reproduce:
`CGO_ENABLED=0 go build -o daemonidle main.go && /usr/bin/time -l ./daemonidle 300s`.

## Results

| Metric | Value | Budget (performance-budget.md) | Verdict |
|---|---|---|---|
| RSS (steady, 5 min, 60 samples) | 3.0–3.7 MB (max RSS 4.9 MB) | idle ≤ 60 MB | PASS with 16× headroom |
| CPU user+sys over 307 s | 0.01 s total ⇒ ≈0.003 % of one core | idle avg ≤ 0.3 % | PASS with 100× headroom |
| Goroutines | 1 | — | — |
| FDs | 10 (runtime + jsonl file) | — | — |
| Heap inuse | 0.6–0.9 MB | — | — |
| Peak footprint (rusage) | 3.3–3.5 MB | — | — |

Raw JSONL: `idle.jsonl` (60 samples). The ticker+JSONL loop itself is noise
against the MP-REQ-070 budgets; the real idle cost will be the collectors
(S-5 shows the sampling cost is ~1.5 ms per N=600 cycle at 5 s cadence ⇒
≈0.03 % of one core), so the combined idle+sampling budget remains dominated
by Go runtime RSS, well under 60 MB.

## Exit items

- P1SELF idle budgets (RSS 60 MB, CPU 0.3 %) now have a denominator:
  empty-daemon baseline ≈ 3–5 MB RSS / ≈0.003 % CPU.
- ADR-008 (self-monitor) evidence: baseline recorded on the target machine.
