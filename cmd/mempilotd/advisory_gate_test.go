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
