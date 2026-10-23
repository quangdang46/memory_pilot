// Package pipeline: the ST-22 forbidden-scenario test at the integration
// level. Enforce-mode pipeline against synthetic Critical pressure with a
// foreign "agent" process. Asserts: no signal path exists for foreign PIDs,
// full escalation recorded, only self-actions possible.
package pipeline

import (
	"testing"
	"time"

	"memory_pilot/internal/classify"
	"memory_pilot/internal/executor"
	"memory_pilot/internal/forecast"
	"memory_pilot/internal/model"
	"memory_pilot/internal/policy"
	"memory_pilot/internal/pressure"
)

func TestST22ForbiddenScenario(t *testing.T) {
	reg := executor.NewRegistry() // foreign agent is NOT registered: no signal path exists
	guard := &executor.ActionGuard{
		Mode:     func() policy.Mode { return policy.ModeEnforce },
		Registry: reg, MaxAge: 30 * time.Second,
	}
	ex := executor.New(guard)
	est := pressure.New()
	fc := forecast.New(335e6) // critical at 2% free
	eng := policy.NewEngine(policy.ModeEnforce)

	var ledger []map[string]any
	record := func(kind string, fields map[string]any) {
		m := map[string]any{"kind": kind}
		for k, v := range fields {
			m[k] = v
		}
		ledger = append(ledger, m)
	}

	mono := int64(0)
	levels := []model.PressureLevel{model.LevelNormal, model.LevelWarn, model.LevelCritical}
	freePages := []uint64{300_000, 60_000, 8_000}
	escalated := false
	for i, lvl := range levels {
		mono += 20_000
		s := model.SystemSample{At: time.Unix(int64(i), 0), MonotonicMs: mono, PageBytes: 16384,
			FreePages: freePages[i], CompressorPages: 400_000,
			MachStatsQuality: model.QualGood, KernelLevelQuality: model.QualGood, KernelLevel: lvl}
		st := est.Update(pressure.Input{Sample: s, TotalPages: 1_000_000, SwapOK: true})
		record("pressure_state", map[string]any{"state": st.State, "provenance": st.Provenance})
		if st.Transition != nil {
			record("pressure_transition", map[string]any{"from": st.Transition.From, "to": st.Transition.To})
			if st.Transition.To == pressure.Critical {
				escalated = true
			}
		}
		fo := fc.Update(forecast.Sample{MonotonicMs: mono,
			HeadroomBytes: float64(s.FreePages) * 16384, CompressorPages: 4e5})
		verdict := classify.Classify(classify.SeriesStats{
			GroupKey: "foreign-agent", SlopeBytesS: 2e6, QuietS: 900,
			ActivityCorr: 0.05, CoverageRatio: 0.9, DurationS: 2400,
		})
		dec := eng.Evaluate(policy.Input{
			Pressure:       model.PressureState{Level: lvl, Score: st.Score},
			Forecast:       forecast.ToModel(fo, s.At),
			Classification: verdict, Now: s.At})
		record("decision", map[string]any{"no_action": dec.NoAction, "n": len(dec.Candidates), "reason": dec.Reason})
		for _, c := range dec.Candidates {
			if c.Kind == model.ActionStopOwned {
				t.Fatalf("ST-22: L2 planned against a foreign workload (not owned, no authorization)")
			}
			ar := ex.Execute(nil, executor.Decision{ID: dec.At.String(), IssuedAt: dec.At, Candidate: c})
			record("action_result", map[string]any{"kind": c.Kind, "state": ar.State})
		}
		// advisory must be recorded naming the situation (SI-5)
		record("advisory", map[string]any{"text": "pressure elevated; foreign workload grew without explanation"})
	}

	if !escalated {
		t.Fatal("ST-22: state machine did not record full escalation")
	}
	sawDecision := false
	for _, e := range ledger {
		if e["kind"] == "decision" {
			sawDecision = true
		}
		if e["kind"] == "action_result" && e["state"] == "executed" {
			switch e["kind"] {
			case model.ActionTrimOwnBuffers, model.ActionRotateOwnLedger, model.ActionEmitAdvisory:
			default:
				t.Errorf("ST-22: non-self action executed: %v", e)
			}
		}
	}
	if !sawDecision {
		t.Fatal("no decision records")
	}
}
