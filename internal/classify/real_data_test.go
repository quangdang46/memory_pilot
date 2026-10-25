package classify

import "testing"

// Replay the six groups that produced false leak advisories at 14:05 on a
// normally loaded machine, using the signals the daemon actually recorded.
// This is the warning board the owner complained about; it must stay silent.
func TestObservedFalseAdvisoriesStaySilent(t *testing.T) {
	type obs struct {
		name    string
		growth  int64 // bytes, whole life
		peak    int
		quiet   int64
		dur     int64
		corr    float64
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
