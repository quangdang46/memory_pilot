// Package policy: expected-benefit reasoning (bead 3r5). Pure: no I/O.
// Analysis cannot act (SI-2) — Evaluate returns values only.
package policy

import (
	"fmt"
	"time"

	"memory_pilot/internal/classify"
	"memory_pilot/internal/model"
)

type Mode string

const (
	ModeObserve Mode = "observe"
	ModeCanary  Mode = "canary"
	ModeEnforce Mode = "enforce"
)

// Mode rank for allowability comparisons.
func rank(m Mode) int {
	switch m {
	case ModeObserve:
		return 0
	case ModeCanary:
		return 1
	case ModeEnforce:
		return 2
	}
	return -1
}

// Per-class required modes (ADR-013/ADR-014).
var RequiredMode = map[model.ActionKind]Mode{
	model.ActionTrimOwnBuffers:  ModeObserve,
	model.ActionRotateOwnLedger: ModeObserve,
	model.ActionEmitAdvisory:    ModeObserve,
	model.ActionDeferLaunch:     ModeObserve, // T2 advisory+deferral
	model.ActionStopOwned:       ModeCanary,  // L2: canary before enforce
}

type Engine struct {
	Mode Mode
	// breaker state (fed by verification outcomes)
	broken map[model.ActionKind]bool
}

func NewEngine(mode Mode) *Engine {
	return &Engine{Mode: mode, broken: map[model.ActionKind]bool{}}
}

func (e *Engine) Break(kind model.ActionKind)      { e.broken[kind] = true }
func (e *Engine) IsBroken(k model.ActionKind) bool { return e.broken[k] }

type Input struct {
	Pressure       model.PressureState
	Forecast       model.Forecast
	Classification classify.Verdict
	Now            time.Time
	// OwnedTargets: pre-authorized managed-workload tokens from the spawn
	// registry. L2 can ONLY target these — foreign processes never appear.
	OwnedTargets []string
}

// Evaluate: NoAction unless pressure+forecast justify a candidate from the
// closed allowlist. Never "always NoAction"; never acts without confidence.
func (e *Engine) Evaluate(in Input) model.Decision {
	d := model.Decision{At: in.Now, NoAction: true}
	if in.Pressure.Level == model.LevelUnknown {
		d.Reason = "pressure unreliable — no action possible (SI-4/SI-5)"
		return d
	}
	switch in.Pressure.Level {
	case model.LevelNormal, model.LevelUnknown:
		d.Reason = "no pressure"
		return d
	case model.LevelWarn:
		// A candidate exists, so this is not "no action". Returning early with
		// NoAction still true (the zero value) meant the advisory was planned
		// and then silently discarded by the pipeline — measured in the field:
		// 50 Prevent samples produced 'policy_no_action' with
		// candidates=["emit_advisory"] and no action ever executed.
		d.Reason = "watch/prevent: advisory only in this state"
		d.Candidates = append(d.Candidates, model.Candidate{Kind: model.ActionEmitAdvisory})
		d.NoAction = false
		return d
	}
	// Prevent/Critical: expected-benefit reasoning
	if in.Forecast.Actionable || in.Pressure.Level == model.LevelCritical {
		// T1 self-relief is always in scope
		if !e.IsBroken(model.ActionTrimOwnBuffers) {
			d.Candidates = append(d.Candidates,
				model.Candidate{Kind: model.ActionTrimOwnBuffers, ExpectBytes: 32 << 20})
			d.Candidates = append(d.Candidates,
				model.Candidate{Kind: model.ActionRotateOwnLedger, ExpectBytes: 0})
		}
		// L2 managed stop ONLY for owned, pre-authorized workloads with a
		// suspicion + canary+. No owned target ⇒ no L2 candidate exists (ST-22).
		if len(in.OwnedTargets) > 0 && in.Classification.Bucket == classify.LeakSuspicion &&
			rank(e.Mode) >= rank(ModeCanary) && !e.IsBroken(model.ActionStopOwned) {
			for _, tgt := range in.OwnedTargets {
				d.Candidates = append(d.Candidates, model.Candidate{Kind: model.ActionStopOwned, TargetID: tgt})
			}
		}
		// T3 detect+recommend: recommendation is a report, never an executor path
		d.Reason = fmt.Sprintf("pressure=%d score=%.2f; %d candidates", in.Pressure.Level, scoreOf(in), len(d.Candidates))
		d.NoAction = len(d.Candidates) == 0
	} else {
		d.Reason = "forecast not actionable (warming/untrusted) — observe only"
	}
	return d
}

func scoreOf(in Input) float64 { return in.Pressure.Score }
