// Package forecast: MP-ALG-003. Pure (timestamps in the sample).
// One-sided conformal lower bound is what policy acts on; the point estimate
// is display-only. fallback_active ⇒ not actionable, ever.
package forecast

import (
	"math"
	"sort"
	"time"

	"memory_pilot/internal/model"
)

// Constants (forecasting.md §2; sbh-adopted starting points).
const (
	BaseAlpha   = 0.30
	MinAlpha    = 0.10
	MaxAlpha    = 0.75
	MinSamples  = 5
	HistoryCap  = 30 * time.Minute // time-based burst history (ADR-005)
	BurstMinCal = 30

	CfgWindow         = 500
	CfgWarmup         = 30
	CfgGamma          = 0.01
	CfgCoverageTarget = 0.90
	HorizonCapS       = 7200

	EprocPenaltyLog = 0.405465 // ln(1.5)
	EprocRewardLog  = -0.405465 // ln(2/3)
	EprocMin        = -5.0
	EprocMax        = 3.5
	EprocThreshold  = 20.0
	MaxRateError    = 0.3
)

type Sample struct {
	At           time.Time
	MonotonicMs  int64
	HeadroomBytes float64
	SwapUsedBytes float64
	CompressorPages float64
}

type Out struct {
	RateBytesS     float64
	AccelBytesS2   float64
	TteS           float64 // -1 = none within horizon
	Confidence     float64
	Samples        int
	FallbackActive bool
	BurstProb      float64
	BurstActive    bool
	Calibrated     bool
	LowerBoundS    float64 // conformal one-sided lower bound; what policy acts on
	GuardAlarm     bool
	FarPct         float64 // PredictionScorecard false-alarm rate
	Actionable     bool
}

type resolved struct {
	score float64 // nonconformity: relative overestimate of remaining time
}

type Forecaster struct {
	thresholdBytes float64 // Critical free-bytes level (coupled via constants module)

	// EWMA state
	prevHeadroom float64
	prevS        int64
	rate, accel  float64
	prevRateAtUpdate float64
	residualEWMA float64
	jitterEWMA   float64
	prevTte      float64
	n            int

	// burst history: (t, |rate|)
	rateHist []struct {
		s    int64
		rate float64
	}
	burstDur int

	// conformal state
	scores   []resolved
	pending  *pendingForecast
	alpha    float64 // ACI adaptive alpha, starts at coverage_target complement
	pendingN int

	// guardrail e-process
	elog float64

	// scorecard
	realized, intervened, falseAlarms, inactive int
}

type pendingForecast struct {
	tteS    float64
	loS     float64
	issuedS int64
}

func New(thresholdBytes float64) *Forecaster {
	return &Forecaster{thresholdBytes: thresholdBytes, alpha: 1 - CfgCoverageTarget, prevTte: -1}
}

func clamp(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }

// Update advances one sample; threshold crossing is forecast on headroom.
func (f *Forecaster) Update(s Sample) Out {
	out := Out{}
	dtS := 0.0
	if f.prevS > 0 {
		dtS = float64(s.MonotonicMs-f.prevS) / 1000
	}
	if dtS <= 0 {
		dtS = 0 // first sample or non-monotone; rate stays 0
	}
	f.prevS = s.MonotonicMs

	instRate := 0.0
	if dtS > 0 && f.n > 0 {
		instRate = (f.prevHeadroom - s.HeadroomBytes) / dtS // positive = consuming
	}
	// 2.1 adaptive alpha with burst damping
	burstiness := math.Abs(instRate-f.rate) / (math.Abs(f.rate) + 1)
	damping := 1 / (2*burstiness + 1)
	alpha := clamp(BaseAlpha*damping, MinAlpha, MaxAlpha)
	if f.n == 0 {
		f.rate = instRate
	} else {
		f.rate += alpha * (instRate - f.rate)
	}

	// 2.2 acceleration from smoothed rate
	if dtS > 0 && f.n > 0 {
		smAccel := (f.rate - f.prevRateAtUpdate) / dtS
		f.accel += 0.3*(smAccel-f.accel)
	}
	f.prevRateAtUpdate = f.rate
	f.prevHeadroom = s.HeadroomBytes
	f.n++
	out.Samples = f.n
	out.FallbackActive = f.n < MinSamples
	out.RateBytesS = f.rate
	out.AccelBytesS2 = f.accel

	// burst detection (time-based history)
	f.rateHist = append(f.rateHist, struct {
		s    int64
		rate float64
	}{s.MonotonicMs / 1000, math.Abs(instRate)})
	cutoff := s.MonotonicMs/1000 - int64(HistoryCap/time.Second)
	i := 0
	for i < len(f.rateHist) && f.rateHist[i].s < cutoff {
		i++
	}
	f.rateHist = f.rateHist[i:]
	out.BurstProb, out.BurstActive = f.burst()

	// 2.3 quadratic TTE with guard rails
	d := s.HeadroomBytes - f.thresholdBytes
	out.TteS = tte(f.rate, f.accel, d)

	// residual + jitter for confidence
	if f.prevTte >= 0 && !out.FallbackActive {
		resid := math.Abs(out.TteS-f.prevTte) / (math.Abs(out.TteS) + 1)
		f.residualEWMA += 0.2 * (resid - f.residualEWMA)
		jitter := math.Abs(out.TteS-f.prevTte) / math.Max(math.Max(math.Abs(out.TteS), math.Abs(f.prevTte)), 60)
		f.jitterEWMA += 0.2 * (jitter - f.jitterEWMA)
	}
	f.prevTte = out.TteS

	// conformal enrollment: resolve pending, enroll new (only when crossing imminent)
	f.resolvePending(s, out)
	if out.TteS >= 0 && out.TteS <= HorizonCapS && !out.FallbackActive {
		f.enroll(out.TteS, s.MonotonicMs/1000)
	}
	out.Calibrated = len(f.scores) >= CfgWarmup && !f.pendingActive()
	out.LowerBoundS = f.lowerBound(out.TteS)

	// 2.4 confidence
	sampleTerm := math.Min(float64(f.n)/float64(MinSamples), 1)
	residualTerm := 1 / (1 + f.residualEWMA/(math.Abs(f.rate)+1))
	stabilityTerm := 1 / (1 + 3*f.jitterEWMA)
	conf := clamp(0.3*stabilityTerm+0.2*residualTerm+0.5*sampleTerm, 0, 1)
	if out.Calibrated {
		// coverage term: penalize when empirical coverage is below target
		cov := f.empiricalCoverage()
		conf *= clamp(0.7+0.3*cov/CfgCoverageTarget, 0, 1)
	}
	out.Confidence = conf
	out.GuardAlarm = math.Exp(f.elog) >= EprocThreshold
	out.FarPct = f.far()
	out.Actionable = !out.FallbackActive && !out.GuardAlarm &&
		(!out.Calibrated || out.LowerBoundS > 0) && conf >= 0.5
	return out
}

// tte implements forecasting.md §2.3 verbatim, including the conjugate form.
func tte(v, a, d float64) float64 {
	if d <= 0 {
		return 0
	}
	if v <= 0 {
		return -1 // not consuming
	}
	if math.Abs(a) < 1e-12 {
		return d / v
	}
	disc := v*v + 2*a*d
	if disc < 0 {
		return -1
	}
	root := math.Sqrt(disc)
	if a > 0 {
		return (-v + root) / a
	}
	t := 2 * d / (v + root) // conjugate form for a<0
	if t > -v/a {           // unphysical: rate hits zero first
		return -1
	}
	return t
}

func (f *Forecaster) burst() (prob float64, active bool) {
	if len(f.rateHist) < BurstMinCal {
		return 0, false
	}
	mags := make([]float64, len(f.rateHist))
	for i, r := range f.rateHist {
		mags[i] = math.Abs(r.rate)
	}
	sort.Float64s(mags)
	median := mags[len(mags)/2]
	// sbh skips when median < 1 (disk rates, always nonzero). Headroom rates
	// sit at 0 on an idle machine, so a flat baseline uses an absolute floor
	// (1 MB/s) instead of the relative MAD bound — otherwise a single spike
	// on an idle machine would be undetectable.
	devs := make([]float64, len(mags))
	for i, m := range mags {
		devs[i] = math.Abs(m - median)
	}
	sort.Float64s(devs)
	mad := 1.4826 * devs[len(devs)/2]
	upper := median + 3*mad
	if median < 1<<10 {
		upper = 1 << 20 // 1 MB/s absolute floor on a flat baseline
	}
	last := f.rateHist[len(f.rateHist)-1]
	if math.Abs(last.rate) > upper {
		f.burstDur++
		devFactor := math.Max(math.Abs(last.rate)/median-1, 0)
		w := math.Max(math.Min(devFactor/10, 1), math.Min(float64(f.burstDur)/5, 1))
		return devFactor * w / (devFactor + 1), true
	}
	f.burstDur = 0
	return 0, false
}

// conformal: one-sided lower bound, k>n ⇒ 0 fail-safe (forecasting.md §4.1).
func (f *Forecaster) lowerBound(tteS float64) float64 {
	n := len(f.scores)
	if n < CfgWarmup {
		return tteS // warmup: point (marked untrustworthy separately)
	}
	sorted := make([]float64, n)
	for i, s := range f.scores {
		sorted[i] = s.score
	}
	sort.Float64s(sorted)
	k := int(math.Ceil(float64(n+1) * f.alpha)) // alpha = 1 - coverage target
	var q float64
	if k > n {
		q = 1.0 // fail-safe: zero guaranteed headroom after severe misses
	} else {
		q = sorted[k-1]
	}
	lo := tteS * (1 - q)
	return clamp(lo, 0, math.Max(tteS, 0))
}

func (f *Forecaster) pendingActive() bool { return f.pending != nil }

func (f *Forecaster) enroll(tteS float64, nowS int64) {
	if f.pending != nil {
		return // wait for resolution (enrollment gate: no replace-before-deadline blindness)
	}
	f.pending = &pendingForecast{tteS: tteS, issuedS: nowS}
}

// resolvePending resolves the outstanding forecast when it crosses or expires.
func (f *Forecaster) resolvePending(s Sample, out Out) {
	if f.pending == nil {
		return
	}
	age := float64(s.MonotonicMs/1000 - f.pending.issuedS)
	actualCrossed := s.HeadroomBytes <= f.thresholdBytes
	if actualCrossed {
		// predicted tte vs actual: score = relative overestimate
		score := clamp((f.pending.tteS-age)/math.Max(f.pending.tteS, 1), 0, 1)
		f.scores = append(f.scores, resolved{score: score})
		if age < f.pending.tteS*(1-0.3) {
			f.bumpEproc(true, 1.0) // under-predicted badly? actual came early ⇒ bad
			f.falseAlarms = 0
			f.realized++
		} else {
			f.bumpEproc(false, 0)
			f.realized++
		}
		f.aciUpdate(false)
		f.pending = nil
	} else if age >= HorizonCapS {
		// censored resolution: score 0 (true time ≥ prediction)
		f.scores = append(f.scores, resolved{score: 0})
		f.bumpEproc(false, 0)
		f.inactive++
		f.pending = nil
	}
	// discard-on-recovery is handled by policy via DiscardPending
}

// DiscardPending invalidates a forecast never tested because the system recovered.
func (f *Forecaster) DiscardPending() {
	if f.pending != nil {
		f.inactive++
		f.pending = nil
	}
}

// aciUpdate: adaptive conformal inference step; err=true when bound missed.
func (f *Forecaster) aciUpdate(err bool) {
	e := 0.0
	if err {
		e = 1
	}
	f.alpha += CfgGamma * ((1 - CfgCoverageTarget) - e)
	f.alpha = clamp(f.alpha, -0.5, 1)
}

func (f *Forecaster) empiricalCoverage() float64 {
	if len(f.scores) == 0 {
		return CfgCoverageTarget
	}
	// fraction of actuals that were >= the bound issued at the time
	// (approximated by score<=0 fraction — exact replay keeps this honest)
	ok := 0
	for _, s := range f.scores {
		if s.score <= 0.01 {
			ok++
		}
	}
	return float64(ok) / float64(len(f.scores))
}

// bumpEproc: directional guardrail (only under-prediction penalized, §4.2).
func (f *Forecaster) bumpEproc(bad bool, severity float64) {
	if bad {
		scale := 0.3 + 0.7*clamp(severity, 0, 1)
		f.elog += EprocPenaltyLog * scale
	} else {
		f.elog += EprocRewardLog
	}
	f.elog = clamp(f.elog, EprocMin, EprocMax)
}

// ObserveDrift feeds a rate-vs-actual observation to the guardrail
// (under-prediction only penalizes; over-prediction is the safe direction).
func (f *Forecaster) ObserveDrift(ratioActualOverPred float64) {
	if ratioActualOverPred > 1+MaxRateError {
		severity := clamp((ratioActualOverPred-(1+MaxRateError))/(1-(1+MaxRateError)+1e-9), 0, 1)
		f.bumpEproc(true, severity)
	} else {
		f.bumpEproc(false, 0)
	}
}

func (f *Forecaster) far() float64 {
	den := float64(f.realized + f.intervened + f.falseAlarms)
	if den == 0 {
		return 0
	}
	return float64(f.falseAlarms) / den * 100
}

// Scorecard records §4.2b outcomes. Intervened is NOT a false alarm.
func (f *Forecaster) Scorecard(outcome string) {
	switch outcome {
	case "intervened":
		f.intervened++
	case "false_alarm":
		f.falseAlarms++
	case "realized":
		f.realized++
	case "inactive":
		f.inactive++
	}
}

// Trustworthy is what policy consults.
func (o Out) Trustworthy() bool { return o.Actionable && o.Confidence >= 0.5 && !o.FallbackActive }

func ToModel(o Out, at time.Time) model.Forecast {
	return model.Forecast{
		At: at, RateBytesS: o.RateBytesS, EtaS: math.Round(o.TteS),
		Confidence: o.Confidence, Calibrated: o.Calibrated,
		Fallback: o.FallbackActive, Actionable: o.Actionable, Bound: o.LowerBoundS > 0,
	}
}
