// Package pressure: MP-ALG-002. Pure: no I/O, no clock reads (timestamps in
// the sample). Kernel level is authoritative; trend may elevate Normal→Watch
// but never lower a kernel-reported state; fail-safe to Unknown (SI-4).
package pressure

import (
	"fmt"
	"math"

	"memory_pilot/internal/model"
)

// Constants (constants.md §1; P = provisional, retuned on traces per Q-3).
const (
	FallbackFreeCriticalPct = 2.0
	FallbackFreePreventPct  = 5.0
	FallbackFreeWatchPct    = 12.0
	FallbackCompPreventPct  = 10.0
	FallbackCompWatchPct    = 6.0
	WatchTrendThreshold     = 0.35
	ScoreWeightLevel        = 0.50
	ScoreWeightFree         = 0.20
	ScoreWeightComp         = 0.15
	ScoreWeightSwap         = 0.10
	ScoreWeightDisk         = 0.05
	FreeRefPct              = 20.0
	CompRefPct              = 20.0
	DiskRefPct              = 15.0
	HysteresisPct           = 5.0
	MinDwellS               = 15
)

// State — five states; Unknown is real, never an alias for Normal.
type State string

const (
	Normal   State = "Normal"
	Watch    State = "Watch"
	Prevent  State = "Prevent"
	Critical State = "Critical"
	Unknown  State = "Unknown"
)

type Input struct {
	Sample        model.SystemSample
	TotalPages    uint64
	SwapUsedBytes uint64
	SwapOK        bool
	DiskFreePct   float64
	DiskOK        bool
	TrendRisk     float64 // 0..1 rising-pressure trend score (from forecast layer)
}

type StateOut struct {
	State      State
	Provenance string // "kernel" | "derived" | "unknown"
	Score      float64
	ScoreReliable bool
	DwellS     int64
	Reasons    []string
	Transition *Transition
}

type Transition struct {
	From, To State
	Reason   string
}

type Estimator struct {
	last        State
	candidate   State
	candSinceS  int64
	lastChangeS int64
	lastSampleS int64
	inIncident  bool
	peak        State
}

func New() *Estimator { return &Estimator{last: Unknown} }

func rank(s State) int {
	switch s {
	case Normal:
		return 0
	case Watch:
		return 1
	case Prevent:
		return 2
	case Critical:
		return 3
	}
	return -1
}

// Update advances the state machine one sample.
func (e *Estimator) Update(in Input) StateOut {
	out := e.derive(in)
	dt := in.Sample.MonotonicMs/1000 - e.lastSampleS
	if e.lastSampleS > 0 && dt < 0 {
		dt = 0
	}
	e.lastSampleS = in.Sample.MonotonicMs / 1000

	// dwell gate: Critical escalates immediately, others need MinDwell
	if out.State != e.last {
		if e.candidate != out.State {
			e.candidate = out.State
			e.candSinceS = in.Sample.MonotonicMs / 1000
		}
		dwelled := (in.Sample.MonotonicMs/1000-e.candSinceS) >= MinDwellS
		immediate := rank(out.State) > rank(e.last) && out.State == Critical
		if dwelled || immediate || e.last == Unknown {
			out.Transition = &Transition{From: e.last, To: out.State, Reason: out.Provenance}
			e.last = out.State
			e.lastChangeS = in.Sample.MonotonicMs / 1000
		} else {
			out.State = e.last // dwell not satisfied
		}
	} else {
		e.candidate = out.State
	}
	out.DwellS = in.Sample.MonotonicMs/1000 - e.candSinceS

	// SI-4: no recovery on degraded evidence
	if rank(out.State) < rank(e.last) && qualityBad(in.Sample) {
		out.State = e.last
		out.Reasons = append(out.Reasons, "recovery blocked: degraded evidence (SI-4)")
	}
	// monotone within incident
	if rank(out.State) >= rank(Prevent) {
		e.inIncident = true
		if rank(out.State) > rank(e.peak) {
			e.peak = out.State
		}
	} else if out.State == Normal {
		e.inIncident = false
		e.peak = Normal
	}
	return out
}

func qualityBad(s model.SystemSample) bool {
	return s.MachStatsQuality == model.QualMissing || s.KernelLevelQuality == model.QualMissing
}

// derive implements §4 (primary kernel path, fallback, unknown).
func (e *Estimator) derive(in Input) StateOut {
	s := in.Sample
	freePct := pct(float64(s.FreePages), float64(in.TotalPages))
	compPct := pct(float64(s.CompressorPages), float64(in.TotalPages))

	switch {
	case s.KernelLevelQuality == model.QualGood && s.KernelLevel != model.LevelUnknown:
		st := map[model.PressureLevel]State{
			model.LevelNormal: Normal, model.LevelWarn: Prevent, model.LevelCritical: Critical,
		}[s.KernelLevel]
		if st == Normal && in.TrendRisk >= WatchTrendThreshold {
			st = Watch
		}
		return StateOut{State: st, Provenance: "kernel",
			Score: e.score(st, freePct, compPct, in), ScoreReliable: true,
			Reasons: []string{fmt.Sprintf("kernel_level=%d", s.KernelLevel)}}

	case s.MachStatsQuality == model.QualGood:
		// fallback path §4.2 — provenance=derived
		swapHot := in.SwapOK && in.SwapUsedBytes > 0
		var st State
		switch {
		case freePct < FallbackFreeCriticalPct && swapHot:
			st = Critical
		case freePct < FallbackFreePreventPct && (compPct >= FallbackCompPreventPct || swapHot):
			st = Prevent
		case freePct < FallbackFreeWatchPct || compPct >= FallbackCompWatchPct:
			st = Watch
		default:
			st = Normal
		}
		return StateOut{State: st, Provenance: "derived",
			Score: e.score(st, freePct, compPct, in), ScoreReliable: true,
			Reasons: []string{fmt.Sprintf("fallback free=%.1f%% comp=%.1f%%", freePct, compPct)}}

	default:
		return StateOut{State: Unknown, Provenance: "unknown",
			Score: e.score(Unknown, freePct, compPct, in), ScoreReliable: false,
			Reasons: []string{"insufficient evidence: kernel level + VM stats unavailable"}}
	}
}

func pct(v, total float64) float64 {
	if total <= 0 {
		return 0
	}
	return v / total * 100
}

func clamp(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }

func (e *Estimator) score(st State, freePct, compPct float64, in Input) float64 {
	lc := 0.5
	switch st {
	case Normal:
		lc = 0.0
	case Watch:
		lc = 0.33
	case Prevent:
		lc = 0.66
	case Critical:
		lc = 1.0
	}
	freeScarcity := clamp((FreeRefPct-freePct)/FreeRefPct, 0, 1)
	compLoad := clamp(compPct/CompRefPct, 0, 1)
	swapPressure := 0.0
	if in.SwapOK && in.TotalPages > 0 {
		swapPressure = clamp(float64(in.SwapUsedBytes)/float64(in.TotalPages*in.Sample.PageBytes), 0, 1)
	}
	disk := 0.0
	if in.DiskOK {
		disk = clamp((DiskRefPct-in.DiskFreePct)/DiskRefPct, 0, 1)
	}
	return clamp(ScoreWeightLevel*lc+ScoreWeightFree*freeScarcity+ScoreWeightComp*compLoad+
		ScoreWeightSwap*swapPressure+ScoreWeightDisk*disk, 0, 1)
}
