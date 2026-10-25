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
