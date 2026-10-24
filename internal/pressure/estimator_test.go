package pressure

import (
	"testing"
	"time"

	"memory_pilot/internal/model"
)

func base() Input {
	return Input{TotalPages: 1_000_000, SwapOK: true, DiskOK: true, DiskFreePct: 30,
		Sample: model.SystemSample{
			PageBytes: 16384, MachStatsQuality: model.QualGood,
			KernelLevelQuality: model.QualGood, KernelLevel: model.LevelNormal,
			FreePages: 200_000, CompressorPages: 100_000,
		}}
}

// T-PE-1: kernel 1/2/4 map to Normal/Prevent/Critical.
func TestKernelDecode(t *testing.T) {
	e := New()
	ms := int64(0)
	for lvl, want := range map[model.PressureLevel]State{
		model.LevelNormal: Normal, model.LevelWarn: Prevent, model.LevelCritical: Critical,
	} {
		// first tick starts the dwell, second publishes (15s dwell, 20s ticks)
		var got StateOut
		for k := 0; k < 2; k++ {
			ms += 20000
			in := base()
			in.Sample.MonotonicMs = ms
			in.Sample.KernelLevel = lvl
			got = e.Update(in)
		}
		if got.State != want {
			t.Errorf("level %d: got %s want %s", lvl, got.State, want)
		}
		if got.Provenance != "kernel" {
			t.Errorf("provenance %s", got.Provenance)
		}
	}
}

// T-PE-2: kernel unavailable ⇒ fallback with provenance=derived.
func TestFallbackDerived(t *testing.T) {
	e := New()
	in := base()
	in.Sample.KernelLevelQuality = model.QualMissing
	// free 2% + swap hot ⇒ Critical via fallback
	in.Sample.FreePages = 10_000
	in.SwapUsedBytes = 1 << 30
	got := e.Update(in)
	if got.State != Critical || got.Provenance != "derived" {
		t.Errorf("got %s/%s want Critical/derived", got.State, got.Provenance)
	}
}

// T-PE-3: all unavailable ⇒ Unknown, never Normal.
func TestUnknownNeverNormal(t *testing.T) {
	e := New()
	in := base()
	in.Sample.KernelLevelQuality = model.QualMissing
	in.Sample.MachStatsQuality = model.QualMissing
	got := e.Update(in)
	if got.State != Unknown || got.ScoreReliable {
		t.Errorf("got %s reliable=%v", got.State, got.ScoreReliable)
	}
}

// T-PE-4: trend raises Normal→Watch but never lowers Prevent.
func TestTrendElevatesOnly(t *testing.T) {
	e := New()
	in := base()
	in.TrendRisk = 0.5
	if got := e.Update(in); got.State != Watch {
		t.Errorf("trend should elevate to Watch, got %s", got.State)
	}
	e2 := New()
	in2 := base()
	in2.Sample.KernelLevel = model.LevelWarn
	in2.TrendRisk = 0.0
	if got := e2.Update(in2); got.State != Prevent {
		t.Errorf("low trend must not lower kernel Prevent, got %s", got.State)
	}
}

// T-PE-5: hysteresis/dwell — no flapping within 15s dwell.
func TestDwellNoFlapping(t *testing.T) {
	e := New()
	in := base()
	in.Sample.MonotonicMs = 0
	got := e.Update(in)
	if got.State != Normal {
		t.Fatalf("initial %s", got.State)
	}
	// kernel jumps to warn 5s later — must not publish (dwell 15s)
	in.Sample.KernelLevel = model.LevelWarn
	in.Sample.MonotonicMs = 5000
	if got := e.Update(in); got.State != Normal {
		t.Errorf("dwell violated: %s at 5s", got.State)
	}
	// at t=20s it publishes
	in.Sample.MonotonicMs = 20000
	if got := e.Update(in); got.State != Prevent {
		t.Errorf("dwell never satisfied: %s at 20s", got.State)
	}
}

// T-PE-6: degraded input during Prevent blocks recovery.
func TestNoRecoveryOnDegraded(t *testing.T) {
	e := New()
	in := base()
	in.Sample.KernelLevel = model.LevelWarn
	in.Sample.MonotonicMs = 0
	e.Update(in)
	in.Sample.MonotonicMs = 20000
	e.Update(in) // Prevent published
	// now kernel read fails but machine recovered — must NOT recover
	in.Sample.KernelLevel = model.LevelNormal
	in.Sample.KernelLevelQuality = model.QualMissing
	in.Sample.MonotonicMs = 40000
	got := e.Update(in)
	if got.State == Normal && got.Provenance != "derived" {
		t.Errorf("recovered despite degraded evidence")
	}
}

// T-PE-7: score monotone in free scarcity.
func TestScoreMonotone(t *testing.T) {
	e := New()
	in := base()
	s1 := e.score(Normal, 30, 5, in)
	s2 := e.score(Normal, 5, 5, in)
	if s2 <= s1 {
		t.Errorf("score not monotone in free: %v vs %v", s1, s2)
	}
}

// Regression (live bug, 2026-10-24): the macOS collector never set
// MonotonicMs, so dwell was computed from a constant 0. `dwelled` was
// therefore always false while `immediate` let every Critical through —
// the estimator published Critical once and could never recover, and the
// daemon reported Critical for 45 minutes after the kernel returned to
// Normal. A collector without a monotonic clock must still recover.
func TestRecoveryWithoutMonotonicClock(t *testing.T) {
	e := New()
	in := base()
	wall := time.Unix(1_700_000_000, 0)
	in.Sample.At = wall
	in.Sample.KernelLevel = model.LevelCritical
	if got := e.Update(in); got.State != Critical {
		t.Fatalf("initial Critical not published: %s", got.State)
	}
	// 40 more samples, all Normal on the kernel, still no MonotonicMs.
	var got StateOut
	for i := 1; i <= 40; i++ {
		in.Sample.At = wall.Add(time.Duration(i) * 5 * time.Second)
		in.Sample.KernelLevel = model.LevelNormal
		got = e.Update(in)
	}
	if got.State != Normal {
		t.Fatalf("latched at %s with no monotonic clock (the live bug)", got.State)
	}
}

// Dwell must still gate flapping when the clock *is* present.
func TestDwellStillGatesWithClock(t *testing.T) {
	e := New()
	in := base()
	in.Sample.MonotonicMs = 1000
	in.Sample.KernelLevel = model.LevelWarn
	if got := e.Update(in); got.State != Prevent {
		t.Fatalf("first sample should publish: %s", got.State)
	}
	in.Sample.MonotonicMs = 6000 // 5s later: inside the 15s dwell
	in.Sample.KernelLevel = model.LevelNormal
	if got := e.Update(in); got.State != Prevent {
		t.Errorf("recovery published inside dwell window: %s", got.State)
	}
	in.Sample.MonotonicMs = 30000
	if got := e.Update(in); got.State != Normal {
		t.Errorf("recovery blocked past dwell: %s", got.State)
	}
}
