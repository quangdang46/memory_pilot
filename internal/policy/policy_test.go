package policy

import (
	"testing"
	"time"

	"memory_pilot/internal/classify"
	"memory_pilot/internal/model"
)

func input(level model.PressureLevel, actionable bool) Input {
	return Input{
		Pressure:       model.PressureState{Level: level, Score: 0.8},
		Forecast:       model.Forecast{Actionable: actionable, Confidence: 0.9, Calibrated: true, Bound: true},
		Classification: classify.Verdict{Bucket: classify.LeakSuspicion},
		Now:            time.Now(),
	}
}

func TestObserveModeAdvisoryOnly(t *testing.T) {
	e := NewEngine(ModeObserve)
	d := e.Evaluate(input(model.LevelCritical, true))
	hasStop := false
	for _, c := range d.Candidates {
		if c.Kind == model.ActionStopOwned {
			hasStop = true
		}
	}
	if hasStop {
		t.Errorf("observe mode must never plan L2 stop")
	}
	if len(d.Candidates) == 0 {
		t.Errorf("not always-NoAction: self-relief candidates expected")
	}
}

func TestCanaryAllowsL2(t *testing.T) {
	e := NewEngine(ModeCanary)
	d := e.Evaluate(input(model.LevelCritical, true))
	hasStop := false
	for _, c := range d.Candidates {
		if c.Kind == model.ActionStopOwned {
			hasStop = true
		}
	}
	in := input(model.LevelCritical, true)
	in.OwnedTargets = []string{"owned:4242"}
	d = e.Evaluate(in)
	hasStop = false
	for _, c := range d.Candidates {
		if c.Kind == model.ActionStopOwned && c.TargetID == "owned:4242" {
			hasStop = true
		}
	}
	if !hasStop {
		t.Errorf("canary mode with leak suspicion should consider L2 for OWNED workloads")
	}
	// no owned targets ⇒ no L2 candidate at all
	d = e.Evaluate(input(model.LevelCritical, true))
	for _, c := range d.Candidates {
		if c.Kind == model.ActionStopOwned {
			t.Errorf("L2 planned without owned target (ST-22)")
		}
	}
}

func TestUnreliablePressureNoAction(t *testing.T) {
	e := NewEngine(ModeEnforce)
	in := input(model.LevelUnknown, true)
	d := e.Evaluate(in)
	if !d.NoAction {
		t.Errorf("unknown pressure must be NoAction")
	}
}

func TestUntrustedForecastNoL2(t *testing.T) {
	e := NewEngine(ModeEnforce)
	in := input(model.LevelWarn, false)
	d := e.Evaluate(in)
	for _, c := range d.Candidates {
		if c.Kind == model.ActionStopOwned {
			t.Errorf("untrusted forecast at warn must not plan L2")
		}
	}
}

func TestBreakerSilencesKind(t *testing.T) {
	e := NewEngine(ModeEnforce)
	e.Break(model.ActionTrimOwnBuffers)
	d := e.Evaluate(input(model.LevelCritical, true))
	for _, c := range d.Candidates {
		if c.Kind == model.ActionTrimOwnBuffers {
			t.Errorf("broken kind still planned")
		}
	}
}

// A planned advisory is not "no action". The Warn branch returned early with
// NoAction at its zero value (true), so the pipeline skipped the advisory it
// had just planned. Measured in the field: 50 Prevent samples recorded
// policy_no_action with candidates=[emit_advisory] and nothing executed.
func TestWarnAdvisoryIsNotNoAction(t *testing.T) {
	e := NewEngine(ModeObserve)
	d := e.Evaluate(Input{
		Pressure: model.PressureState{Level: model.LevelWarn, Score: 0.4},
		Now:      time.Unix(0, 0),
	})
	if len(d.Candidates) == 0 {
		t.Fatal("Warn produced no candidate")
	}
	if d.NoAction {
		t.Errorf("NoAction=true with %d candidate(s): the pipeline will drop them",
			len(d.Candidates))
	}
}

// The same must hold for the Prevent/Critical branch when self-relief is
// available.
func TestPreventCandidatesAreNotNoAction(t *testing.T) {
	e := NewEngine(ModeCanary)
	d := e.Evaluate(Input{
		Pressure: model.PressureState{Level: model.LevelCritical, Score: 0.9},
		Forecast: model.Forecast{Actionable: true},
		Now:      time.Unix(0, 0),
	})
	if len(d.Candidates) == 0 {
		t.Fatal("Critical produced no candidate")
	}
	if d.NoAction {
		t.Error("NoAction=true with candidates present")
	}
}
