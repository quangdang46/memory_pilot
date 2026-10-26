package main

import (
	"encoding/json"
	"fmt"
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
	const same = "closing 5 abandoned workload(s) would return ~601 MB\n" +
		"  /usr/bin/node (pid 64747): 120 MB, idle by its own CPU record, parent gone\n" +
		"      to release: kill 64747\n" +
		"shortfall is 3100 MB — this will NOT cover it, and nothing else can be released safely"
	h.mu.Lock()
	h.lastReliefText = reliefActionKey(same)
	h.lastReliefLevel = model.LevelWarn
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
	// Same action, different shortfall: must be suppressed.
	drifted := "closing 5 abandoned workload(s) would return ~601 MB\n" +
		"  /usr/bin/node (pid 64747): 120 MB, idle by its own CPU record, parent gone\n" +
		"      to release: kill 64747\n" +
		"shortfall is 3140 MB — this will NOT cover it, and nothing else can be released safely"
	h.emitReliefIfChanged(time.Unix(1793000000, 0), drifted, 601, 3140, 5, model.LevelWarn)
	if got := count(); got != before {
		t.Errorf("an identical recommendation was recorded again (%d -> %d)", before, got)
	}
	// A different answer is still delivered.
	h.emitReliefIfChanged(time.Unix(1793000300, 0), "closing 6 abandoned workload(s) would return ~900 MB", 900, 3100, 6, model.LevelWarn)
	if got := count(); got != before+1 {
		t.Errorf("a changed recommendation was suppressed (%d -> %d)", before, got)
	}
}

// Two recommendations that differ only in the shortfall are the same news.
// Measured: five minutes apart, same five pids, same 601 MB, "3134 MB" against
// "3202 MB".
func TestShortfallDriftIsNotNews(t *testing.T) {
	a := `closing 5 abandoned workload(s) would return ~601 MB
  /usr/bin/node (pid 64747): 120 MB, idle by its own CPU record, parent gone
      to release: kill 64747
shortfall is 3134 MB — this will NOT cover it, and nothing else can be released safely`
	b := `closing 5 abandoned workload(s) would return ~601 MB
  /usr/bin/node (pid 64747): 120 MB, idle by its own CPU record, parent gone
      to release: kill 64747
shortfall is 3202 MB — this will NOT cover it, and nothing else can be released safely`
	if reliefActionKey(a) != reliefActionKey(b) {
		t.Errorf("shortfall drift changed the action key:\n%q\nvs\n%q",
			reliefActionKey(a), reliefActionKey(b))
	}
	// A different process set is different news.
	c := `closing 1 abandoned workload(s) would return ~900 MB
  /usr/bin/other (pid 999): 900 MB, no CPU for 2h0m0s, parent gone
shortfall is 3202 MB — this will NOT cover it, and nothing else can be released safely`
	if reliefActionKey(a) == reliefActionKey(c) {
		t.Error("a different process set produced the same action key")
	}
}

// Escalation is news even when the sentence is not. Measured: the machine
// reached Critical for the first time while the recommendation was identical to
// one already delivered at Warn, so the dedup held it back — at the moment the
// owner most needed to know the gap cannot be closed.
func TestEscalationOverridesDedup(t *testing.T) {
	dir := t.TempDir()
	lg, err := ledger.Open(dir, "ledger")
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()
	h := &daemonHandler{lg: lg, pipe: pipeline.New(pipeline.Config{WindowS: 300}, lg, nil, nil, nil)}

	const txt = "closing 5 abandoned workload(s) would return ~601 MB\n" +
		"  /usr/bin/node (pid 64747): 120 MB, idle by its own CPU record, parent gone\n" +
		"shortfall is 2104 MB — this will NOT cover it, and nothing else can be released safely"
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
	h.emitReliefIfChanged(time.Unix(1793000000, 0), txt, 601, 2104, 19, model.LevelWarn)
	atWarn := count()
	if atWarn != 1 {
		t.Fatalf("first recommendation not recorded (%d)", atWarn)
	}
	// Same sentence, same level: held.
	h.emitReliefIfChanged(time.Unix(1793000300, 0), txt, 601, 2104, 19, model.LevelWarn)
	if got := count(); got != atWarn {
		t.Errorf("repeat at the same level was recorded (%d -> %d)", atWarn, got)
	}
	// Same sentence, escalated to Critical: delivered.
	h.emitReliefIfChanged(time.Unix(1793000600, 0), txt, 601, 2104, 19, model.LevelCritical)
	if got := count(); got != atWarn+1 {
		t.Errorf("escalation to Critical was suppressed (%d -> %d)", atWarn, got)
	}
}

// The advisory path must produce output for a verdict that deserves one,
// through the real policy, executor and rate limiter — not just be free of
// refusals. Measured: for an hour after the rate-limit fix the classifier
// produced 205 unknown, 36 cache and 1 workload verdict and no leak at all, so
// "0 refused" meant nothing about whether the path works.
func TestAdvisoryPathProducesOutputForARealVerdict(t *testing.T) {
	dir := t.TempDir()
	lg, err := ledger.Open(dir, "ledger")
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()
	h := &daemonHandler{lg: lg, lastAdvisory: map[string]time.Time{}}
	s := model.SystemSample{At: time.Unix(1793000000, 0), PageBytes: 16384,
		FreePages: 4000, KernelLevel: model.LevelWarn,
		MachStatsQuality: model.QualGood, KernelLevelQuality: model.QualGood}
	vs := []classify.Verdict{{
		GroupKey: "leftover-build", Bucket: classify.LeakSuspicion,
		Confidence: 0.65, Basis: []string{"sustained 1500s >= 1200s"}, GrowthMB: 900,
	}}
	h.recordVerdicts(s, vs, nil)

	entries, _ := os.ReadDir(dir)
	var advisories int
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		b, _ := os.ReadFile(filepath.Join(dir, e.Name()))
		for _, line := range strings.Split(string(b), "\n") {
			if strings.Contains(line, `"kind":"advisory"`) || strings.Contains(line, `"kind": "advisory"`) {
				advisories++
			}
		}
	}
	if advisories != 1 {
		t.Errorf("%d advisories written for a leak verdict at Warn, want 1 — "+
			"the path is silent for the verdict it exists to deliver", advisories)
	}
}

// Every verdict must leave a trace saying what happened to it. Without this,
// "no advisory" and "the advisory path is broken" are the same observation —
// which is how four defects hid in this file: dedup blocking escalation, a rate
// limit refusing at Critical, an executor reporting advisories it never wrote,
// and policy offering an advisory it never recorded.
func TestEveryVerdictRecordsItsOutcome(t *testing.T) {
	dir := t.TempDir()
	lg, err := ledger.Open(dir, "ledger")
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()
	h := &daemonHandler{lg: lg, lastAdvisory: map[string]time.Time{}}
	s := model.SystemSample{At: time.Unix(1793000000, 0), PageBytes: 16384,
		FreePages: 200000, KernelLevel: model.LevelNormal}
	vs := []classify.Verdict{
		{GroupKey: "wezterm-gui", Bucket: classify.LeakSuspicion, GrowthMB: 900},
		{GroupKey: "quiet-cache", Bucket: classify.CacheOrRetrievable, GrowthMB: 20},
		{GroupKey: "tiny-work", Bucket: classify.WorkloadGrowth, GrowthMB: 5},
	}
	h.recordVerdicts(s, vs, nil)

	entries, _ := os.ReadDir(dir)
	var row map[string]any
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		b, _ := os.ReadFile(filepath.Join(dir, e.Name()))
		for _, line := range strings.Split(string(b), "\n") {
			var m map[string]any
			if json.Unmarshal([]byte(line), &m) != nil || m["kind"] != "advisory_decision" {
				continue
			}
			row = m
		}
	}
	if row == nil {
		t.Fatal("no advisory_decision row — a quiet window cannot be told from a broken one")
	}
	if n, _ := row["verdicts"].(float64); int(n) != 3 {
		t.Errorf("verdicts = %v, want 3", row["verdicts"])
	}
	sup, _ := row["suppressed"].([]any)
	if len(sup) != 3 {
		t.Fatalf("%d suppressions recorded, want 3 (one per verdict)", len(sup))
	}
	for _, raw := range sup {
		m, _ := raw.(map[string]any)
		if m["why"] == "" || m["why"] == nil {
			t.Errorf("suppression with no reason: %v", m)
		}
	}
	// The reasons must be specific, not one catch-all.
	seen := map[string]bool{}
	for _, raw := range sup {
		m, _ := raw.(map[string]any)
		seen[fmt.Sprint(m["why"])] = true
	}
	if len(seen) < 2 {
		t.Errorf("only %d distinct reasons across three different verdicts: %v", len(seen), seen)
	}
}

// The complete decision table. Every combination of bucket, pressure and size
// must land on "announced" or on one of the four legitimate reasons — never on
// an unexplained silence, and never announcing something that is not news.
func TestSuppressionTableIsComplete(t *testing.T) {
	type row struct {
		name    string
		v       classify.Verdict
		level   model.PressureLevel
		wantAdv bool
	}
	rows := []row{
		// The shapes that must be announced.
		{"leak at Warn", classify.Verdict{GroupKey: "a", Bucket: classify.LeakSuspicion, GrowthMB: 500}, model.LevelWarn, true},
		{"leak at Critical", classify.Verdict{GroupKey: "a", Bucket: classify.LeakSuspicion, GrowthMB: 500}, model.LevelCritical, true},
		{"leak at Normal", classify.Verdict{GroupKey: "a", Bucket: classify.LeakSuspicion, GrowthMB: 500}, model.LevelNormal, false},
		{"accumulation at Warn", classify.Verdict{GroupKey: "a", Bucket: classify.SubprocessAccumulation, GrowthMB: 400}, model.LevelWarn, true},
		{"accumulation at Normal", classify.Verdict{GroupKey: "a", Bucket: classify.SubprocessAccumulation, GrowthMB: 400}, model.LevelNormal, false},
		// The owner's decision: a large working workload is a cost they
		// accepted, at any pressure.
		{"big workload at Warn", classify.Verdict{GroupKey: "a", Bucket: classify.WorkloadGrowth, GrowthMB: 3000}, model.LevelWarn, false},
		{"big workload at Critical", classify.Verdict{GroupKey: "a", Bucket: classify.WorkloadGrowth, GrowthMB: 3000}, model.LevelCritical, false},
		{"small workload at Warn", classify.Verdict{GroupKey: "a", Bucket: classify.WorkloadGrowth, GrowthMB: 100}, model.LevelWarn, false},
		// Not news, at any pressure.
		{"cache at Critical", classify.Verdict{GroupKey: "a", Bucket: classify.CacheOrRetrievable, GrowthMB: 900}, model.LevelCritical, false},
		{"unknown at Critical", classify.Verdict{GroupKey: "a", Bucket: classify.Unknown, GrowthMB: 900}, model.LevelCritical, false},
		// Terminal exemption applies whatever the shape.
		{"terminal leak at Critical", classify.Verdict{GroupKey: "wezterm-gui", Bucket: classify.LeakSuspicion, GrowthMB: 900}, model.LevelCritical, false},
	}
	for _, r := range rows {
		dir := t.TempDir()
		lg, err := ledger.Open(dir, "ledger")
		if err != nil {
			t.Fatal(err)
		}
		h := &daemonHandler{lg: lg, lastAdvisory: map[string]time.Time{}}
		s := model.SystemSample{At: time.Unix(1793000000, 0), PageBytes: 16384,
			FreePages: 4000, KernelLevel: r.level}
		h.recordVerdicts(s, []classify.Verdict{r.v}, nil)

		var announced, decided int
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
					announced++
				case "advisory_decision":
					decided++
				}
			}
		}
		if decided != 1 {
			t.Errorf("%s: %d advisory_decision rows, want 1 — the outcome was not recorded", r.name, decided)
		}
		if (announced > 0) != r.wantAdv {
			t.Errorf("%s: announced=%d, want %v", r.name, announced, r.wantAdv)
		}
		lg.Close()
	}
}
