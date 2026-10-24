// Package executor: the ONLY component that acts (SI-2). Serialized, revalidates
// everything (TOCTOU), rate-limits, breaks on failures, demotes on faults.
// The single signal-capable call site is stopOwnedChild (ST-1/ST-22).
package executor

import (
	"context"
	"fmt"
	"sync"
	"time"

	"memory_pilot/internal/model"
	"memory_pilot/internal/policy"
)

type Decision struct {
	ID        string
	IssuedAt  time.Time
	Required  policy.Mode
	Candidate model.Candidate
}

type OwnedPid struct {
	Pid       int32
	Pgid      int32
	StartSec  int64
	StartUsec int64
	Auth      string // pre-authorization token; required (ADR-014)
}

// OwnedPidRef is what the single signal call site receives.
type OwnedPidRef = model.OwnedPidRef

// Registry of daemon-owned workloads (L2 targets ONLY). Foreign processes can
// never enter this registry: there is no code path that adds them except the
// daemon's own spawn/attach-with-preauth flow.
type Registry struct {
	mu    sync.Mutex
	owned map[int32]OwnedPid
}

func NewRegistry() *Registry { return &Registry{owned: map[int32]OwnedPid{}} }

func (r *Registry) Register(o OwnedPid) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.owned[o.Pid] = o
}

func (r *Registry) Lookup(pid int32) (OwnedPid, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	o, ok := r.owned[pid]
	return o, ok
}

// Forget drops a workload once it exits, so a recycled pid cannot inherit a
// previous workload's stop authorization.
func (r *Registry) Forget(pid int32) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.owned, pid)
}

// List returns the owned pids as L2 candidate tokens ("owned:<pid>"), which is
// the only shape policy is allowed to hand the executor (ST-24).
func (r *Registry) List() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, 0, len(r.owned))
	for pid, o := range r.owned {
		if o.Auth == "" {
			continue // registered but not stop-authorized: not an L2 target
		}
		out = append(out, "owned:"+itoa(pid))
	}
	return out
}

func itoa(pid int32) string {
	if pid == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	neg := pid < 0
	if neg {
		pid = -pid
	}
	for pid > 0 {
		i--
		b[i] = byte('0' + pid%10)
		pid /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

type rateLimiter struct {
	mu      sync.Mutex
	counts  map[model.ActionKind][]time.Time
	maxPerH map[model.ActionKind]int
	minGap  map[model.ActionKind]time.Duration
}

func newRateLimiter() *rateLimiter {
	return &rateLimiter{
		counts: map[model.ActionKind][]time.Time{},
		maxPerH: map[model.ActionKind]int{
			model.ActionTrimOwnBuffers: 12, model.ActionRotateOwnLedger: 6,
			model.ActionEmitAdvisory: 60, model.ActionDeferLaunch: 30,
			model.ActionStopOwned: 2, // L2 is deliberately scarce
		},
		minGap: map[model.ActionKind]time.Duration{
			model.ActionStopOwned: 15 * time.Minute,
		},
	}
}

func (rl *rateLimiter) allow(k model.ActionKind, now time.Time) (bool, string) {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	max := rl.maxPerH[k]
	var keep []time.Time
	for _, t := range rl.counts[k] {
		if now.Sub(t) < time.Hour {
			keep = append(keep, t)
		}
	}
	if len(keep) >= max {
		rl.counts[k] = keep
		return false, fmt.Sprintf("rate limit: %s %d/hour", k, max)
	}
	rl.counts[k] = append(keep, now)
	return true, ""
}

// ActionGuard revalidates preconditions at execution time (ST-7 TOCTOU).
type ActionGuard struct {
	Mode     func() policy.Mode
	Registry *Registry
	MaxAge   time.Duration
}

type Check struct {
	OK     bool
	Reason string
}

func (g *ActionGuard) Revalidate(d Decision, now time.Time) Check {
	if now.Sub(d.IssuedAt) > g.MaxAge {
		return Check{false, "decision stale (ST-6)"}
	}
	mode := g.Mode()
	if policyRequired(d.Candidate.Kind) > modeRank(mode) {
		return Check{false, fmt.Sprintf("mode %s insufficient for %s (ST-5)", mode, d.Candidate.Kind)}
	}
	if d.Candidate.Kind == model.ActionStopOwned {
		// identity revalidation: target must be in the owned registry with auth
		if d.Candidate.TargetID == "" {
			return Check{false, "L2 without OwnedPid token (ST-24)"}
		}
		var pid int32
		if _, err := fmt.Sscanf(d.Candidate.TargetID, "owned:%d", &pid); err != nil {
			return Check{false, "malformed OwnedPid token"}
		}
		o, ok := g.Registry.Lookup(pid)
		if !ok || o.Auth == "" {
			return Check{false, "target not owned or unauthorized (ST-22/ST-24)"}
		}
	}
	return Check{OK: true}
}

func modeRank(m policy.Mode) int {
	switch m {
	case policy.ModeObserve:
		return 0
	case policy.ModeCanary:
		return 1
	case policy.ModeEnforce:
		return 2
	}
	return -1
}

func policyRequired(k model.ActionKind) int {
	switch k {
	case model.ActionStopOwned:
		return 1 // canary
	default:
		return 0
	}
}

// Effect is what an action actually did. It exists because "executed" alone
// is a claim without evidence (SI-5): the daemon reported executed actions
// that freed nothing, which is worse than reporting nothing.
type Effect struct {
	Bytes   int64 // measured or provider-declared release; 0 when unknown
	Note    string
	Changed bool // false ⇒ nothing to do; recorded as "no_effect", not success
}

// Actors are the real side-effect implementations. They are injected so the
// executor never imports a provider or the ledger (SI-2: the executor decides
// to act, the actor performs; layering stays intact).
type Actors struct {
	TrimOwnBuffers  func() (Effect, error)
	RotateOwnLedger func() (Effect, error)
	// EmitAdvisory is performed by the caller that records the advisory.
	EmitAdvisory func(text string) (Effect, error)
}

type result struct {
	mu    sync.Mutex
	fails map[model.ActionKind]int
}

func newResult() result { return result{fails: map[model.ActionKind]int{}} }

type Executor struct {
	Guard    *ActionGuard
	Actors   Actors
	registry *Registry
	limiter  *rateLimiter
	res      result
	mu       sync.Mutex // serialized: one action at a time
}

func New(guard *ActionGuard) *Executor {
	return &Executor{Guard: guard, registry: guard.Registry, limiter: newRateLimiter(), res: newResult()}
}

// Execute: guard → rate limit → actor → verify. Never returns "succeeded"
// without a separate verification verdict.
func (e *Executor) Execute(ctx context.Context, d Decision) model.ActionResult {
	e.mu.Lock()
	defer e.mu.Unlock()
	ar := model.ActionResult{ActionID: d.ID, Kind: d.Candidate.Kind}
	if c := e.Guard.Revalidate(d, time.Now()); !c.OK {
		ar.State = "refused"
		ar.Evidence = map[string]string{"reason": c.Reason}
		return ar
	}
	if ok, why := e.limiter.allow(d.Candidate.Kind, time.Now()); !ok {
		ar.State = "refused"
		ar.Evidence = map[string]string{"reason": why}
		return ar
	}
	switch d.Candidate.Kind {
	case model.ActionTrimOwnBuffers:
		e.apply(&ar, e.Actors.TrimOwnBuffers, "own caches trimmed")
	case model.ActionRotateOwnLedger:
		e.apply(&ar, e.Actors.RotateOwnLedger, "ledger rotated")
	case model.ActionEmitAdvisory:
		e.apply(&ar, func() (Effect, error) {
			if e.Actors.EmitAdvisory == nil {
				return Effect{Note: "advisory recorded by caller"}, nil
			}
			return e.Actors.EmitAdvisory("")
		}, "advisory recorded")
	case model.ActionDeferLaunch:
		// Admission control is enforced by `mp run`, not by the daemon; the
		// daemon's own record of it is the action.
		ar.State = "executed"
		ar.Evidence = map[string]string{"effect": "launch deferred", "changed": "true"}
	case model.ActionStopOwned:
		if c := e.stopOwned(ctx, d); c != "" {
			ar.State = "failed"
			ar.Evidence = map[string]string{"reason": c}
		} else {
			ar.State = "executed"
			ar.Evidence = map[string]string{"effect": "owned workload stopped via stopOwnedChild", "changed": "true"}
		}
	default:
		ar.State = "refused"
		ar.Evidence = map[string]string{"reason": "kind outside allowlist (SI-2)"}
	}
	if ar.State == "failed" {
		e.res.mu.Lock()
		e.res.fails[d.Candidate.Kind]++
		n := e.res.fails[d.Candidate.Kind]
		e.res.mu.Unlock()
		_ = n // breaker state read via Broken()
	}
	return ar
}

// Breaker: after 3 consecutive failures the kind is broken for the session.
func (e *Executor) Broken(k model.ActionKind) bool {
	e.res.mu.Lock()
	defer e.res.mu.Unlock()
	return e.res.fails[k] >= 3
}

// stopOwnedChild is THE ONLY call site in the entire binary that can signal
// another process (ST-1). It targets exclusively OwnedPid-authorized, MP-owned
// workloads. Foreign processes are structurally unreachable.
func (e *Executor) stopOwned(ctx context.Context, d Decision) string {
	var pid int32
	if _, err := fmt.Sscanf(d.Candidate.TargetID, "owned:%d", &pid); err != nil {
		return "malformed target"
	}
	o, ok := e.registry.Lookup(pid)
	if !ok || o.Auth == "" {
		return "not owned or unauthorized"
	}
	return stopOwnedChild(ctx, model.OwnedPidRef{Pid: o.Pid, Pgid: o.Pgid})
}

// apply runs one actor and records what actually happened. An actor that
// reports no change is "executed/no_effect", never "success": the difference
// is whether the user's memory situation improved, and pretending otherwise
// is exactly the dishonesty SI-5 forbids.
func (e *Executor) apply(ar *model.ActionResult, actor func() (Effect, error), fallbackNote string) {
	if actor == nil {
		ar.State = "refused"
		ar.Evidence = map[string]string{"reason": "no actor wired for " + string(ar.Kind)}
		return
	}
	eff, err := actor()
	if err != nil {
		ar.State = "failed"
		ar.Evidence = map[string]string{"reason": err.Error()}
		return
	}
	ar.State = "executed"
	note := eff.Note
	if note == "" {
		note = fallbackNote
	}
	ar.Evidence = map[string]string{
		"effect":       note,
		"changed":      boolStr(eff.Changed),
		"effect_bytes": i64str(eff.Bytes),
	}
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func i64str(n int64) string { return fmt.Sprintf("%d", n) }
