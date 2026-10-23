package pipeline

// Fault-injection matrix (bead j7b): every injected fault must demote or
// refuse — never fabricate calm (SI-4/SI-7).
import (
	"testing"
	"time"

	"memory_pilot/internal/forecast"
	"memory_pilot/internal/model"
	"memory_pilot/internal/pressure"
)

// FI-1 collector failure: mach stats missing ⇒ Unknown, never Normal.
func TestFI1CollectorFailure(t *testing.T) {
	est := pressure.New()
	out := est.Update(pressure.Input{Sample: model.SystemSample{
		MachStatsQuality: model.QualMissing, KernelLevelQuality: model.QualMissing}})
	if out.State != pressure.Unknown {
		t.Fatalf("FI-1: got %s want Unknown", out.State)
	}
}

// FI-2 kernel level unreadable: fallback used, provenance=derived.
func TestFI2KernelLevelDead(t *testing.T) {
	est := pressure.New()
	out := est.Update(pressure.Input{
		Sample: model.SystemSample{MachStatsQuality: model.QualGood, PageBytes: 16384, FreePages: 300_000},
		TotalPages: 1_000_000, SwapOK: true, SwapUsedBytes: 1e9})
	if out.Provenance != "derived" {
		t.Fatalf("FI-2: provenance %s", out.Provenance)
	}
}

// FI-3 forecaster fault (alarm): forecast unusable ⇒ not actionable (SI-7).
func TestFI3GuardrailAlarm(t *testing.T) {
	fc := forecast.New(100)
	for i := 0; i < 50; i++ {
		fc.ObserveDrift(3.0) // sustained severe under-prediction
	}
	o := fc.Update(forecast.Sample{MonotonicMs: 1000, HeadroomBytes: 1e9})
	if o.GuardAlarm && o.Actionable {
		t.Fatal("FI-3: alarmed forecast still actionable")
	}
}

// FI-4 clock skew: non-monotone timestamps must not produce rates.
func TestFI4ClockSkew(t *testing.T) {
	fc := forecast.New(100)
	fc.Update(forecast.Sample{MonotonicMs: 10000, HeadroomBytes: 1e9})
	o := fc.Update(forecast.Sample{MonotonicMs: 5000, HeadroomBytes: 5e8}) // backwards
	if o.RateBytesS != 0 {
		t.Fatalf("FI-4: rate computed across skew: %v", o.RateBytesS)
	}
}

// FI-5 partial coverage: low coverage blocks classification (tested in
// classify: low-coverage ⇒ Unknown) and blocks orphan recommendation
// (reporting TestOrphanHonestDenial).

// FI-6 stale decision refused (executor TestST6), FI-7 mode demoted
// (executor TestST7), FI-8 rate limit (executor TestST8),
// FI-9 missing evidence blocks recovery (pressure TestNoRecoveryOnDegraded),
// FI-10 path escape (store), FI-11 crash tail (ledger), FI-12 second instance
// (supervisor TestST21), FI-13 unverified by default (verify TestST10),
// FI-14 budget breach ⇒ unhealthy (selfmonitor TestST20).
func TestFIIndexDocumented(t *testing.T) {
	// The matrix above is the index; individual behaviors live in the
	// referenced package tests. This test keeps the index compiling.
	_ = time.Now
}
