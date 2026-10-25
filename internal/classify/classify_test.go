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
		// A suspect must have grown materially; 300 MB of sustained growth with
		// no plateau and no CPU correlation is what the bucket is for.
		{"suspicion", SeriesStats{SlopeBytesS: 1e6, QuietS: 900, ActivityCorr: 0.1, CoverageRatio: 0.9, DurationS: 2400, GrowthBytes: 300 << 20, LongGrowthBytes: 300 << 20, NetGrowthBytes: 300 << 20}, LeakSuspicion},
		{"too-short", SeriesStats{SlopeBytesS: 1e6, QuietS: 900, CoverageRatio: 0.9, DurationS: 600, GrowthBytes: 300 << 20, NetGrowthBytes: 300 << 20}, Unknown},
		{"low-coverage", SeriesStats{SlopeBytesS: 1e6, QuietS: 900, CoverageRatio: 0.1, DurationS: 2400, GrowthBytes: 300 << 20, NetGrowthBytes: 300 << 20}, Unknown},
		{"no-growth", SeriesStats{SlopeBytesS: 0, CoverageRatio: 0.9}, Unknown},
		// Alive 40 minutes, quiet 40 minutes, grew 1 MB: an idle process, not a
		// suspect. Measured on a normally loaded desktop — zsh, esbuild and
		// extensionkitser all fired here.
		{"idle-with-trivial-growth", SeriesStats{SlopeBytesS: 1, QuietS: 2400, ActivityCorr: 0.0, CoverageRatio: 1.0, DurationS: 2400, GrowthBytes: 1 << 20, LongGrowthBytes: 1 << 20, NetGrowthBytes: 1 << 20}, Unknown},
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
		SlopeBytesS:    200e3, // SeriesFor supplies this from long-horizon growth
		NetGrowthBytes: 500 << 20, LongGrowthBytes: 500 << 20,
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
		LongGrowthBytes: 288 << 20, NetGrowthBytes: 288 << 20, LongPeakProcesses: 21,
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
		LongGrowthBytes: 288 << 20, NetGrowthBytes: 288 << 20, LongPeakProcesses: 21,
		ProcessCount: 21, PeakProcessCount: 21,
		DurationS: 1800, QuietS: 1700, ActivityCorr: 0.0, SlopeBytesS: 100})
	if v.Bucket != SubprocessAccumulation {
		t.Errorf("got %s (%v), want subprocess_accumulation", v.Bucket, v.Basis)
	}
}

// The warning board the owner complained about. Observed live on a normally
// loaded desktop at 14:05, with every process above behaving exactly as usual:
//
//	advisory group zsh grew by 1 MB — unexplained sustained growth (suspicion)
//	advisory group extensionkitser grew by 0 MB — unexplained sustained growth
//	advisory group esbuild grew by 0 MB — unexplained sustained growth
//
// The leak branch needed only duration and quiet time, both of which every
// long-lived idle process has. Production of the advisory is the failure, so
// this test asserts on the bucket, not on the wording.
func TestIdleProcessesAreNotLeakSuspects(t *testing.T) {
	cases := []struct {
		name string
		mb   int64
	}{
		{"zsh", 1}, {"extensionkitser", 0}, {"esbuild", 0}, {"bash", 7},
	}
	for _, c := range cases {
		v := Classify(SeriesStats{GroupKey: c.name, CoverageRatio: 1.0,
			GrowthBytes: c.mb << 20, LongGrowthBytes: c.mb << 20,
			SlopeBytesS: 100, QuietS: 2400, ActivityS: 0,
			ActivityCorr: 0.0, DurationS: 2400,
			ProcessCount: 1, PeakProcessCount: 1})
		if v.Bucket == LeakSuspicion {
			t.Errorf("%s grew %d MB in 40 minutes and was called a leak "+
				"suspicion: %v", c.name, c.mb, v.Basis)
		}
	}
}

// And real suspicion still fires: material growth, quiet, long enough.
func TestMaterialUnexplainedGrowthIsStillSuspicion(t *testing.T) {
	v := Classify(SeriesStats{GroupKey: "probe-leak", CoverageRatio: 1.0,
		GrowthBytes: 300 << 20, LongGrowthBytes: 300 << 20, NetGrowthBytes: 300 << 20,
		SlopeBytesS: 100e3, QuietS: 1800, ActivityS: 0, ActivityCorr: 0.0,
		DurationS: 2400, ProcessCount: 1, PeakProcessCount: 1})
	if v.Bucket != LeakSuspicion {
		t.Errorf("300 MB of quiet sustained growth: got %s (%v), want leak_suspicion",
			v.Bucket, v.Basis)
	}
}

// Gross growth is not size. Observed live: 46 node processes oscillating a few
// MB per sample accumulated 11 GB of gross "growth" after an hour and 73 GB
// after four, on a 16 GB machine where they held 3.2 GB in total. Every one of
// those windows produced a subprocess advisory that grew more absurd each time.
func TestOscillatingGroupIsNotAccumulating(t *testing.T) {
	v := Classify(SeriesStats{GroupKey: "node", CoverageRatio: 1.0,
		// A window where node moved: 400 MB of gross churn, no net change.
		GrowthBytes: 400 << 20, ReleaseBytes: 0,
		// Lifetime: 73 GB of gross growth, ~0 net, 46 stable processes.
		LongGrowthBytes: 73 << 30, NetGrowthBytes: 0,
		ProcessCount: 46, PeakProcessCount: 46, LongPeakProcesses: 46,
		DurationS: 14205, QuietS: 10000, ActivityCorr: 0.2, SlopeBytesS: 100,
	})
	if v.Bucket == SubprocessAccumulation || v.Bucket == LeakSuspicion {
		t.Errorf("a group oscillating around a constant size was called %s: %v",
			v.Bucket, v.Basis)
	}
}

// Real accumulation has positive net growth.
func TestNetGrowthStillAccumulates(t *testing.T) {
	v := Classify(SeriesStats{GroupKey: "probe-children", CoverageRatio: 1.0,
		GrowthBytes: 0, LongGrowthBytes: 300 << 20, NetGrowthBytes: 288 << 20,
		ProcessCount: 21, PeakProcessCount: 21, LongPeakProcesses: 21,
		DurationS: 1800, QuietS: 1700, ActivityCorr: 0.0, SlopeBytesS: 100})
	if v.Bucket != SubprocessAccumulation {
		t.Errorf("288 MB of net growth across 21 processes: got %s (%v)",
			v.Bucket, v.Basis)
	}
}
