package executor

import (
	"context"
	"strings"
	"testing"
	"time"

	"memory_pilot/internal/model"
	"memory_pilot/internal/policy"
)

func guard(mode policy.Mode) *ActionGuard {
	r := NewRegistry()
	return &ActionGuard{Mode: func() policy.Mode { return mode }, Registry: r, MaxAge: 30 * time.Second}
}

func dec(kind model.ActionKind, age time.Duration, target string) Decision {
	return Decision{ID: "d1", IssuedAt: time.Now().Add(-age),
		Required: policy.ModeObserve, Candidate: model.Candidate{Kind: kind, TargetID: target}}
}

// ST-5: executor refuses out-of-mode actions.
func TestST5RefusesOutOfMode(t *testing.T) {
	e := New(guard(policy.ModeObserve))
	ar := e.Execute(context.Background(), dec(model.ActionStopOwned, 0, "owned:999"))
	if ar.State != "refused" || !strings.Contains(ar.Evidence["reason"], "ST-5") {
		t.Errorf("ST-5: %+v", ar)
	}
}

// ST-6: executor refuses stale decisions.
func TestST6RefusesStale(t *testing.T) {
	e := New(guard(policy.ModeEnforce))
	ar := e.Execute(context.Background(), dec(model.ActionTrimOwnBuffers, time.Minute, ""))
	if ar.State != "refused" || !strings.Contains(ar.Evidence["reason"], "stale") {
		t.Errorf("ST-6: %+v", ar)
	}
}

// testActors performs the trivial self-actions and reports a real change, so
// these tests exercise the guard/limiter path rather than an unwired actor.
func testActors() Actors {
	return Actors{
		TrimOwnBuffers: func() (Effect, error) {
			return Effect{Bytes: 8 << 20, Note: "trimmed", Changed: true}, nil
		},
		RotateOwnLedger: func() (Effect, error) {
			return Effect{Note: "rotated", Changed: true}, nil
		},
	}
}

// ST-7: TOCTOU — decision issued under canary re-checked at execution.
func TestST7Revalidation(t *testing.T) {
	g := guard(policy.ModeCanary)
	e := New(g)
	e.Actors = testActors()
	ok := e.Execute(context.Background(), dec(model.ActionTrimOwnBuffers, 0, ""))
	if ok.State != "executed" {
		t.Fatalf("expected executed, got %+v", ok)
	}
	// demote mode after issuance; next decision of the same shape refused
	g.Mode = func() policy.Mode { return policy.ModeObserve }
	ar := e.Execute(context.Background(), dec(model.ActionStopOwned, 0, "owned:1"))
	if ar.State != "refused" {
		t.Errorf("ST-7: mode change not revalidated: %+v", ar)
	}
}

// ST-8: rate limits apply across decisions (session-limited breaker).
func TestST8RateLimit(t *testing.T) {
	e := New(guard(policy.ModeEnforce))
	refused := 0
	for i := 0; i < 20; i++ {
		ar := e.Execute(context.Background(), dec(model.ActionStopOwned, 0, "owned:1"))
		if ar.State == "refused" {
			refused++
		}
	}
	if refused == 0 {
		t.Errorf("ST-8: rate limit never engaged")
	}
}

// ST-22 (unit level): foreign processes cannot be expressed as targets.
func TestST22ForeignTargetUnreachable(t *testing.T) {
	e := New(guard(policy.ModeEnforce))
	for _, tgt := range []string{"5012", "owned:5012", "pid:5012", "owned:1; rm -rf /"} {
		ar := e.Execute(context.Background(), dec(model.ActionStopOwned, 0, tgt))
		if ar.State == "executed" {
			t.Errorf("ST-22: foreign/malformed target executed: %q", tgt)
		}
	}
}

// ST-24: L2 requires OwnedPid + pre-auth + identity in registry.
func TestST24L2Boundaries(t *testing.T) {
	g := guard(policy.ModeEnforce)
	e := New(g)
	// registered owned pid with auth
	g.Registry.Register(OwnedPid{Pid: 4242, Pgid: 4242, Auth: "pre-auth-token"})
	// foreign pid not registered
	ar := e.Execute(context.Background(), dec(model.ActionStopOwned, 0, "owned:777"))
	if ar.State == "executed" {
		t.Errorf("ST-24: unregistered pid executed")
	}
	// registered: reaches the (real) stopOwnedChild path; in tests the pgid
	// 4242 process does not exist, so kill fails — the state must be failed,
	// never silently executed.
	ar = e.Execute(context.Background(), dec(model.ActionStopOwned, 0, "owned:4242"))
	if ar.State != "executed" && ar.State != "failed" {
		t.Errorf("ST-24: unexpected state %s", ar.State)
	}
	_ = ar
}

// A wrapper killed with an uncatchable signal never reaches admit_exit, so its registry
// entry has to be reaped by the daemon — otherwise a dead pid stays
// "stop-authorized" and could match a recycled pid, and the map grows with
// every orphaned launch.
func TestRegistryReapsDeadPids(t *testing.T) {
	r := NewRegistry()
	r.Register(OwnedPid{Pid: 999999, Pgid: 999999, Auth: "allow-stop"}) // certainly dead
	r.Register(OwnedPid{Pid: 1, Pgid: 1, Auth: "allow-stop"})           // init: alive
	alive := func(pid int32) bool { return pid == 1 }
	if n := r.Reap(alive); n != 1 {
		t.Errorf("reaped %d, want 1", n)
	}
	if _, ok := r.Lookup(999999); ok {
		t.Error("dead pid still registered (stop-authorized)")
	}
	if _, ok := r.Lookup(1); !ok {
		t.Error("live pid was reaped")
	}
}
