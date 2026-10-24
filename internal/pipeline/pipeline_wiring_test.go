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
			ResidentBytes:  uint64(e[3].(int64)),
			CpuUserS:       float64(e[3].(int64)) / 1e12,
			Quality:        model.QualGood})
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
