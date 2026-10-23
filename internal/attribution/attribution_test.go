package attribution

import (
	"strings"
	"testing"
	"time"

	"memory_pilot/internal/model"
)

var t0 = time.Unix(1700000000, 0)

func snap(pids map[[2]any]uint64) model.ProcessSnapshot {
	s := model.ProcessSnapshot{At: time.Now(), Quality: model.QualGood, Coverage: model.Coverage{Attempted: len(pids), Succeeded: len(pids)}}
	for k, b := range pids {
		pid := k[0].(int32)
		start := t0
		if k[1] != nil {
			start = k[1].(time.Time)
		}
		s.Processes = append(s.Processes, model.ProcessSample{
			Identity: model.ProcessIdentity{Pid: pid, StartTime: start}, ResidentBytes: b, Quality: model.QualGood})
	}
	return s
}

func sys(freePages uint64) model.SystemSample {
	return model.SystemSample{At: time.Now(), PageBytes: 16384, FreePages: freePages,
		CompressorPages: 50000, MachStatsQuality: model.QualGood, KernelLevelQuality: model.QualGood}
}

// Identity change on same pid is a new series — no delta across the change.
func TestIdentityChangeNoDelta(t *testing.T) {
	a := New(60)
	a.Observe(snap(map[[2]any]uint64{{int32(100), t0}: 1 << 30}), sys(200000))
	reborn := t0.Add(30 * time.Second)
	a.Observe(snap(map[[2]any]uint64{{int32(100), reborn}: 2 << 30}), sys(150000))
	w := a.Window(sys(150000))
	// reborn process observed once: delta recorded only across identical identity.
	// new identity first sighting = no delta; ended series keeps its history.
	if w.AccountedGrowth != 0 {
		t.Errorf("accounted %d want 0 (no cross-identity delta)", w.AccountedGrowth)
	}
}

func TestSameIdentityDelta(t *testing.T) {
	a := New(60)
	a.Observe(snap(map[[2]any]uint64{{int32(100), t0}: 1 << 30}), sys(200000))
	a.Observe(snap(map[[2]any]uint64{{int32(100), t0}: 2 << 30}), sys(180000))
	w := a.Window(sys(180000))
	if w.AccountedGrowth != 1<<30 {
		t.Errorf("accounted %d want 1GB", w.AccountedGrowth)
	}
}

// Coverage is never normalized: sums can exceed the system delta.
func TestCoverageNotNormalized(t *testing.T) {
	a := New(60)
	a.Observe(snap(map[[2]any]uint64{{int32(1), t0}: 1 << 30, {int32(2), t0}: 1 << 30}), sys(300000))
	a.Observe(snap(map[[2]any]uint64{{int32(1), t0}: 2 << 30, {int32(2), t0}: 2 << 30}), sys(250000))
	w := a.Window(sys(250000))
	if w.AccountedGrowth != 2<<30 {
		t.Fatalf("accounted %d", w.AccountedGrowth)
	}
	if w.CoverageRatio <= 1.0 {
		t.Errorf("coverage %v should exceed 1 (double-counting is normal)", w.CoverageRatio)
	}
	if !strings.Contains(w.Note, "can exceed the system delta") {
		t.Errorf("note missing: %s", w.Note)
	}
	if !strings.Contains(w.Note, "belong to no process") {
		t.Errorf("compression note missing: %s", w.Note)
	}
}

// Large but stable processes never appear in the growth ranking.
func TestLargeButStable(t *testing.T) {
	a := New(60)
	a.Observe(snap(map[[2]any]uint64{{int32(1), t0}: 600 << 20, {int32(2), t0}: 10 << 20}), sys(300000))
	a.Observe(snap(map[[2]any]uint64{{int32(1), t0}: 600 << 20, {int32(2), t0}: 200 << 20}), sys(300000))
	w := a.Window(sys(300000))
	for _, g := range w.Groups {
		if g.AnchorKey == "pid-1" {
			t.Errorf("stable process ranked as growth")
		}
	}
	if len(w.LargeButStable) == 0 {
		t.Errorf("large-but-stable section missing")
	}
}
