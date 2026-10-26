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
	"memory_pilot/internal/pipeline"
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

// Growth without anything releasable is no longer announced. Measured before
// this rule: 98 advisories in 45 minutes at Normal pressure, most naming groups
// that were working. The verdict still reaches the ledger and `report`.
func TestGrowthAloneIsNotAdvised(t *testing.T) {
	vs := []classify.Verdict{
		{GroupKey: "Docker Desktop", Bucket: classify.SubprocessAccumulation,
			Confidence: 0.6, Basis: []string{"process count reached 30"}, GrowthMB: 400},
	}
	if got := advisoryTexts(t, vs); len(got) != 0 {
		t.Errorf("growth-only verdict produced %d advisories: %v", len(got), got)
	}
}

// The same group is not announced twice within the rate limit.
func TestAdvisoryIsRateLimited(t *testing.T) {
	h := &daemonHandler{lastAdvisory: map[string]time.Time{}}
	now := time.Now()
	if last, ok := h.advisoriesThisWindow(now)["leftover"]; ok {
		t.Fatalf("fresh handler already advised at %v", last)
	}
	h.markAdvised("leftover", now)
	got := h.advisoriesThisWindow(now.Add(5 * time.Minute))["leftover"]
	if got.IsZero() {
		t.Fatal("markAdvised did not record the time")
	}
	if now.Add(5*time.Minute).Sub(got) >= advisoryRepeatEvery {
		t.Errorf("a group advised 5 minutes ago is due again (limit is %v)",
			advisoryRepeatEvery)
	}
}

// The relief cooldown is claimed only when a recommendation is emitted.
// Measured after the 00:17 restart: the daemon was at Warn for 55 s with one
// second of process history, so no member could be eligible — and that empty
// window used up the 20-minute cooldown, silencing the first one that had a
// name to offer.
func TestReliefCooldownIsNotSpentOnAnEmptyWindow(t *testing.T) {
	dir := t.TempDir()
	lg, err := ledger.Open(dir, "ledger")
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()
	h := &daemonHandler{lg: lg,
		pipe: pipeline.New(pipeline.Config{WindowS: 300}, lg, nil, nil, nil)}
	warn := model.SystemSample{KernelLevel: model.LevelWarn, At: time.Unix(1000, 0)}
	// A fresh attributor has no series, so there is nothing to recommend.
	h.recommendRelief(warn, 0.5)
	if !h.lastReliefAt.IsZero() {
		t.Errorf("an empty window claimed the cooldown at %v", h.lastReliefAt)
	}
	// And the next Warn sample is still allowed to try.
	next := warn
	next.At = warn.At.Add(5 * time.Second)
	h.recommendRelief(next, 0.5)
}

// The cooldown must fit inside the episodes it is meant to answer. Measured on
// this machine over one day: 40 Warn episodes, median 0.3 minutes, longest 8.3
// minutes, none reaching 20. A 20-minute cooldown answered each episode at most
// once — and at the moment it had the least history to answer with.
func TestReliefCooldownFitsInsideAWarnEpisode(t *testing.T) {
	const longestEpisode = 8*time.Minute + 20*time.Second // measured maximum
	if reliefCooldown > longestEpisode {
		t.Errorf("cooldown %v exceeds the longest observed Warn episode %v; "+
			"an episode can then never be answered twice, however much the "+
			"first answer missed", reliefCooldown, longestEpisode)
	}
	if reliefCooldown < time.Minute {
		t.Errorf("cooldown %v is short enough to repeat a sentence the owner "+
			"has not finished reading", reliefCooldown)
	}
}

// An advisory must carry the pressure it was emitted at. Without it, "was that
// at Warn or at Normal?" needs a timestamp join against system samples, and the
// answer is only as good as whichever samples happen to bracket it.
func TestAdvisoryRecordsItsPressure(t *testing.T) {
	dir := t.TempDir()
	lg, err := ledger.Open(dir, "ledger")
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()
	h := &daemonHandler{lg: lg, lastAdvisory: map[string]time.Time{}}
	s := model.SystemSample{At: time.Unix(1793000000, 0), PageBytes: 16384,
		FreePages: 4000, KernelLevel: model.LevelWarn}
	vs := []classify.Verdict{{GroupKey: "leftover", Bucket: classify.LeakSuspicion,
		Confidence: 0.65, Basis: []string{"sustained 1260s"}, GrowthMB: 900}}
	h.recordVerdicts(s, vs, nil)

	entries, _ := os.ReadDir(dir)
	var found bool
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
			if json.Unmarshal([]byte(line), &m) != nil || m["kind"] != "advisory" {
				continue
			}
			found = true
			if lvl, ok := m["kernel_level"].(float64); !ok || int(lvl) != int(model.LevelWarn) {
				t.Errorf("advisory kernel_level = %v, want %d", m["kernel_level"], model.LevelWarn)
			}
			if _, ok := m["free_mb"]; !ok {
				t.Error("advisory does not record free memory")
			}
		}
	}
	if !found {
		t.Fatal("no advisory was written — the test did not exercise the path")
	}
}

// The same answer must not be delivered twice. Measured: five identical
// recommendations in eighteen minutes naming the same five pids and the same
// 601 MB — the repeating warning the owner asked never to see again, produced
// by the tool rather than the machine.
func TestIdenticalReliefIsNotRepeated(t *testing.T) {
	dir := t.TempDir()
	lg, err := ledger.Open(dir, "ledger")
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()
	h := &daemonHandler{lg: lg, pipe: pipeline.New(pipeline.Config{WindowS: 300}, lg, nil, nil, nil)}

	// Two identical texts: the second must not be recorded.
	h.mu.Lock()
	h.lastReliefText = "closing 5 abandoned workload(s) would return ~601 MB"
	h.mu.Unlock()

	count := func() int {
		n := 0
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".jsonl") {
				continue
			}
			b, _ := os.ReadFile(filepath.Join(dir, e.Name()))
			for _, line := range strings.Split(string(b), "\n") {
				if strings.Contains(line, `"relief_recommendation"`) {
					n++
				}
			}
		}
		return n
	}
	before := count()
	// Drive the gate directly with the same text it just recorded.
	h.emitReliefIfChanged(time.Unix(1793000000, 0), "closing 5 abandoned workload(s) would return ~601 MB", 601, 3100, 5)
	if got := count(); got != before {
		t.Errorf("an identical recommendation was recorded again (%d -> %d)", before, got)
	}
	// A different answer is still delivered.
	h.emitReliefIfChanged(time.Unix(1793000300, 0), "closing 6 abandoned workload(s) would return ~900 MB", 900, 3100, 6)
	if got := count(); got != before+1 {
		t.Errorf("a changed recommendation was suppressed (%d -> %d)", before, got)
	}
}
