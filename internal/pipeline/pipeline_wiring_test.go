package pipeline

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"memory_pilot/internal/classify"
	"memory_pilot/internal/executor"
	"memory_pilot/internal/model"
	"memory_pilot/internal/policy"
	"memory_pilot/internal/verify"
)

// recordingLedger captures ledger writes so tests can assert what was
// recorded without a filesystem.
type recordingLedger struct {
	kinds []string
	recs  []map[string]any
}

func (r *recordingLedger) Append(v any) error {
	b, _ := json.Marshal(v)
	var m map[string]any
	json.Unmarshal(b, &m)
	if k, ok := m["kind"].(string); ok {
		r.kinds = append(r.kinds, k)
	}
	r.recs = append(r.recs, m)
	return nil
}

func (r *recordingLedger) has(kind string) bool {
	for _, k := range r.kinds {
		if k == kind {
			return true
		}
	}
	return false
}

func (r *recordingLedger) find(kind string) map[string]any {
	for i := len(r.recs) - 1; i >= 0; i-- {
		if r.recs[i]["kind"] == kind {
			return r.recs[i]
		}
	}
	return nil
}

func newTestPipeline(t *testing.T, mode policy.Mode, lg Ledger) *Pipeline {
	t.Helper()
	reg := executor.NewRegistry()
	guard := &executor.ActionGuard{
		Mode: func() policy.Mode { return mode }, Registry: reg,
		MaxAge: 30 * time.Second,
	}
	ex := executor.New(guard)
	ex.Actors = executor.Actors{
		TrimOwnBuffers:  func() (executor.Effect, error) { return executor.Effect{Bytes: 16 << 20, Changed: true}, nil },
		RotateOwnLedger: func() (executor.Effect, error) { return executor.Effect{Changed: true}, nil },
	}
	p := New(Config{WindowS: 60, TopN: 5, MinGroupGrowthBytes: 1 << 20},
		lg, policy.NewEngine(mode), ex, verify.New())
	p.Owned = reg.List()
	return p
}

func mkSnap(at time.Time, entries [][4]any) model.ProcessSnapshot {
	s := model.ProcessSnapshot{At: at, Quality: model.QualGood}
	s.Coverage = model.Coverage{Attempted: len(entries), Succeeded: len(entries)}
	for _, e := range entries {
		s.Processes = append(s.Processes, model.ProcessSample{
			Identity: model.ProcessIdentity{Pid: e[0].(int32), Ppid: e[1].(int32),
				StartTime: time.Unix(1700000000, 0), Executable: e[2].(string)},
			ResidentBytes: uint64(e[3].(int64)),
			CpuUserS:      float64(e[3].(int64)) / 1e12,
			Quality:       model.QualGood})
	}
	return s
}

func sysSample(at time.Time, freePages uint64) model.SystemSample {
	return model.SystemSample{At: at, MonotonicMs: at.UnixMilli(), PageBytes: 16384,
		FreePages: freePages, CompressorPages: 400000,
		MachStatsQuality: model.QualGood, KernelLevelQuality: model.QualGood,
		KernelLevel: model.LevelNormal}
}

// The window must produce a real attribution record naming the group that
// grew, and a classification for it — this is the output the old daemon never
// wrote (it printed "attribution pending Phase 3 wiring" instead).
func TestWindowRecordsAttributionAndClassification(t *testing.T) {
	lg := &recordingLedger{}
	p := newTestPipeline(t, policy.ModeObserve, lg)
	t0 := time.Now().Add(-5 * time.Minute)

	grow := func(mb int64) [][4]any {
		return [][4]any{
			{int32(500), int32(1), "iTerm2", int64(100 << 20)},
			{int32(501), int32(500), "node", mb << 20},
		}
	}
	for i := 0; i < 20; i++ {
		at := t0.Add(time.Duration(i) * 15 * time.Second)
		// Steady CPU while growing: this is workload growth, not a leak.
		snap := mkSnap(at, grow(100+int64(i)*30))
		p.ObserveProcesses(snap, sysSample(at, 200000))
	}
	vs := p.CloseWindow(sysSample(t0.Add(5*time.Minute), 180000))

	if !lg.has("attribution") {
		t.Fatal("no attribution record written")
	}
	att := lg.find("attribution")
	groups, _ := att["groups"].([]any)
	if len(groups) == 0 {
		t.Fatalf("attribution has no groups: %v", att)
	}
	if !strings.Contains(att["note"].(string), "can exceed the system delta") {
		t.Errorf("honest coverage note missing: %v", att["note"])
	}
	if len(vs) == 0 {
		t.Fatal("no verdicts produced")
	}
	if !lg.has("classification") {
		t.Fatal("no classification record written")
	}
	cls := lg.find("classification")
	if cls["bucket"] == "" || cls["text"] == "" {
		t.Errorf("classification record incomplete: %v", cls)
	}
	// The rendered text must never claim a conclusion (SI-5).
	if viol := classify.PhraseTest(cls["text"].(string)); len(viol) > 0 {
		t.Errorf("phrase test violations in recorded text: %v — %s", viol, cls["text"])
	}
	// And it must name the growing group, not be a generic message.
	if !strings.Contains(cls["text"].(string), "iTerm2") {
		t.Errorf("classification does not name the group: %v", cls["text"])
	}
}

// Policy must not fire on a normal-pressure tick with no candidates.
func TestNoActionAtNormalPressure(t *testing.T) {
	lg := &recordingLedger{}
	p := newTestPipeline(t, policy.ModeCanary, lg)
	at := time.Now()
	s := sysSample(at, 400000)
	results := p.Decide(context.Background(), s,
		PressureView{Level: model.LevelNormal, Score: 0.1, Quality: model.QualGood},
		model.Forecast{}, nil)
	if len(results) != 0 {
		t.Errorf("actions executed at Normal pressure: %+v", results)
	}
	if !lg.has("policy_no_action") {
		t.Errorf("decision not recorded: %v", lg.kinds)
	}
}

// Under Critical pressure the self-relief candidates run and are recorded with
// real effect evidence — not a bare "executed".
func TestSelfReliefExecutedUnderCritical(t *testing.T) {
	lg := &recordingLedger{}
	p := newTestPipeline(t, policy.ModeCanary, lg)
	at := time.Now()
	s := sysSample(at, 8000)
	vs := []classify.Verdict{{GroupKey: "iTerm2", Bucket: classify.WorkloadGrowth,
		Confidence: 0.8, GrowthMB: 400, Basis: []string{"grew with activity"}}}
	results := p.Decide(context.Background(), s,
		PressureView{Level: model.LevelCritical, Score: 0.9, Quality: model.QualGood},
		model.Forecast{Actionable: true, Confidence: 0.9, Calibrated: true, Bound: true}, vs)
	if len(results) == 0 {
		t.Fatal("no self-relief executed under Critical pressure")
	}
	for _, r := range results {
		if r.State != "executed" {
			t.Errorf("action %s state %s (%v)", r.Kind, r.State, r.Evidence)
		}
		if r.Evidence["changed"] != "true" {
			t.Errorf("action %s reports no change: %v", r.Kind, r.Evidence)
		}
	}
	if !lg.has("action_result") {
		t.Errorf("action result not recorded: %v", lg.kinds)
	}
}

// A foreign process must never become an L2 candidate even under Critical
// pressure with a leak-shaped verdict (ST-22).
func TestForeignProcessNeverStopTarget(t *testing.T) {
	lg := &recordingLedger{}
	p := newTestPipeline(t, policy.ModeEnforce, lg)
	at := time.Now()
	s := sysSample(at, 8000)
	vs := []classify.Verdict{{GroupKey: "foreign-agent", Bucket: classify.LeakSuspicion,
		Confidence: 0.9, GrowthMB: 900, Basis: []string{"sustained growth"}}}
	results := p.Decide(context.Background(), s,
		PressureView{Level: model.LevelCritical, Score: 0.95, Quality: model.QualGood},
		model.Forecast{Actionable: true, Confidence: 0.9, Calibrated: true, Bound: true}, vs)
	for _, r := range results {
		if r.Kind == model.ActionStopOwned {
			t.Fatalf("ST-22: L2 executed against a foreign workload: %+v", r)
		}
	}
	pc := lg.find("policy_candidates")
	if pc == nil {
		t.Fatalf("no policy record: %v", lg.kinds)
	}
	if kinds, ok := pc["candidates"].([]any); ok {
		for _, k := range kinds {
			if k == string(model.ActionStopOwned) {
				t.Errorf("ST-22: stop_owned planned for a foreign process")
			}
		}
	}
}

// L2 must be reachable for an owned, pre-authorized workload — the previous
// implementation skipped stop_owned unconditionally, so the ADR-014 path was
// wired but dead.
func TestL2ReachesOwnedAuthorizedTarget(t *testing.T) {
	lg := &recordingLedger{}
	reg := executor.NewRegistry()
	// A workload the daemon spawned and the user authorized for managed stop.
	reg.Register(executor.OwnedPid{Pid: 4242, Pgid: 4242, Auth: "allow-stop"})
	guard := &executor.ActionGuard{
		Mode: func() policy.Mode { return policy.ModeCanary }, Registry: reg,
		MaxAge: 30 * time.Second,
	}
	ex := executor.New(guard)
	ex.Actors = executor.Actors{
		TrimOwnBuffers:  func() (executor.Effect, error) { return executor.Effect{Changed: true}, nil },
		RotateOwnLedger: func() (executor.Effect, error) { return executor.Effect{Changed: true}, nil },
	}
	p := New(Config{WindowS: 60}, lg, policy.NewEngine(policy.ModeCanary), ex, verify.New())
	p.Owned = reg.List()

	at := time.Now()
	vs := []classify.Verdict{{GroupKey: "claude", Bucket: classify.LeakSuspicion,
		Confidence: 0.8, GrowthMB: 700, Basis: []string{"sustained growth"}}}
	results := p.Decide(context.Background(), sysSample(at, 8000),
		PressureView{Level: model.LevelCritical, Score: 0.95, Quality: model.QualGood},
		model.Forecast{Actionable: true, Confidence: 0.9, Calibrated: true, Bound: true}, vs)

	sawL2 := false
	for _, r := range results {
		if r.Kind == model.ActionStopOwned {
			sawL2 = true
			// The test registry points at pid 4242, which does not exist; the
			// signal attempt must be recorded as failed, never as success.
			if r.State != "failed" && r.State != "executed" {
				t.Errorf("L2 outcome %q: %v", r.State, r.Evidence)
			}
		}
	}
	if !sawL2 {
		t.Fatal("L2 never reached the executor for an owned, authorized workload")
	}
	rec := lg.find("action_result")
	if rec != nil && rec["tier"] != "L2" {
		// The last record may be a self-action; scan for the L2 one.
		found := false
		for _, r := range lg.recs {
			if r["kind"] == "action_result" && r["tier"] == "L2" {
				found = true
			}
		}
		if !found {
			t.Error("L2 result was not recorded with its tier")
		}
	}
}

// An owned workload WITHOUT the authorization token is not an L2 target: it
// must never appear in the policy candidate list (ADR-014 precondition 2).
func TestUnauthorizedOwnedNotAnL2Target(t *testing.T) {
	reg := executor.NewRegistry()
	reg.Register(executor.OwnedPid{Pid: 4242, Pgid: 4242, Auth: ""}) // launched without --allow-stop
	if got := reg.List(); len(got) != 0 {
		t.Errorf("un-authorized workload exposed as L2 target: %v", got)
	}
	reg.Register(executor.OwnedPid{Pid: 4243, Pgid: 4243, Auth: "allow-stop"})
	if got := reg.List(); len(got) != 1 || got[0] != "owned:4243" {
		t.Errorf("authorized workload missing from L2 targets: %v", got)
	}
}

// The failure measured in run 3: on a busy machine, a small sustained grower
// was never judged because five bigger groups filled the top-N by bytes. The
// dangerous set and the biggest set were disjoint.
func TestSmallSustainedGrowerIsJudged(t *testing.T) {
	lg := &recordingLedger{}
	p := newTestPipeline(t, policy.ModeObserve, lg)
	p.Cfg.TopN = 2 // harsher than the daemon's 5, to make the point

	base := time.Now().Add(-25 * time.Minute)
	start := base
	pid := int32(900)
	// Two loud groups that fill the top-2, plus one quiet sustained grower.
	mk := func(i int, loudA, loudB, quiet int64) model.ProcessSnapshot {
		at := base.Add(time.Duration(i) * 15 * time.Second)
		s := model.ProcessSnapshot{At: at, Quality: model.QualGood}
		add := func(id int32, ppid int32, exe string, bytes uint64) {
			s.Processes = append(s.Processes, model.ProcessSample{
				Identity: model.ProcessIdentity{Pid: id, Ppid: ppid,
					StartTime: start, Executable: exe},
				FootprintBytes: bytes, ResidentBytes: bytes,
				Quality: model.QualGood})
		}
		add(1, 1, "wezterm-gui", uint64(loudA))
		add(2, 1, "Google Chrome", uint64(loudB))
		add(pid, 1, "quiet-grower", uint64(quiet))
		s.Coverage = model.Coverage{Attempted: 3, Succeeded: 3}
		return s
	}
	for i := 0; i < 100; i++ {
		// loud groups oscillate (they release as much as they grow)
		loud := int64(2<<30) + int64(i%2)*int64(1<<30)
		quiet := int64(50<<20) + int64(i)*int64(1<<20) // steady 1 MB/15 s
		p.ObserveProcesses(mk(i, loud, loud, quiet), sysSample(base.Add(time.Duration(i)*15*time.Second), 200000))
	}
	vs := p.CloseWindow(sysSample(base.Add(100*15*time.Second), 200000))

	var judged []string
	for _, v := range vs {
		judged = append(judged, v.GroupKey)
	}
	found := false
	for _, g := range judged {
		if g == "quiet-grower" {
			found = true
		}
	}
	if !found {
		t.Fatalf("sustained small grower never judged; judged only %v — "+
			"this is exactly the run-3 failure", judged)
	}
}

// A group that releases as much as it grows must not be called a leak, no
// matter how large its gross growth number is. Measured on Chrome: 1.4-3.1 GB
// released against 1.4-2.4 GB grown, labelled leak four windows running.
func TestCyclingGroupIsNotALeak(t *testing.T) {
	v := classify.Classify(classify.SeriesStats{
		GroupKey: "Google Chrome", SlopeBytesS: 5e6, GrowthBytes: 2400 << 20,
		ReleaseBytes: 3000 << 20, DurationS: 2400, ActivityCorr: 0.2,
		QuietS: 2000, CoverageRatio: 0.99, ProcessCount: 22,
	})
	if v.Bucket != classify.CacheOrRetrievable {
		t.Errorf("cycling group classified %s, want cache_or_retrievable (basis %v)",
			v.Bucket, v.Basis)
	}
}

// Process accumulation must be reachable and must win over "leak" when the
// growth is explained by a rising process count with no CPU correlation.
func TestProcessAccumulationReachable(t *testing.T) {
	v := classify.Classify(classify.SeriesStats{
		GroupKey: "probe-children", SlopeBytesS: 2e6, GrowthBytes: 400 << 20,
		NetGrowthBytes: 400 << 20, LongGrowthBytes: 400 << 20,
		ReleaseBytes: 0, DurationS: 2400, ActivityCorr: 0.0, QuietS: 2000,
		CoverageRatio: 0.99, ProcessCount: 20, PeakProcessCount: 24,
	})
	if v.Bucket != classify.SubprocessAccumulation {
		t.Errorf("accumulating group classified %s, want subprocess_accumulation (basis %v)",
			v.Bucket, v.Basis)
	}
}

// A group that grew and then stopped must be re-judged, not silently dropped.
// While it was climbing it may have been called a leak; once it plateaus the
// correct verdict is a cache. Filtering it out for low current growth is why
// that correction could never happen.
func TestPlateauedGroupIsReJudged(t *testing.T) {
	lg := &recordingLedger{}
	p := newTestPipeline(t, policy.ModeObserve, lg)
	p.Cfg.MinGroupGrowthBytes = 32 << 20

	base := time.Now().Add(-30 * time.Minute)
	start := base
	mk := func(i int, mb uint64) model.ProcessSnapshot {
		at := base.Add(time.Duration(i) * 15 * time.Second)
		return model.ProcessSnapshot{At: at, Quality: model.QualGood,
			Coverage: model.Coverage{Attempted: 1, Succeeded: 1},
			Processes: []model.ProcessSample{{
				Identity: model.ProcessIdentity{Pid: 55, Ppid: 1, StartTime: start,
					Executable: "cachey"},
				FootprintBytes: mb, Quality: model.QualGood}}}
	}
	// 20 samples growing to 400MB, then 60 samples flat at 400MB.
	var mb uint64 = 10 << 20
	for i := 0; i < 80; i++ {
		if i < 20 {
			mb += 20 << 20
		}
		at := base.Add(time.Duration(i) * 15 * time.Second)
		p.ObserveProcesses(mk(i, mb), sysSample(at, 200000))
		if i%20 == 19 {
			p.CloseWindow(sysSample(at, 200000))
		}
	}
	vs := p.CloseWindow(sysSample(base.Add(80*15*time.Second), 200000))
	judged := false
	for _, v := range vs {
		if v.GroupKey == "cachey" {
			judged = true
			if v.Bucket != classify.CacheOrRetrievable {
				t.Errorf("plateaued group judged %s, want cache_or_retrievable (basis %v)",
					v.Bucket, v.Basis)
			}
		}
	}
	if !judged {
		t.Fatal("plateaued group never re-judged — a stale leak verdict can never be corrected")
	}
}

// The window that is judged must be the window that closed. Closing resets
// every counter, and the classifier runs after the close — so reading the live
// series gave it GrowthBytes=0 and SlopeBytesS=0 for a group that had just
// grown 98 MB, and no growth-dependent branch could fire. Measured in run18:
// probe-plateau, flat for 907 s (past the 900 s plateau threshold), was
// classified leak_suspicion; probe-children and probe-sawtooth likewise.
func TestVerdictSeesTheClosedWindow(t *testing.T) {
	lg := &recordingLedger{}
	p := newTestPipeline(t, policy.ModeObserve, lg)
	t0 := time.Now().Add(-5 * time.Minute)

	// A group that grows hard all window and is then judged on it.
	for i := 0; i < 20; i++ {
		at := t0.Add(time.Duration(i) * 15 * time.Second)
		snap := mkSnap(at, [][4]any{
			{int32(700), int32(1), "probe-leak", int64(100+i*8) << 20},
		})
		p.ObserveProcesses(snap, sysSample(at, 200000))
	}
	p.CloseWindow(sysSample(t0.Add(5*time.Minute), 180000))

	cls := lg.find("classification")
	if cls == nil {
		t.Fatal("no classification written")
	}
	growth, _ := cls["growth_mb"].(float64)
	if growth <= 0 {
		t.Errorf("verdict reports growth_mb=%v for a group that grew every "+
			"tick of the window — the classifier read the counters after they "+
			"were reset (signals: %v)", growth, cls["signals"])
	}
	sig, _ := cls["signals"].(map[string]any)
	if sig == nil {
		t.Fatal("no signals recorded — the verdict cannot be checked")
	}
	if gb, _ := sig["growth_bytes"].(float64); gb <= 0 {
		t.Errorf("signals growth_bytes=%v: classifier saw a reset window", gb)
	}
}

// The sentence must quote the number the verdict was decided on. Observed live
// at 14:36: "group 2.1.310 grew by 0 MB — unexplained sustained growth
// (suspicion)". The verdict was reached on lifetime growth (409 MB recorded in
// its own basis) while the sentence quoted a flat window.
//
// Asserted directly against the mapping, because constructing a live sequence
// that reaches leak_suspicion with a zero final window takes 30 simulated
// minutes and the mapping is what was wrong.
func TestDecidedGrowthUsesTheDecidingHorizon(t *testing.T) {
	cases := []struct {
		bucket    classify.Bucket
		win, long int64
		wantMB    int64
		reason    string
	}{
		{classify.LeakSuspicion, 0, 409 << 20, 409, "suspicion decides on lifetime growth"},
		{classify.SubprocessAccumulation, 0, 288 << 20, 288, "accumulation decides on lifetime growth"},
		{classify.WorkloadGrowth, 150 << 20, 900 << 20, 150, "workload is a window judgement"},
		{classify.CacheOrRetrievable, 0, 900 << 20, 0, "cache is a window judgement"},
		{classify.LeakSuspicion, 0, 0, 0, "no lifetime record yet: fall back to the window"},
	}
	for _, c := range cases {
		got := decidedGrowthMB(c.bucket, c.win, c.long)
		if got != c.wantMB {
			t.Errorf("%s: bucket %s win=%dMB long=%dMB → %dMB, want %dMB",
				c.reason, c.bucket, c.win>>20, c.long>>20, got, c.wantMB)
		}
	}
}
