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

// The question the whole product exists to answer, asked end to end: when a
// real leak grows on a machine under real pressure, does the board notice, and
// does the owner see it without asking?
//
// Everything below is the daemon's own path — attribution, window close,
// classification, policy, executor, recordVerdicts, a real ledger — driven by
// the slow-leak trace's pressure shape, which falls from 4.9 GB free to 48 MB
// and crosses Normal -> Warn exactly as the kernel reports it.
//
// The observation runs 1500 s because the classifier demands 1200 s of
// evidence before it will call anything a suspicion. The first version of this
// test ran the trace's own 595 s and could not have passed — no verdict is
// even reachable at that length, which is a property of the design, not a bug.
func TestTheBoardFiresOnARealLeakUnderRealPressure(t *testing.T) {
	assertBoardFires(t, "../../traces/slow-leak.json", 1500*time.Second)
}

// Same fixture, but the leak stops growing and holds. The board must stay
// silent: a plateau is cache, not a leak, and announcing it is the warning
// fatigue the owner asked never to see again.
func TestTheBoardStaysSilentOnAPlateau(t *testing.T) {
	trace := loadTrace(t, "../../traces/slow-leak.json")
	dir := t.TempDir()
	lg, err := ledger.Open(dir, "ledger")
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()
	pipe := pipeline.New(pipeline.Config{WindowS: 300}, lg, nil, nil, nil)
	h := &daemonHandler{lg: lg, pipe: pipe, lastAdvisory: map[string]time.Time{}}

	start := time.Unix(1793000000, 0)
	const leakExe = "/Users/someone/builds/orphaned-batch"
	leakBytes := uint64(64 << 20)
	var warnTicks int

	for elapsed := time.Duration(0); elapsed < 1500*time.Second; elapsed += 5 * time.Second {
		sys := sampleAt(trace, start, elapsed)
		if sys.KernelLevel >= model.LevelWarn {
			warnTicks++
		}
		if elapsed < 200*time.Second {
			leakBytes += 6 << 20 // grows briefly
		}
		// from 200 s on: flat. It holds what it took.
		pipe.ObserveProcesses(snapshotAt(start, sys.At, leakExe, leakBytes), sys)
		closeAndRecord(t, h, pipe, sys, elapsed)
	}
	if warnTicks == 0 {
		t.Fatal("the trace never reached Warn — proving nothing about silence")
	}
	if n := countAdvisories(t, lg); n != 0 {
		t.Errorf("%d advisory(ies) fired for a group that stopped growing", n)
	}
}

func assertBoardFires(t *testing.T, tracePath string, observe time.Duration) {
	t.Helper()
	trace := loadTrace(t, tracePath)
	dir := t.TempDir()
	lg, err := ledger.Open(dir, "ledger")
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()

	pipe := pipeline.New(pipeline.Config{WindowS: 300}, lg, nil, nil, nil)
	h := &daemonHandler{lg: lg, pipe: pipe, lastAdvisory: map[string]time.Time{}}

	start := time.Unix(1793000000, 0)
	const leakExe = "/Users/someone/builds/orphaned-batch"
	leakBytes := uint64(64 << 20)
	var warnTicks int

	for elapsed := time.Duration(0); elapsed < observe; elapsed += 5 * time.Second {
		sys := sampleAt(trace, start, elapsed)
		if sys.KernelLevel >= model.LevelWarn {
			warnTicks++
		}
		leakBytes += 6 << 20 // 1.2 MB/s, no CPU — the shape the board exists for
		pipe.ObserveProcesses(snapshotAt(start, sys.At, leakExe, leakBytes), sys)
		closeAndRecord(t, h, pipe, sys, elapsed)
	}

	if warnTicks == 0 {
		t.Fatal("the trace never reached Warn — the gate never opened")
	}
	if pipe.Attr.ProcessCount() == 0 {
		t.Fatal("attribution saw no processes")
	}

	advisories := advisoriesOf(t, lg)
	if len(advisories) == 0 {
		t.Fatalf("BOARD DID NOT FIRE: %v observed, %d ticks at Warn, a %d MB leak, no advisory",
			observe, warnTicks, leakBytes>>20)
	}
	for _, a := range advisories {
		if g, _ := a["group"].(string); strings.Contains(g, "orphaned-batch") {
			t.Logf("advisory: %s", a["text"])
			return
		}
	}
	t.Fatalf("advisories fired but none named the leak: %v", advisories)
}

// sampleAt projects the trace's pressure shape onto an arbitrary elapsed time.
// The trace itself covers 595 s; a leak verdict needs 1200 s of evidence, so
// the last sample's pressure is held while the observation continues.
func sampleAt(trace traceFile, start time.Time, elapsed time.Duration) model.SystemSample {
	ms := elapsed.Seconds() * 1000
	var s struct {
		MonoMs      float64 `json:"mono_ms"`
		FreeBytes   float64 `json:"free_bytes"`
		KernelLevel int     `json:"kernel_level"`
		PageBytes   uint64  `json:"page_bytes"`
	}
	s = trace.Samples[len(trace.Samples)-1]
	for _, c := range trace.Samples {
		if c.MonoMs >= ms {
			s = c
			break
		}
	}
	page := s.PageBytes
	if page == 0 {
		page = 16384
	}
	return model.SystemSample{
		At: start.Add(elapsed), PageBytes: page,
		FreePages:        uint64(s.FreeBytes) / page,
		KernelLevel:      model.PressureLevel(s.KernelLevel),
		MachStatsQuality: model.QualGood, KernelLevelQuality: model.QualGood,
	}
}

func snapshotAt(start, at time.Time, leakExe string, leakBytes uint64) model.ProcessSnapshot {
	procs := []model.ProcessSample{{
		Identity: model.ProcessIdentity{Pid: 4242, Ppid: 1,
			StartTime: start.Add(-2 * time.Hour), Executable: leakExe},
		FootprintBytes: leakBytes, ResidentBytes: leakBytes, Quality: model.QualGood,
	}}
	for j := 0; j < 40; j++ {
		procs = append(procs, model.ProcessSample{
			Identity: model.ProcessIdentity{Pid: int32(5000 + j), Ppid: 1,
				StartTime:  start.Add(-3 * time.Hour),
				Executable: "/usr/libexec/steady-helper"},
			FootprintBytes: 20 << 20, ResidentBytes: 20 << 20, Quality: model.QualGood,
		})
	}
	return model.ProcessSnapshot{At: at, Processes: procs, Quality: model.QualGood,
		Coverage: model.Coverage{Attempted: len(procs), Succeeded: len(procs)}}
}

// closeAndRecord runs the daemon's window cadence: close every five minutes.
func closeAndRecord(t *testing.T, h *daemonHandler, pipe *pipeline.Pipeline,
	sys model.SystemSample, elapsed time.Duration) {
	t.Helper()
	if elapsed == 0 || elapsed%(300*time.Second) != 0 {
		return
	}
	if vs := pipe.CloseWindow(sys); len(vs) > 0 {
		h.recordVerdicts(sys, vs, nil)
	}
}

func countAdvisories(t *testing.T, lg *ledger.Ledger) int {
	t.Helper()
	return len(advisoriesOf(t, lg))
}

func advisoriesOf(t *testing.T, lg *ledger.Ledger) []map[string]any {
	t.Helper()
	recs, err := lg.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	for _, raw := range recs {
		var m map[string]any
		if json.Unmarshal(raw, &m) != nil {
			continue
		}
		if m["kind"] == "advisory" {
			out = append(out, m)
		}
	}
	return out
}

type traceFile struct {
	Samples []struct {
		MonoMs      float64 `json:"mono_ms"`
		FreeBytes   float64 `json:"free_bytes"`
		KernelLevel int     `json:"kernel_level"`
		PageBytes   uint64  `json:"page_bytes"`
	} `json:"samples"`
}

func loadTrace(t *testing.T, path string) traceFile {
	t.Helper()
	b, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatal(err)
	}
	var tf traceFile
	if err := json.Unmarshal(b, &tf); err != nil {
		t.Fatal(err)
	}
	return tf
}
