package classify

import (
	"strings"
	"testing"
)

// Replay the six groups that produced false leak advisories at 14:05 on a
// normally loaded machine, using the signals the daemon actually recorded.
// This is the warning board the owner complained about; it must stay silent.
func TestObservedFalseAdvisoriesStaySilent(t *testing.T) {
	type obs struct {
		name   string
		growth int64 // bytes, whole life
		peak   int
		quiet  int64
		dur    int64
		corr   float64
	}
	cases := []obs{
		{"Google Chrome", 13 << 20, 19, 1784, 2400, 0.0},
		{"zsh", 1 << 20, 34, 2400, 2400, 0.0},
		{"extensionkitser", 0, 1, 2400, 2400, 0.0},
		{"esbuild", 0, 1, 2400, 2400, 0.0},
		{"bash", 7 << 20, 38, 2400, 2400, 0.0},
	}
	for _, c := range cases {
		v := Classify(SeriesStats{GroupKey: c.name, CoverageRatio: 1.0,
			GrowthBytes: c.growth, LongGrowthBytes: c.growth,
			SlopeBytesS: 100, QuietS: c.quiet, ActivityCorr: c.corr,
			DurationS: c.dur, ProcessCount: c.peak, PeakProcessCount: c.peak,
			PlateauS: 0})
		if v.Bucket == LeakSuspicion || v.Bucket == SubprocessAccumulation {
			t.Errorf("%s: grew %d MB over %ds with peak %d processes → %s (%v); "+
				"this is the advisory the owner asked never to see again",
				c.name, c.growth>>20, c.dur, c.peak, v.Bucket, v.Basis)
		}
	}
}

// The four false-positive advisories of 2026-10-26, replayed from their exact
// recorded signals. Each was produced by an inflated NetGrowthBytes — a slope
// extrapolation fitted across a coverage flip — so with the attribution fix the
// net is zero and the classifier has nothing to act on.
//
// Recorded at 09:34:56 for group Code: it held ~300 MB, grew 14 MB in the
// window and 17 MB over its life, and reported net_growth_bytes 4051939249
// (3864 MB). The threshold is 256 MB.
func TestInflatedNetGrowthWasTheOnlyThingBehindTheAdvisory(t *testing.T) {
	base := SeriesStats{
		GroupKey: "Code", SlopeBytesS: 3376616, ActivityCorr: 0,
		ActivityS: 0, QuietS: 585, DurationS: 585, PlateauS: 225,
		GrowthBytes: 14123008, ReleaseBytes: 28835840,
		LongGrowthBytes: 17498112,
		ProcessCount:    12, PeakProcessCount: 12, LongPeakProcesses: 12,
		CoverageRatio: 1.0,
	}
	// The recorded figure is 250 times the group's lifetime growth. The
	// classifier now refuses it as not credible — that is the second line of
	// defence, and it is what this asserts.
	inflated := base
	inflated.NetGrowthBytes = 4051939249
	v := Classify(inflated)
	if v.Bucket == SubprocessAccumulation || v.Bucket == LeakSuspicion {
		t.Errorf("a net growth 250x the lifetime growth still produced %s (%v)",
			v.Bucket, v.Basis)
	}
	joined := ""
	for _, b := range v.Basis {
		joined += b + " "
	}
	if !strings.Contains(joined, "not credible") && !strings.Contains(joined, "no trend extrapolates") {
		t.Errorf("the refusal does not say why: %v", v.Basis)
	}

	// The guard must not silence a real accumulator. A group that genuinely
	// added 400 MB over its life and is on pace for twice that passes both the
	// credibility check and the 256 MB accumulation threshold — whereas this
	// group's 17 MB lifetime cannot reach 256 MB at any credible multiple,
	// which is exactly why its advisory was impossible.
	plausible := base
	plausible.LongGrowthBytes = 400 << 20
	plausible.NetGrowthBytes = 800 << 20 // 2x lifetime: an ordinary trend
	if v := Classify(plausible); v.Bucket != SubprocessAccumulation {
		t.Errorf("a genuine 400 MB accumulation on pace for 800 MB produced %s "+
			"(%v), want accumulation — the guard is too tight", v.Bucket, v.Basis)
	}
	// With the net the fixed attribution now produces, it does not.
	fixed := base
	fixed.NetGrowthBytes = 0
	if v := Classify(fixed); v.Bucket == SubprocessAccumulation || v.Bucket == LeakSuspicion {
		t.Errorf("with net 0 the classifier still produced %s (%v) — the advisory "+
			"did not depend on the inflated net after all", v.Bucket, v.Basis)
	}
}

// VTDecoderXPCSer, recorded at 00:53/00:58/01:03: net growth 665/741/447 MB
// against 7/10/22 MB in the window and 556/567/589 MB over its whole life. It
// cannot be on pace to add more than it has ever added.
func TestPaceCannotExceedLifetimeGrowth(t *testing.T) {
	for _, c := range []struct{ net, life, win int64 }{
		{665, 556, 7}, {741, 567, 10}, {447, 589, 22},
	} {
		s := SeriesStats{
			GroupKey: "VTDecoderXPCSer", CoverageRatio: 1.0,
			SlopeBytesS: 581386, ActivityCorr: 0, QuietS: 1484, DurationS: 1484,
			GrowthBytes: c.win << 20, LongGrowthBytes: c.life << 20,
			NetGrowthBytes: c.net << 20, ProcessCount: 2, PeakProcessCount: 2,
			LongPeakProcesses: 2,
		}
		v := Classify(s)
		// A leak verdict is correct here — this group added 13-32 MB every five
		// minutes for five hours. What is not acceptable is the number: the fit
		// claimed 665 MB against 7 MB actually added.
		if v.Bucket == LeakSuspicion && v.GrowthMB > 5*c.win {
			t.Errorf("verdict quotes %d MB while the window added %d MB — the "+
				"sentence outruns its own evidence (%v)", v.GrowthMB, c.win, v.Basis)
		}
	}
	// A trend that extrapolates at up to twice lifetime is still credible.
	s := SeriesStats{
		GroupKey: "real-accum", CoverageRatio: 1.0, SlopeBytesS: 900000,
		ActivityCorr: 0, QuietS: 1300, DurationS: 1300,
		GrowthBytes: 300 << 20, LongGrowthBytes: 400 << 20,
		NetGrowthBytes: 700 << 20, ProcessCount: 12, PeakProcessCount: 12,
		LongPeakProcesses: 12,
	}
	if v := Classify(s); v.Bucket != SubprocessAccumulation {
		t.Errorf("a credible 700 MB pace on 400 MB lifetime produced %s — too tight", v.Bucket)
	}
}
