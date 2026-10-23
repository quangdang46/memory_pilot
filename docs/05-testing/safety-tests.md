# MP-TEST-SAFETY — Safety Test Suite

**Status:** Draft — **hard release gate; never waived**
**Version:** 0.1.0

These tests exist to prove the safety invariants (`../04-safety/safety-invariants.md`) still hold after every change. They are not feature tests. Breaking one is a release blocker regardless of what else works.

---

## 1. Static / build-time

### ST-1 — No signaling capability in the binary

Scan non-test source and the compiled dependency graph for:

```
syscall.Kill, unix.Kill, syscall.Syscall(.*SYS_KILL), SIGSTOP, SIGCONT, SIGKILL, SIGTERM
proc_terminate, task_terminate, task_suspend, launchctl kill, pkill, killall
os.Process.Kill, exec.Command("kill"|...)
```

Allowlist: the daemon may send `SIGTERM` **to itself** during shutdown — that is `os.Interrupt` handling, not `Process.Kill`. The scan must distinguish self-signaling by construction, not by file path.

**Implementation:** a `go test` that greps the AST of every package (not string grep, to reduce false negatives from obfuscation like `syscall.Syscall(37, ...)`), plus a check of `go list -deps` for process-control packages.

### ST-2 — Sealed action type

A golden test lists every variant of `internal/action.Action`; adding a variant fails the test until the golden file and `../04-safety/action-authorization.md` are updated together.

### ST-3 — Layering

`go list -deps` assertions:

- `internal/{pressure,forecast,attribution,leaksuspect,policy}` must not depend on `internal/{providers,executor,platform}`;
- `internal/platform` must not depend on anything above it;
- only `internal/actionguard` may import `os/exec`.

### ST-4 — No network

Assert the binary's dependency graph contains no `net` usage beyond `net` types required by the Unix socket path (Go's `net.UnixConn` lives in `net`; the check is on actual dial/listen-to-TCP calls). A targeted AST check for `net.Dial("tcp"`, `net.Listen("tcp"`, `http.`, `grpc.`.

---

## 2. Type-level / runtime

### ST-5 — Executor refuses out-of-mode actions

Construct a decision requiring `canary` while the daemon is in `observe`; assert `refused(mode)` and that the provider was never invoked (spy provider).

### ST-6 — Executor refuses stale decisions

Decision older than `max_age` ⇒ refused, provider not invoked.

### ST-7 — Precondition revalidation (TOCTOU)

Provider whose precondition flips between plan and execute ⇒ refused, no side effect.

### ST-8 — Rate limit and breaker across restart

Exhaust the hourly budget, restart the daemon, assert the budget is still exhausted (persisted counters).

### ST-9 — Fault demotes mode

Inject a worker panic ⇒ mode becomes `observe` ⇒ subsequent decisions refused.

### ST-10 — Unverified by default

Provider that returns success but produces no measurable effect ⇒ verdict `ineffective`, backoff applied.

---

## 3. Filesystem containment

### ST-11 — Path escape

Attempt ledger/store writes to `../`, absolute paths outside the data root, symlink-to-outside, and `..` via a crafted config. All must be rejected.

### ST-12 — Rotation containment

Create non-mempilot files in the data dir; rotate; assert they are untouched.

### ST-13 — No deletes of user data

AST/grep gate for `os.Remove`, `os.RemoveAll`, `unlink` in non-test code outside `internal/store`'s own segment logic; and a runtime test that the only files removed are matching `ledger-*.jsonl` segments older than retention.

---

## 4. Evidence-integrity

### ST-14 — Missing evidence never recovers

Drive state to `Prevent`; kill the telemetry source; run 100 ticks; assert state never returns to `Normal` and an `observation_gap`/`unavailable` record exists.

### ST-15 — Partial coverage never recovers

Same with partial process coverage.

### ST-16 — Schema rejects bare claims

Feed schemas `forecast` without `confidence`, `attribution` without `coverage`, `leak_suspicion` without `alternative_explanations`; all must be rejected.

### ST-17 — Renderer cannot emit forbidden phrasing

Corpus test: render every classification with adversarial data; assert the output never matches `/\b(is|are|has) (a )?memory leak/i` and never contains a bare `leaking`.

### ST-18 — Unknown is surfaced

All sources unavailable ⇒ CLI shows `Unknown` (not `Normal`), exit code documented.

---

## 5. Self-protection

### ST-19 — Monotonic degradation

Drive an escalation sequence; assert `effective_work()` is non-increasing.

### ST-20 — Hard-limit exit

Force RSS above the hard limit (test hook) ⇒ exit code 10, clean shutdown record written, no action attempted in the final cycle.

### ST-21 — Single instance

Two instances ⇒ second exits 4, first unaffected.

---

## 6. End-to-end safety scenario

### ST-22 — The forbidden scenario

Run the full daemon in `enforce` mode against a synthetic workload that induces `Critical` pressure, with a fake "agent" process holding 4 GB.

Assert:
1. no signal was sent to any PID (verified by the workload process installing a signal handler that records every signal; it must receive none);
2. the ledger contains an advisory naming the group;
3. if no provider is available, no action was attempted;
4. the daemon's own RSS stayed within budget throughout;
5. the state machine recorded the full escalation.

This is the flagship safety test. If it ever fails, the product has lost its reason to exist.

---

## 7. Checklist for reviewers

Before approving a PR that touches actions, platform, or store:

- [ ] Did the action type grow? If so, is there a spec + ADR?
- [ ] Does any new code path reach a syscall that could affect another process?
- [ ] Are new caches bounded?
- [ ] Does any new output path bypass the schema (e.g. hand-built JSON)?
- [ ] Do the safety tests still pass **unmodified**? (Modifying them requires justification in the PR.)
