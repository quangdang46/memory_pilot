package classify

import (
	"strings"
	"testing"
)

func TestFiveBuckets(t *testing.T) {
	cases := []struct {
		name string
		in   SeriesStats
		want Bucket
	}{
		{"workload", SeriesStats{SlopeBytesS: 1e6, ActivityCorr: 0.8, ActivityS: 300, CoverageRatio: 0.9, DurationS: 1800}, WorkloadGrowth},
		{"cache-plateau", SeriesStats{SlopeBytesS: 1e6, PlateauS: 1200, CacheRole: true, CoverageRatio: 0.9, DurationS: 2400}, CacheOrRetrievable},
		{"suspicion", SeriesStats{SlopeBytesS: 1e6, QuietS: 900, ActivityCorr: 0.1, CoverageRatio: 0.9, DurationS: 2400}, LeakSuspicion},
		{"too-short", SeriesStats{SlopeBytesS: 1e6, QuietS: 900, CoverageRatio: 0.9, DurationS: 600}, Unknown},
		{"low-coverage", SeriesStats{SlopeBytesS: 1e6, QuietS: 900, CoverageRatio: 0.1, DurationS: 2400}, Unknown},
		{"no-growth", SeriesStats{SlopeBytesS: 0, CoverageRatio: 0.9}, Unknown},
	}
	for _, c := range cases {
		v := Classify(c.in)
		if v.Bucket != c.want {
			t.Errorf("%s: got %s want %s (basis %v)", c.name, v.Bucket, c.want, v.Basis)
		}
		if len(v.Basis) == 0 && v.Bucket != Unknown {
			t.Errorf("%s: bare claim without basis", c.name)
		}
		if v.Bucket == LeakSuspicion && len(v.Alternatives) == 0 {
			t.Errorf("suspicion must list alternatives it could not exclude")
		}
	}
}

func TestPhraseTest(t *testing.T) {
	good := RenderVerdict(Verdict{GroupKey: "node", Bucket: LeakSuspicion, Confidence: 0.65,
		Basis: []string{"sustained 2400s"}}, 2048)
	if bad := PhraseTest(good); len(bad) > 0 {
		t.Errorf("rendered text failed phrase test: %v — %s", bad, good)
	}
	badText := "node is leaking memory; the cause is Chrome. Safe to kill it."
	viol := PhraseTest(badText)
	if len(viol) != 3 || !strings.Contains(strings.Join(viol, ","), "is leaking") {
		t.Errorf("phrase test missed violations: %v", viol)
	}
}

// A group that neither grew nor released is not a cache; 0 >= 0 must not
// satisfy the cycling test. Measured: probe-children with growth=0 and
// release=0 was classified cache_or_retrievable.
func TestZeroGrowthZeroReleaseIsNotCycling(t *testing.T) {
	v := Classify(SeriesStats{GroupKey: "idle-group", CoverageRatio: 1.0,
		GrowthBytes: 0, ReleaseBytes: 0, ProcessCount: 12, PeakProcessCount: 20,
		DurationS: 2400, ActivityCorr: 0.0, QuietS: 2000})
	if v.Bucket == CacheOrRetrievable {
		t.Errorf("idle group classified as cycling cache: %v", v.Basis)
	}
}

// A few megabytes of churn is not a cycling cache. Measured in run16:
// probe-children (a shape that was accumulating 20 subprocesses) was called
// cache_or_retrievable on the basis "released 1 MB while growing 0 MB",
// because the cycling test only required both sides to be non-zero.
func TestNegligibleChurnIsNotCycling(t *testing.T) {
	v := Classify(SeriesStats{GroupKey: "probe-children", CoverageRatio: 1.0,
		GrowthBytes: 0, ReleaseBytes: 1 << 20, SlopeBytesS: 100,
		ProcessCount: 21, PeakProcessCount: 21, DurationS: 2400,
		ActivityCorr: 0.0, QuietS: 2000})
	if v.Bucket == CacheOrRetrievable {
		t.Errorf("1 MB of churn classified as a cycling cache: %v", v.Basis)
	}
}

// Real cycling still classifies: a browser releasing what it allocates.
func TestRealCyclingStillDetected(t *testing.T) {
	v := Classify(SeriesStats{GroupKey: "Google Chrome", CoverageRatio: 1.0,
		GrowthBytes: 400 << 20, ReleaseBytes: 900 << 20, SlopeBytesS: 100,
		ProcessCount: 12, PeakProcessCount: 12, DurationS: 2400,
		ActivityCorr: 0.2, QuietS: 2000})
	if v.Bucket != CacheOrRetrievable {
		t.Errorf("browser cycling 400 MB in / 900 MB out: got %s (%v), want cache",
			v.Bucket, v.Basis)
	}
}

// A normal app with many processes and modest growth is not accumulation.
// Measured: the first version fired on zsh (0 MB), Chrome (12 MB) and node
// (110 MB) in a single window, because "has many processes" is true of every
// long-lived app.
func TestNormalAppIsNotAccumulation(t *testing.T) {
	cases := []struct {
		name  string
		stats SeriesStats
	}{
		{"chrome-idle", SeriesStats{GroupKey: "Google Chrome", GrowthBytes: 12 << 20,
			ReleaseBytes: 40 << 20, PeakProcessCount: 13, ProcessCount: 13,
			DurationS: 2400, CoverageRatio: 1, ActivityCorr: 0.1}},
		{"zsh-no-growth", SeriesStats{GroupKey: "zsh", GrowthBytes: 0,
			PeakProcessCount: 16, ProcessCount: 16, DurationS: 2400,
			CoverageRatio: 1, ActivityCorr: 0.0}},
	}
	for _, c := range cases {
		v := Classify(c.stats)
		if v.Bucket == SubprocessAccumulation {
			t.Errorf("%s classified as accumulation (growth %d MB): %v",
				c.name, c.stats.GrowthBytes>>20, v.Basis)
		}
	}
}

// Real accumulation: many processes AND material growth, no CPU correlation.
func TestRealAccumulationStillDetected(t *testing.T) {
	v := Classify(SeriesStats{GroupKey: "probe-children", GrowthBytes: 500 << 20,
		SlopeBytesS: 200e3, // SeriesFor supplies this from long-horizon growth
		PeakProcessCount: 21, ProcessCount: 21, DurationS: 2400,
		CoverageRatio: 1, ActivityCorr: 0.0})
	if v.Bucket != SubprocessAccumulation {
		t.Errorf("genuine accumulation missed: %s (%v)", v.Bucket, v.Basis)
	}
}

// Horizon mismatch: a group that accumulated 288 MB over 25 minutes and freed
// 130 MB of it must not be called a "cycling cache" because the current
// 5-minute window happened to show no allocation. Comparing a window's growth
// against a lifetime's release is not a comparison.
func TestHorizonMismatchIsNotCycling(t *testing.T) {
	v := Classify(SeriesStats{GroupKey: "probe-children", CoverageRatio: 1.0,
		// This window: nothing allocated, 130 MB released.
		GrowthBytes: 0, ReleaseBytes: 130 << 20,
		// Lifetime: 288 MB accumulated, 130 MB released, 21 processes.
		LongGrowthBytes: 288 << 20, LongPeakProcesses: 21,
		ProcessCount: 21, PeakProcessCount: 21,
		DurationS: 1800, QuietS: 1700, ActivityCorr: 0.0, SlopeBytesS: 100})
	if v.Bucket == CacheOrRetrievable {
		t.Errorf("accumulated 288 MB, released 130 MB — called a cycling cache: %v", v.Basis)
	}
	if v.Bucket != SubprocessAccumulation {
		t.Errorf("21 processes holding 288 MB: got %s (%v), want subprocess_accumulation",
			v.Bucket, v.Basis)
	}
}

// A group that spawns children and then holds them grows nothing in the window
// it is judged on, but it is still accumulation.
func TestAccumulationWithoutWindowGrowth(t *testing.T) {
	v := Classify(SeriesStats{GroupKey: "probe-children", CoverageRatio: 1.0,
		GrowthBytes: 0, ReleaseBytes: 0,
		LongGrowthBytes: 288 << 20, LongPeakProcesses: 21,
		ProcessCount: 21, PeakProcessCount: 21,
		DurationS: 1800, QuietS: 1700, ActivityCorr: 0.0, SlopeBytesS: 100})
	if v.Bucket != SubprocessAccumulation {
		t.Errorf("got %s (%v), want subprocess_accumulation", v.Bucket, v.Basis)
	}
}
