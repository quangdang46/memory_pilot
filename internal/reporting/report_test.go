package reporting

import (
	"strings"
	"testing"
	"time"

	"memory_pilot/internal/classify"
)

// G-P5 golden outputs + honest-language rules.
func TestGoldenStatus(t *testing.T) {
	got := RenderStatus(StatusLine{PID: 1234, UptimeS: 3600, Mode: "observe",
		Pressure: "Prevent", Score: 0.66, Provenance: "kernel", Reliable: true,
		SelfRSSMB: 4.0, SelfCPUPct: 0.03, LedgerMB: 1.2})
	want := "mempilotd pid 1234 | uptime 1h0m0s | mode observe | pressure Prevent (0.66, kernel) | self rss 4.0MB cpu 0.03% | ledger 1.2MB"
	if got != want {
		t.Errorf("golden status mismatch:\n got %q\nwant %q", got, want)
	}
}

func TestUnreliableNeverLooksCalm(t *testing.T) {
	got := RenderStatus(StatusLine{PID: 1, Mode: "observe", Reliable: false, Pressure: "Unknown"})
	if !strings.Contains(got, "UNKNOWN") || strings.Contains(got, "Normal") {
		t.Errorf("unreliable state must read UNKNOWN: %s", got)
	}
}

func TestGoldenForecast(t *testing.T) {
	got := RenderForecast(ForecastLine{TteS: 812, LowerS: 410, Confidence: 0.78, Calibrated: true, Actionable: true})
	if !strings.Contains(got, "ETA 13m32s") || !strings.Contains(got, "0.78") || !strings.Contains(got, "lower bound 6m50s") {
		t.Errorf("golden forecast mismatch: %s", got)
	}
	warming := RenderForecast(ForecastLine{TteS: -1, Confidence: 0.2})
	if !strings.Contains(warming, "warming up (not actionable)") {
		t.Errorf("warming language wrong: %s", warming)
	}
}

func TestReportPassesPhraseTest(t *testing.T) {
	v := classify.Verdict{GroupKey: "node", Bucket: classify.LeakSuspicion,
		Confidence: 0.64, Basis: []string{"sustained 2820s"}, Alternatives: []classify.Bucket{classify.WorkloadGrowth}}
	text := RenderClassification(v, 1843)
	if bad := classify.PhraseTest(text); len(bad) > 0 {
		t.Errorf("report text failed phrase test: %v in %q", bad, text)
	}
}

func TestReliefThreeNumbers(t *testing.T) {
	got := RenderRelief(ReliefCandidate{Kind: "trim_own_buffers",
		EffectLowBytes: 8 << 20, EffectHighBytes: 64 << 20, Confidence: 0.7,
		CostCPUms: 3, RiskNote: "self only", Verified: false})
	if !strings.Contains(got, "8–64 MB") || !strings.Contains(got, "0.70") || !strings.Contains(got, "3ms") {
		t.Errorf("relief report missing three numbers: %s", got)
	}
	if !strings.Contains(got, "unverified") {
		t.Errorf("unverified relief must say so: %s", got)
	}
}

func TestOrphanHonestDenial(t *testing.T) {
	ok := OrphanCandidate{Pid: 5, Exe: "node", ParentGone: true, IdleS: 3600, FootprintMB: 900, CoverageOK: true}
	rec, yes := ok.Recommend(1800)
	if !yes || !strings.Contains(rec, "we will not act on it") {
		t.Errorf("orphan recommendation wrong: %q", rec)
	}
	lowCov := OrphanCandidate{Pid: 5, Exe: "node", ParentGone: true, IdleS: 3600, FootprintMB: 900, CoverageOK: false}
	if _, yes := lowCov.Recommend(1800); yes {
		t.Errorf("low coverage must honestly deny")
	}
}

func TestSessionDescribe(t *testing.T) {
	now := time.Unix(1700000000, 0)
	active := SessionState{GroupKey: "iTerm2:3", CPUDeltaS: 2.5, LastActive: now.Add(-time.Minute)}
	if !strings.Contains(active.Describe(now), "active") {
		t.Errorf("active describe wrong: %s", active.Describe(now))
	}
	resumed := SessionState{GroupKey: "iTerm2:3", LastActive: now, Resumed: true}
	if !strings.Contains(resumed.Describe(now), "resumed") {
		t.Errorf("resume awareness missing")
	}
}
