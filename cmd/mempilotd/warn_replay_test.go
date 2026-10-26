package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"memory_pilot/internal/ledger"
	"memory_pilot/internal/model"
	"memory_pilot/internal/pipeline"
)

// The false-positive advisories of 2026-10-26 happened at Warn pressure, when
// the advisory gate is open. This drives the real daemon path — attribution,
// window close, classification, recordVerdicts — with snapshots that reproduce
// the coverage flip that caused them, at kernel level 2, through a real ledger.
//
// It exists because "no advisories for twelve minutes at Normal" is not
// evidence: at Normal the gate never opens, so nothing could have fired.
func TestNeverAdvisesOnACoverageFlipAtWarn(t *testing.T) {
	dir := t.TempDir()
	lg, err := ledger.Open(dir, "ledger")
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()

	pipe := pipeline.New(pipeline.Config{WindowS: 300}, lg, nil, nil, nil)
	h := &daemonHandler{lg: lg, pipe: pipe, lastAdvisory: map[string]time.Time{}}

	start := time.Unix(1793000000, 0)
	sysSample := func(at time.Time) model.SystemSample {
		return model.SystemSample{
			At: at, PageBytes: 16384, FreePages: 20000, // ~305 MB free: Warn
			KernelLevel:      model.LevelWarn, // 2
			MachStatsQuality: model.QualGood, KernelLevelQuality: model.QualGood,
		}
	}
	// The Code group, alternating between 1 and 12 visible members exactly as
	// measured, with a genuine slow climb underneath.
	snap := func(i int, at time.Time) model.ProcessSnapshot {
		visible := 1
		if i%2 == 1 {
			visible = 12
		}
		grow := uint64((100 + i*2) << 20)
		procs := []model.ProcessSample{{
			Identity: model.ProcessIdentity{Pid: 900, Ppid: 1, StartTime: start.Add(-time.Hour),
				Executable: "/Applications/Visual Studio Code.app/Contents/MacOS/Code"},
			FootprintBytes: grow, ResidentBytes: grow, Quality: model.QualGood,
		}}
		for j := 0; j < visible; j++ {
			procs = append(procs, model.ProcessSample{
				Identity: model.ProcessIdentity{Pid: int32(1000 + j), Ppid: 900,
					StartTime:  start.Add(-time.Hour),
					Executable: "/Applications/Visual Studio Code.app/Contents/Frameworks/Code Helper.app/Contents/MacOS/Code Helper"},
				FootprintBytes: grow, ResidentBytes: grow, Quality: model.QualGood,
			})
		}
		return model.ProcessSnapshot{At: at, Processes: procs,
			Quality:  model.QualGood,
			Coverage: model.Coverage{Attempted: len(procs), Succeeded: len(procs)}}
	}

	for i := 0; i < 90; i++ { // 22.5 minutes of 15 s ticks
		at := start.Add(time.Duration(i) * 15 * time.Second)
		s := sysSample(at)
		h.mu.Lock()
		h.lastSample = s
		h.mu.Unlock()
		pipe.ObserveProcesses(snap(i, at), s)
		if i%20 == 19 { // close a window every 300 s
			vs := pipe.CloseWindow(s)
			h.recordVerdicts(s, vs, nil)
		}
	}

	// Read back every advisory the ledger received.
	var advisories []string
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
				advisories = append(advisories, m["text"].(string))
			}
		}
	}
	for _, a := range advisories {
		t.Errorf("advisory at Warn on a coverage flip:\n  %s", a)
	}
	// Guard: the test must actually have exercised an open gate, or it proves
	// nothing. Warn pressure plus a live attribution is what arms the path.
	if n := pipe.Attr.QuietBoundS(); n <= 0 {
		t.Fatalf("attribution recorded nothing (%ds) — the run did not exercise the path", n)
	}
}
