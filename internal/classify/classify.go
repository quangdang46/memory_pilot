// Package classify: MP-ALG-005 — five buckets, label + mandatory basis,
// never a bare claim (SI-5). "leak_suspicion", never "leak".
package classify

import "fmt"

type Bucket string

const (
	WorkloadGrowth         Bucket = "workload_growth"
	CacheOrRetrievable     Bucket = "cache_or_retrievable"
	SubprocessAccumulation Bucket = "subprocess_accumulation"
	LeakSuspicion          Bucket = "leak_suspicion"
	Unknown                Bucket = "unknown"
)

// Constants (leak-suspicion.md §2/§3).
const (
	QuietWindowS      = 600  // 10 min
	PlateauWindowS    = 900  // 15 min
	MinSuspectWindowS = 1200 // 20 min
	MinCoverageRatio  = 0.3
	MinCorrelation    = 0.5
	MinProcessGrowth  = 8 // a group this many processes wide is accumulation, not one leak
)

type SeriesStats struct {
	GroupKey      string
	SlopeBytesS   float64 // median pairwise slope (robust, not OLS)
	Accel         float64
	PlateauS      int64   // time since last growth (0 if still growing)
	ActivityCorr  float64 // sign-agreement rate + rank corr composite
	ActivityS     int64   // seconds of observed activity in window
	QuietS        int64   // seconds with flat activity while growing
	CacheRole     bool
	CeilingBytes  float64 // 0 = none known
	DurationS     int64
	CoverageRatio float64

	// GrowthBytes is gross positive growth in the window, the quantity
	// ReleaseBytes is compared against.
	GrowthBytes int64
	// ReleaseBytes is how much this group freed in the window. A group that
	// frees at least as much as it grows is cycling, not accumulating —
	// measured on the target machine, Chrome released 1.4-3.1 GB in windows
	// where it grew 1.4-2.4 GB, and was labelled a leak four times running
	// because this field did not exist.
	ReleaseBytes int64
	// ProcessCount is how many processes make up the group. Growth that
	// arrives as more processes rather than as a bigger one is its own
	// bucket and its own explanation.
	ProcessCount int
	// PeakProcessCount is the high-water mark over the group's history, so a
	// rising count is distinguishable from a static population.
	PeakProcessCount int
}

type Verdict struct {
	GroupKey     string
	Bucket       Bucket
	Basis        []string // mandatory — no bare claims
	Confidence   float64
	Alternatives []Bucket // what we could NOT exclude
	GrowthMB     int64    // growth behind this verdict; 0 when unknown
}

// Classify one group's series stats. Every path returns a basis; unknown is
// honest (coverage/window/evidence failures are reasons, not silence).
func Classify(s SeriesStats) Verdict {
	v := Verdict{GroupKey: s.GroupKey, Alternatives: []Bucket{}}
	if s.CoverageRatio < MinCoverageRatio {
		v.Bucket = Unknown
		v.Basis = append(v.Basis, fmt.Sprintf("coverage %.2f below %.2f — visibility too low to classify", s.CoverageRatio, MinCoverageRatio))
		return v
	}
	if s.SlopeBytesS <= 0 {
		v.Bucket = Unknown
		v.Basis = append(v.Basis, "no sustained growth in window")
		return v
	}

	// cycling (cache) test — a group that frees as much as it grows is not
	// accumulating anything, whatever its gross growth number says. This is
	// checked first because it is a direct measurement of what happened, not
	// an inference from the shape of a curve.
	// Both sides must be non-zero: "released 0 while growing 0" is a group
	// that did nothing, not a cycling cache. Measured: probe-children with
	// zero growth and zero release was classified a cache purely because
	// 0 >= 0.
	if s.ReleaseBytes > 0 && s.GrowthBytes > 0 && s.ReleaseBytes >= s.GrowthBytes {
		v.Bucket = CacheOrRetrievable
		v.Basis = append(v.Basis,
			fmt.Sprintf("released %d MB while growing %d MB — cycling, not accumulating",
				s.ReleaseBytes>>20, s.GrowthBytes>>20))
		v.Confidence = 0.8
		v.Alternatives = append(v.Alternatives, WorkloadGrowth)
		return v
	}

	// subprocess accumulation — growth that arrives as more processes.
	if s.PeakProcessCount >= MinProcessGrowth && s.ProcessCount > 0 &&
		s.ActivityCorr < MinCorrelation {
		v.Bucket = SubprocessAccumulation
		v.Basis = append(v.Basis,
			fmt.Sprintf("process count reached %d (high-water) with growth not correlated to activity (corr %.2f)",
				s.PeakProcessCount, s.ActivityCorr))
		v.Confidence = 0.6
		v.Alternatives = append(v.Alternatives, WorkloadGrowth, LeakSuspicion)
		return v
	}

	// plateau (cache) test
	if s.PlateauS >= PlateauWindowS || (s.CeilingBytes > 0 && !exceedsCeiling(s)) {
		v.Bucket = CacheOrRetrievable
		v.Basis = append(v.Basis, fmt.Sprintf("plateau %ds ≥ %ds after growth", s.PlateauS, PlateauWindowS))
		if s.CacheRole {
			v.Basis = append(v.Basis, "process has known cache role")
		}
		v.Confidence = 0.7
		v.Alternatives = append(v.Alternatives, WorkloadGrowth)
		return v
	}

	// workload correlation
	if s.ActivityCorr >= MinCorrelation || s.ActivityS > 0 && s.QuietS == 0 {
		v.Bucket = WorkloadGrowth
		v.Basis = append(v.Basis, fmt.Sprintf("growth co-moves with activity (corr %.2f; active %ds, quiet 0s)", s.ActivityCorr, s.ActivityS))
		v.Confidence = 0.6 + 0.3*s.ActivityCorr
		v.Alternatives = append(v.Alternatives, LeakSuspicion)
		return v
	}

	// unexplained sustained growth → suspicion criteria (ALL must hold)
	if s.QuietS >= QuietWindowS && s.DurationS >= MinSuspectWindowS && s.CoverageRatio >= MinCoverageRatio && !s.CacheRole {
		v.Bucket = LeakSuspicion
		v.Basis = append(v.Basis,
			fmt.Sprintf("sustained %ds ≥ %ds with no plateau", s.DurationS, MinSuspectWindowS),
			fmt.Sprintf("activity flat for %ds ≥ %ds (uncorrelated)", s.QuietS, QuietWindowS),
			"not in cache-role table",
			fmt.Sprintf("coverage %.2f adequate", s.CoverageRatio))
		v.Confidence = 0.65
		v.Alternatives = append(v.Alternatives, WorkloadGrowth, CacheOrRetrievable, SubprocessAccumulation)
		return v
	}

	v.Bucket = Unknown
	if s.QuietS < QuietWindowS {
		v.Basis = append(v.Basis, fmt.Sprintf("quiet period %ds < %ds — uncorrelation not established", s.QuietS, QuietWindowS))
	}
	if s.DurationS < MinSuspectWindowS {
		v.Basis = append(v.Basis, fmt.Sprintf("window %ds < %ds — too short", s.DurationS, MinSuspectWindowS))
	}
	return v
}

func exceedsCeiling(s SeriesStats) bool { return s.SlopeBytesS > 0 }

// PhraseTest — G-P3: rendered language must never claim conclusions.
// Returns violations for forbidden phrases in any report text.
func PhraseTest(text string) []string {
	var bad []string
	for _, p := range []string{
		"is leaking", "was leaking", "leaks memory", "the cause is", "caused by process",
		"100% of growth", "accounts for 100%", "safe to kill", "recommend killing",
	} {
		if containsFold(text, p) {
			bad = append(bad, p)
		}
	}
	return bad
}

func containsFold(s, sub string) bool {
	n := len(sub)
	for i := 0; i+n <= len(s); i++ {
		if equalFold(s[i:i+n], sub) {
			return true
		}
	}
	return false
}

func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if 'A' <= ca && ca <= 'Z' {
			ca += 32
		}
		if 'A' <= cb && cb <= 'Z' {
			cb += 32
		}
		if ca != cb {
			return false
		}
	}
	return true
}

// RenderVerdict produces report text that passes PhraseTest by construction.
func RenderVerdict(v Verdict, growthMB int64) string {
	label := map[Bucket]string{
		WorkloadGrowth:         "growth correlated with work",
		CacheOrRetrievable:     "growth consistent with caching behavior",
		SubprocessAccumulation: "growth from accumulating subprocesses",
		LeakSuspicion:          "unexplained sustained growth (suspicion)",
		Unknown:                "insufficient evidence to classify",
	}[v.Bucket]
	return fmt.Sprintf("group %s grew by %d MB — %s (confidence %.2f; basis: %v)",
		v.GroupKey, growthMB, label, v.Confidence, v.Basis)
}
