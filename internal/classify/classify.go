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
	// A group 8 processes wide can be accumulation — but only if it also
	// grew. Every long-lived app has 10-40 processes; Chrome at 13 processes
	// growing 12 MB is a browser doing its job. Measured after a first attempt
	// fired on zsh (0 MB), Chrome (12 MB) and node (110 MB) in one window.
	MinProcessGrowth     = 8
	MinAccumulationBytes = 256 << 20
	// MinCyclingBytes is the least churn that can be called "cycling". Below
	// it the group is doing nothing measurable, and a "cache" verdict would
	// suppress whatever shape the group actually has.
	MinCyclingBytes = 32 << 20
	// MinSuspicionBytes is the least total growth that can be called suspicion.
	// A group that has been alive for 20 minutes and quiet for 10 is an idle
	// process, not a suspect; without a floor the leak branch fires on any of
	// them (measured: zsh +1 MB, esbuild +0 MB).
	MinSuspicionBytes = 128 << 20
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
	// LongGrowthBytes covers the same horizon as ReleaseBytes. Comparing the
	// window's growth with a lifetime's release is not a comparison: it read
	// "released 130 MB while growing 0 MB" for a group that had accumulated
	// 288 MB across many windows (run20, probe-children).
	LongGrowthBytes   int64
	LongPeakProcesses int
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
	// Both sides must be non-zero AND the traffic must be worth explaining:
	// "released 0 while growing 0" is a group that did nothing, not a cycling
	// cache (measured: probe-children was classified a cache purely because
	// 0 >= 0). The same mistake at a smaller scale survived that fix: a
	// subprocess group was called a cycling cache on "released 1 MB while
	// growing 0 MB" — one megabyte of churn in twenty minutes is noise, and
	// calling it a cache hides the process accumulation that was actually
	// happening (run16, 11:50).
	// Both sides on the same horizon: lifetime release vs lifetime growth.
	cLongGrow, cRel := s.LongGrowthBytes, s.ReleaseBytes
	if cLongGrow == 0 {
		cLongGrow = s.GrowthBytes // no long-horizon record yet
	}
	if cRel >= MinCyclingBytes && cLongGrow > 0 && cRel >= cLongGrow {
		v.Bucket = CacheOrRetrievable
		v.Basis = append(v.Basis,
			fmt.Sprintf("released %d MB while growing %d MB — cycling, not accumulating",
				cRel>>20, cLongGrow>>20))
		v.Confidence = 0.8
		v.Alternatives = append(v.Alternatives, WorkloadGrowth)
		return v
	}

	// subprocess accumulation — growth that arrives as more processes.
	//
	// Size matters here, not just count. Every long-lived app has 10-40
	// processes; Chrome at 13 processes growing 12 MB is a browser doing its
	// job, not accumulation. Measured after a first attempt fired on zsh
	// (0 MB), Chrome (12 MB) and node (110 MB) in one window: the bucket was
	// answering "does this app have many processes", which is always yes.
	// Growth measured over the group's whole life, not the current window: a
	// shape that spawned 20 children and then held them grows nothing in the
	// window it is judged on, and would be invisible if only that window
	// counted. Measured in run20: probe-children held 288 MB across 21
	// processes and reported growth 0 MB, so this branch could not fire.
	accGrow, accPeak := s.LongGrowthBytes, s.PeakProcessCount
	if accGrow == 0 {
		accGrow = s.GrowthBytes
	}
	if accPeak < s.LongPeakProcesses {
		accPeak = s.LongPeakProcesses
	}
	if accPeak >= MinProcessGrowth && s.ProcessCount > 0 &&
		accGrow >= MinAccumulationBytes && s.ActivityCorr < MinCorrelation {
		v.Bucket = SubprocessAccumulation
		v.Basis = append(v.Basis,
			fmt.Sprintf("process count reached %d (high-water) with growth not correlated to activity (corr %.2f)",
				accPeak, s.ActivityCorr))
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
	//
	// The group must actually have grown. Without this the branch reduced to
	// "has been alive for 20 minutes and was quiet for 10" — true of nearly
	// every idle process on the machine. Measured on a normally loaded desktop
	// (14:05): zsh +1 MB, extensionkitser +0 MB, esbuild +0 MB all became
	// leak advisories. That is precisely the warning board the owner asked
	// never to see again, produced by the fix meant to remove it.
	leakGrow := s.LongGrowthBytes
	if leakGrow == 0 {
		leakGrow = s.GrowthBytes
	}
	if s.QuietS >= QuietWindowS && s.DurationS >= MinSuspectWindowS &&
		s.CoverageRatio >= MinCoverageRatio && !s.CacheRole &&
		leakGrow >= MinSuspicionBytes {
		v.Bucket = LeakSuspicion
		v.Basis = append(v.Basis,
			fmt.Sprintf("sustained %ds ≥ %ds with no plateau", s.DurationS, MinSuspectWindowS),
			fmt.Sprintf("grew %d MB in total", leakGrow>>20),
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
	// The sentence must not contradict itself. A verdict reached on lifetime
	// growth, printed against the current window's zero, read
	// "group 2.1.310 grew by 0 MB — unexplained sustained growth", which is
	// both confusing and a reason to distrust the tool. GrowthMB carries
	// whichever horizon decided the verdict (see Verdict.GrowthMB).
	return fmt.Sprintf("group %s grew by %d MB — %s (confidence %.2f; basis: %v)",
		v.GroupKey, growthMB, label, v.Confidence, v.Basis)
}
