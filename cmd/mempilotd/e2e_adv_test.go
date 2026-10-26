package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"memory_pilot/internal/classify"
	"memory_pilot/internal/executor"
	"memory_pilot/internal/ledger"
	"memory_pilot/internal/model"
	"memory_pilot/internal/pipeline"
	"memory_pilot/internal/policy"
)

// The full chain as the daemon actually runs it: recordVerdicts writes the
// advisory text, and pipeline.Decide runs policy and the executor. Before the
// fix, policy offered emit_advisory as an action and the executor reported
// "advisory recorded" 40 times while zero advisories were written — the ledger
// claimed work that never happened.
func TestAdvisorySurvivesPolicyAndExecutor(t *testing.T) {
	dir := t.TempDir()
	lg, err := ledger.Open(dir, "ledger")
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()
	reg := executor.NewRegistry()
	eng := policy.NewEngine(policy.ModeCanary)
	ex := executor.New(&executor.ActionGuard{
		Mode: func() policy.Mode { return policy.ModeCanary }, Registry: reg,
		MaxAge: time.Minute,
	})
	pipe := pipeline.New(pipeline.Config{WindowS: 300}, lg, eng, ex, nil)
	h := &daemonHandler{lg: lg, pipe: pipe, lastAdvisory: map[string]time.Time{}}

	start := time.Unix(1793000000, 0)
	// Thirty Warn ticks, as the machine produces over two and a half minutes.
	refused := 0
	for i := 0; i < 30; i++ {
		s := model.SystemSample{At: start.Add(time.Duration(i) * 5 * time.Second),
			PageBytes: 16384, FreePages: 4000, KernelLevel: model.LevelWarn,
			MachStatsQuality: model.QualGood, KernelLevelQuality: model.QualGood}
		vs := []classify.Verdict{{
			GroupKey: "leftover-build", Bucket: classify.LeakSuspicion,
			Confidence: 0.65, Basis: []string{"sustained 1500s"}, GrowthMB: 900,
		}}
		results := pipe.Decide(context.Background(), s,
			pipeline.PressureView{Level: s.KernelLevel, Score: 0.67, Quality: model.QualGood},
			model.Forecast{}, vs)
		for _, r := range results {
			// Policy must not claim to have emitted an advisory; that is
			// recordVerdicts' job.
			if r.Kind == model.ActionEmitAdvisory {
				refused++
				t.Errorf("tick %d: executor was asked to emit an advisory (%s)", i, r.State)
			}
		}
		// The real path: the advisory is written from the verdicts.
		h.recordVerdicts(s, vs, results)
	}
	if refused > 0 {
		t.Errorf("%d advisory attempts refused by the executor over 30 ticks", refused)
	}

	// And the ledger has the advisory.
	entries, _ := os.ReadDir(dir)
	found := 0
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		b, _ := os.ReadFile(filepath.Join(dir, e.Name()))
		for _, line := range strings.Split(string(b), "\n") {
			var m map[string]any
			if json.Unmarshal([]byte(line), &m) != nil {
				continue
			}
			if m["kind"] == "advisory" {
				found++
			}
		}
	}
	if found == 0 {
		t.Error("no advisory reached the ledger through the full chain")
	}
	t.Logf("advisories written over 30 ticks: %d", found)
}
