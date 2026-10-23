package forecast

import (
	"math"
	"testing"
)

// TTE guard rails: d<=0 ⇒ 0; v<=0 ⇒ ∞(-1); negative discriminant ⇒ ∞;
// conjugate form for a<0; unphysical projection rejected.
func TestTTEGuardRails(t *testing.T) {
	if got := tte(10, 1, 0); got != 0 {
		t.Errorf("d<=0 must be 0, got %v", got)
	}
	if got := tte(-5, 1, 100); got != -1 {
		t.Errorf("v<=0 must be none, got %v", got)
	}
	if got := tte(10, -10, 100); got != -1 {
		t.Errorf("strong deceleration must be none, got %v", got)
	}
	// linear: t = d/v
	if got := tte(10, 0, 100); math.Abs(got-10) > 1e-9 {
		t.Errorf("linear tte got %v", got)
	}
	// a>0: t=(-v+sqrt(v²+2ad))/a = (-10+sqrt(100+400))/1 ≈ 12.36
	if got := tte(10, 1, 200); math.Abs(got-12.36068) > 1e-4 {
		t.Errorf("quadratic tte got %v", got)
	}
	// a<0 near-cancellation must not lose precision: v=1e-3, a=-1e-9, d=1e-3
	got := tte(1e-3, -1e-9, 1e-3)
	if got <= 0 || math.IsNaN(got) {
		t.Errorf("conjugate form failed: %v", got)
	}
}

// Conformal fail-safe: after warmup with all-worst scores, k>n ⇒ lo=0.
func TestConformalFailSafe(t *testing.T) {
	f := New(100)
	for i := 0; i < CfgWarmup; i++ {
		f.scores = append(f.scores, resolved{score: 1.0})
	}
	if lo := f.lowerBound(500); lo != 0 {
		t.Errorf("k>n must yield lo=0, got %v", lo)
	}
	// warmup returns the point
	f2 := New(100)
	if lo := f2.lowerBound(500); lo != 500 {
		t.Errorf("warmup must return point, got %v", lo)
	}
}

// E-process: only under-prediction penalized; clamps responsive.
func TestEprocDirectional(t *testing.T) {
	f := New(100)
	for i := 0; i < 50; i++ {
		f.ObserveDrift(0.5) // over-prediction (safe direction) — no penalty
	}
	if math.Exp(f.elog) >= EprocThreshold {
		t.Errorf("safe-direction drift must not alarm: e=%v", math.Exp(f.elog))
	}
	f2 := New(100)
	for i := 0; i < 50; i++ {
		f2.ObserveDrift(2.0) // severe under-prediction
	}
	if math.Exp(f2.elog) < EprocThreshold {
		t.Errorf("sustained severe drift must alarm: e=%v", math.Exp(f2.elog))
	}
	if f2.elog > EprocMax+1e-9 {
		t.Errorf("elog clamp violated: %v", f2.elog)
	}
}

// Burst vs trend: a single spike must not be treated as sustained ramp signal.
func TestBurstDamping(t *testing.T) {
	f := New(0)
	// steady consumption 1MB/s for 40 ticks
	mono := int64(0)
	for i := 0; i < 40; i++ {
		mono += 5000
		f.Update(Sample{MonotonicMs: mono, HeadroomBytes: float64(1e9 - (i+1)*5e6)})
	}
	// spike: 500MB gone in one tick
	o := f.Update(Sample{MonotonicMs: mono + 5000, HeadroomBytes: float64(1e9 - 45*5e6 - 5e8)})
	if o.BurstActive != true {
		t.Errorf("spike should register as burst (prob=%v)", o.BurstProb)
	}
	// EWMA rate should stay well below inst rate (damped)
	if o.RateBytesS > 1e8 {
		t.Errorf("EWMA chased the spike: %v B/s", o.RateBytesS)
	}
}

// Scorecard: Intervened is not a false alarm.
func TestScorecard(t *testing.T) {
	f := New(0)
	f.Scorecard("intervened")
	f.Scorecard("false_alarm")
	if f.far() != 50 {
		t.Errorf("FAR=%v want 50 (intervened excluded)", f.far())
	}
}
