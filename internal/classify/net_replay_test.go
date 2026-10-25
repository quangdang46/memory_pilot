package classify

import "testing"

// The real node group: 46 processes holding ~3.4 GB, oscillating a few MB per
// sample for four hours. Before the net-growth fix it accumulated 73 GB of
// gross "growth" and produced an advisory every window, each larger than the
// last — 11 GB, then 13, then 20, reaching 73 GB on a 16 GB machine. Replayed
// with the values the daemon actually recorded at 18:41.
func TestNodeOscillationReplay(t *testing.T) {
	v := Classify(SeriesStats{GroupKey: "node", CoverageRatio: 1.0,
		GrowthBytes:     907 << 20,   // that window's gross churn
		LongGrowthBytes: 73310 << 20, // lifetime gross — the absurd number
		NetGrowthBytes:  400 << 20,   // what it actually added over four hours
		ProcessCount:    46, PeakProcessCount: 46, LongPeakProcesses: 46,
		DurationS: 14205, QuietS: 10000, ActivityCorr: 0.2, SlopeBytesS: 5e6,
	})
	if v.Bucket == SubprocessAccumulation {
		if v.GrowthMB > 2000 {
			t.Errorf("verdict quotes %d MB for a group holding 3.4 GB", v.GrowthMB)
		}
		t.Logf("accumulation judged on net 400 MB, not gross 73310 MB")
	}
}

// A group oscillating with no net change at all is not accumulating.
func TestPureOscillationNeverAccumulates(t *testing.T) {
	v := Classify(SeriesStats{GroupKey: "node", CoverageRatio: 1.0,
		GrowthBytes: 500 << 20, LongGrowthBytes: 73000 << 20, NetGrowthBytes: 0,
		ProcessCount: 46, PeakProcessCount: 46, LongPeakProcesses: 46,
		DurationS: 14205, QuietS: 10000, ActivityCorr: 0.2, SlopeBytesS: 5e6,
	})
	if v.Bucket == SubprocessAccumulation || v.Bucket == LeakSuspicion {
		t.Errorf("zero net growth over 4 hours called %s: %v", v.Bucket, v.Basis)
	}
}
