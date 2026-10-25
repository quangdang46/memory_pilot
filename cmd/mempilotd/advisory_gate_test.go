package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"memory_pilot/internal/classify"
	"memory_pilot/internal/ledger"
	"memory_pilot/internal/model"
)

// advisoryTexts records verdicts through a real ledger in a temp dir and reads
// back the advisories that were written.
func advisoryTexts(t *testing.T, vs []classify.Verdict) []string {
	t.Helper()
	dir := t.TempDir()
	lg, err := ledger.Open(dir, "ledger")
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()
	h := &daemonHandler{lg: lg}
	h.recordVerdicts(model.SystemSample{}, vs, nil)

	// Read the file the ledger wrote.
	var out []string
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		b, _ := os.ReadFile(filepath.Join(dir, e.Name()))
		for _, line := range strings.Split(string(b), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			var m map[string]any
			if json.Unmarshal([]byte(line), &m) != nil {
				continue
			}
			if m["kind"] == "advisory" {
				out = append(out, m["text"].(string))
			}
		}
	}
	return out
}

// A terminal group must never become an advisory, whatever shape it presents.
// Measured on this machine: wezterm-gui accumulated subprocesses across 86
// descendants holding 8.4 GB — the owner's own editors, agents and builds.
func TestTerminalGroupsProduceNoAdvisory(t *testing.T) {
	vs := []classify.Verdict{
		{GroupKey: "wezterm-gui", Bucket: classify.SubprocessAccumulation,
			Confidence: 0.6, Basis: []string{"process count reached 30"}, GrowthMB: 134},
		{GroupKey: "wezterm-gui", Bucket: classify.LeakSuspicion,
			Confidence: 0.65, Basis: []string{"sustained 2400s"}, GrowthMB: 900},
	}
	if got := advisoryTexts(t, vs); len(got) != 0 {
		t.Errorf("terminal produced %d advisories: %v", len(got), got)
	}
}

// A real application with the same shape must still be surfaced.
func TestApplicationStillAdvised(t *testing.T) {
	vs := []classify.Verdict{
		{GroupKey: "Docker Desktop", Bucket: classify.SubprocessAccumulation,
			Confidence: 0.6, Basis: []string{"process count reached 30"}, GrowthMB: 400},
	}
	if got := advisoryTexts(t, vs); len(got) == 0 {
		t.Error("Docker accumulating 400 MB produced no advisory")
	}
}
