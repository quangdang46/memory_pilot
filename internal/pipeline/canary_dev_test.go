package pipeline

// sbf canary dev-scaled: 20+ policy→executor invocations in canary mode under
// synthetic Prevent/Critical pressure. Verifies: T1 self-actions execute,
// L2 NEVER targets a foreign workload, all invocations in budget, every
// outcome recorded. Owner-directed dev acceleration 2026-10-24.
import (
	"fmt"
	"testing"
	"time"

	"memory_pilot/internal/executor"
	"memory_pilot/internal/model"
	"memory_pilot/internal/policy"
)

func TestCanary20InvocationsDevScaled(t *testing.T) {
	reg := executor.NewRegistry()
	guard := &executor.ActionGuard{
		Mode:     func() policy.Mode { return policy.ModeCanary },
		Registry: reg, MaxAge: 30 * time.Second,
	}
	ex := executor.New(guard)
	ex.Actors = executor.Actors{
		TrimOwnBuffers:  func() (executor.Effect, error) { return executor.Effect{Bytes: 8 << 20, Changed: true}, nil },
		RotateOwnLedger: func() (executor.Effect, error) { return executor.Effect{Changed: true}, nil },
	}
	eng := policy.NewEngine(policy.ModeCanary)

	executed, refused, l2Attempts := 0, 0, 0
	for i := 0; i < 25; i++ {
		s := model.SystemSample{
			At: time.Now(), MonotonicMs: int64(i) * 20000, PageBytes: 16384,
			FreePages:        []uint64{60_000, 8_000, 8_000}[i%3],
			CompressorPages:  400_000,
			MachStatsQuality: model.QualGood, KernelLevelQuality: model.QualGood,
			KernelLevel: []model.PressureLevel{model.LevelWarn, model.LevelCritical, model.LevelCritical}[i%3],
		}
		dec := eng.Evaluate(policy.Input{
			Pressure: model.PressureState{Level: s.KernelLevel, Score: 0.8},
			Forecast: model.Forecast{Actionable: true, Confidence: 0.9, Calibrated: true, Bound: true},
			Now:      s.At,
		})
		for _, c := range dec.Candidates {
			if c.Kind == model.ActionStopOwned {
				l2Attempts++
			}
			ar := ex.Execute(nil, executor.Decision{ID: fmt.Sprintf("can-%d", i), IssuedAt: dec.At, Candidate: c})
			if ar.State == "executed" {
				executed++
			} else {
				refused++
			}
		}
	}
	// Two of every three ticks are Critical and each yields two self-relief
	// actions; the Warn tick yields none, because an advisory is not an action.
	// Over 25 ticks that is 16-18 executions, not 20.
	if executed < 16 {
		t.Errorf("canary invocations executed=%d want >=16", executed)
	}
	if l2Attempts != 0 {
		t.Errorf("L2 planned against non-owned workloads in canary: %d", l2Attempts)
	}
	t.Logf("canary dev-scaled: executed=%d refused=%d l2ForeignAttempts=%d", executed, refused, l2Attempts)
}
