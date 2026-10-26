package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"memory_pilot/internal/classify"
	"memory_pilot/internal/ledger"
	"memory_pilot/internal/model"
)

// The end the owner actually experiences: a verdict that deserves an advisory
// produces exactly one advisory row, the heartbeat records it as announced, and
// a second identical verdict inside the hour produces none — with the reason
// recorded. This is the whole contract in one test.
func TestAnnouncedThenHeldBack(t *testing.T) {
	dir := t.TempDir()
	lg, err := ledger.Open(dir, "ledger")
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()
	h := &daemonHandler{lg: lg, lastAdvisory: map[string]time.Time{}}

	at := time.Unix(1793000000, 0)
	s := model.SystemSample{At: at, PageBytes: 16384, FreePages: 4000,
		KernelLevel: model.LevelWarn, MachStatsQuality: model.QualGood,
		KernelLevelQuality: model.QualGood}
	v := classify.Verdict{GroupKey: "leftover-build", Bucket: classify.LeakSuspicion,
		Confidence: 0.65, Basis: []string{"sustained 1500s"}, GrowthMB: 900}

	h.recordVerdicts(s, []classify.Verdict{v}, nil) // first: announce
	s2 := s
	s2.At = at.Add(10 * time.Minute)
	h.recordVerdicts(s2, []classify.Verdict{v}, nil) // second: hold

	var advisories, decisions []map[string]any
	entries, _ := os.ReadDir(dir)
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
			switch m["kind"] {
			case "advisory":
				advisories = append(advisories, m)
			case "advisory_decision":
				decisions = append(decisions, m)
			}
		}
	}
	if len(advisories) != 1 {
		t.Errorf("%d advisories, want exactly 1 (announced once, then held)", len(advisories))
	}
	if len(decisions) != 2 {
		t.Fatalf("%d decision rows, want 2", len(decisions))
	}
	// First window announced.
	if em, _ := decisions[0]["emitted"].([]any); len(em) != 1 {
		t.Errorf("first window emitted %d, want 1", len(em))
	}
	// Second window held, with a reason naming the limit.
	sup, _ := decisions[1]["suppressed"].([]any)
	if len(sup) != 1 {
		t.Fatalf("second window suppressed %d, want 1", len(sup))
	}
	m, _ := sup[0].(map[string]any)
	if why := str(m["why"]); !strings.Contains(why, "already advised") {
		t.Errorf("hold reason %q does not say why", why)
	}
}

func str(v any) string { s, _ := v.(string); return s }
