// Package pipeline wires the analysis chain that the daemon runs on every
// tick: process snapshot → attribution window → classification → policy
// candidates → executor. It lives outside the analysis packages because it is
// the only place allowed to know about all of them; each layer stays pure and
// independently testable.
//
// SI-2 discipline: nothing here decides. Policy evaluates, the planner orders,
// and the executor re-validates everything before it acts. This type only
// sequences them and records what happened.
package pipeline

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"memory_pilot/internal/attribution"
	"memory_pilot/internal/classify"
	"memory_pilot/internal/executor"
	"memory_pilot/internal/model"
	"memory_pilot/internal/policy"
	"memory_pilot/internal/verify"
)

// Ledger is the narrow slice of the ledger the pipeline writes to.
type Ledger interface {
	Append(v any) error
}

// Config tunes the pipeline's cadence. Zero values take the documented
// defaults (constants.md): 5-minute attribution windows, top-5 groups.
type Config struct {
	WindowS int64 // attribution window; default 300
	TopN    int   // groups classified per window; default 5
	// MinGroupGrowthBytes filters noise: a group that grew less than this in
	// the window is not worth a classification record.
	MinGroupGrowthBytes int64 // default 32 MB
}

func (c Config) withDefaults() Config {
	if c.WindowS <= 0 {
		c.WindowS = 300
	}
	if c.TopN <= 0 {
		c.TopN = 5
	}
	if c.MinGroupGrowthBytes <= 0 {
		c.MinGroupGrowthBytes = 32 << 20
	}
	return c
}

// Pipeline holds the mutable analysis state. One instance per daemon.
type Pipeline struct {
	Cfg    Config
	Attr   *attribution.Attributor
	Engine *policy.Engine
	Exec   *executor.Executor
	Verify *verify.Verifier
	Ledger Ledger
	Owned  []string // pre-authorized managed-workload tokens (L2 candidates)

	mu          sync.Mutex
	windowStart time.Time
	lastWindow  attribution.Window
	lastGroups  []GroupSummary
}

// GroupSummary is the per-group shape the status line and report expose.
type GroupSummary struct {
	ID        string  `json:"id"`
	GrowthMB  int64   `json:"growth_mb"`
	Processes int     `json:"processes"`
	TopExe    string  `json:"top_exe,omitempty"`
	PeakBps   float64 `json:"peak_bps"`
}

func New(cfg Config, lg Ledger, eng *policy.Engine, ex *executor.Executor, v *verify.Verifier) *Pipeline {
	cfg = cfg.withDefaults()
	return &Pipeline{
		Cfg: cfg, Attr: attribution.New(cfg.WindowS), Engine: eng,
		Exec: ex, Verify: v, Ledger: lg,
	}
}

// ObserveProcesses feeds one process snapshot into attribution. The daemon
// calls this on the process cadence (15 s); it must not be throttled, because
// every skipped snapshot is invisible growth (SI-4).
func (p *Pipeline) ObserveProcesses(snap model.ProcessSnapshot, sys model.SystemSample) {
	p.Attr.Observe(snap, sys)
}

// CloseWindow classifies the window and returns the verdicts it recorded.
// Called on the window boundary by the daemon's system tick.
func (p *Pipeline) CloseWindow(sys model.SystemSample) []classify.Verdict {
	w := p.Attr.Window(sys)
	p.mu.Lock()
	p.lastWindow = w
	p.mu.Unlock()
	if p.Ledger != nil {
		type groupOut struct {
			ID         string  `json:"id"`
			GrowthMB   int64   `json:"growth_mb"`
			Processes  int     `json:"processes"`
			TopExe     string  `json:"top_exe,omitempty"`
			LargestPid int32   `json:"largest_pid,omitempty"`
			RateBps    float64 `json:"peak_bps"`
			ReleaseMB  int64   `json:"release_mb,omitempty"`
		}
		groups := make([]groupOut, 0, len(w.Groups))
		for _, g := range w.Groups {
			groups = append(groups, groupOut{
				ID: g.AnchorKey, GrowthMB: g.GrowthBytes >> 20, Processes: g.Processes,
				TopExe: g.TopExe, LargestPid: g.LargestPid, RateBps: g.PeakDeltaRate,
				ReleaseMB: g.ReleaseBytes >> 20,
			})
		}
		stable := make([]groupOut, 0, len(w.LargeButStable))
		for _, g := range w.LargeButStable {
			stable = append(stable, groupOut{ID: g.AnchorKey, Processes: g.Processes})
		}
		_ = p.Ledger.Append(map[string]any{
			"kind": "attribution", "at": sys.At, "window_s": p.Cfg.WindowS,
			"groups": groups, "large_but_stable": stable,
			"accounted_mb":   w.AccountedGrowth >> 20,
			"observed_mb":    w.ObservedGrowth >> 20,
			"coverage_ratio": w.CoverageRatio,
			"compressor_mb":  w.CompressorBytes >> 20,
			"coverage":       w.Coverage,
			"note":           w.Note,
		})
	}

	// Cache the summary for `status`, bounded so a pathological process churn
	// cannot grow the status response without limit.
	summaries := make([]GroupSummary, 0, 10)
	for i, g := range w.Groups {
		if i >= 10 {
			break
		}
		summaries = append(summaries, GroupSummary{ID: g.AnchorKey,
			GrowthMB: g.GrowthBytes >> 20, Processes: g.Processes,
			TopExe: g.TopExe, PeakBps: g.PeakDeltaRate})
	}
	p.mu.Lock()
	p.lastGroups = summaries
	p.mu.Unlock()

	var verdicts []classify.Verdict
	for i, g := range w.Groups {
		if i >= p.Cfg.TopN {
			break
		}
		if g.GrowthBytes < p.Cfg.MinGroupGrowthBytes {
			continue
		}
		st := p.Attr.SeriesFor(g.AnchorKey)
		v := classify.Classify(classify.SeriesStats{
			GroupKey: st.GroupKey, SlopeBytesS: st.SlopeBytesS, Accel: st.Accel,
			PlateauS: st.PlateauS, ActivityCorr: st.ActivityCorr,
			ActivityS: st.ActivityS, QuietS: st.QuietS, CacheRole: st.CacheRole,
			CeilingBytes: st.CeilingBytes, DurationS: st.DurationS,
			CoverageRatio: st.CoverageRatio,
		})
		v.GrowthMB = g.GrowthBytes >> 20
		verdicts = append(verdicts, v)
		if p.Ledger != nil {
			_ = p.Ledger.Append(map[string]any{
				"kind": "classification", "at": sys.At, "group": v.GroupKey,
				"bucket": v.Bucket, "confidence": v.Confidence,
				"basis": v.Basis, "alternatives": v.Alternatives,
				"growth_mb": g.GrowthBytes >> 20, "processes": g.Processes,
				"text": classify.RenderVerdict(v, g.GrowthBytes>>20),
			})
		}
	}
	p.Attr.Reset()
	p.windowStart = sys.At
	return verdicts
}

// Decide runs policy for the current state and, when the policy engine
// produces candidates, plans and executes them. Every outcome is recorded:
// a refused action is as important as an executed one.
func (p *Pipeline) Decide(ctx context.Context, sys model.SystemSample, st PressureView, fo model.Forecast, verdicts []classify.Verdict) []model.ActionResult {
	var results []model.ActionResult
	classification := dominant(verdicts)
	in := policy.Input{
		Pressure:       model.PressureState{At: sys.At, Level: st.Level, Score: st.Score, Quality: st.Quality},
		Forecast:       fo,
		Classification: classification,
		Now:            sys.At,
		OwnedTargets:   p.Owned,
	}
	d := p.Engine.Evaluate(in)
	kind := "policy_no_action"
	if !d.NoAction && len(d.Candidates) > 0 {
		kind = "policy_candidates"
	}
	if p.Ledger != nil {
		kinds := make([]string, 0, len(d.Candidates))
		for _, c := range d.Candidates {
			kinds = append(kinds, string(c.Kind))
		}
		growthMB, group := dominantGroupInfo(verdicts)
		_ = p.Ledger.Append(map[string]any{
			"kind": kind, "at": sys.At, "reason": d.Reason,
			"candidates": kinds, "mode": string(p.Engine.Mode),
			"classification": string(classification.Bucket),
			"group":          group,
			"growth_mb":      growthMB,
		})
	}
	if p.Exec == nil || d.NoAction || len(d.Candidates) == 0 {
		return results
	}
	planned := policy.Plan(d.Candidates, map[model.ActionKind]bool{})
	// Self-relief runs first and must be verified before the scarce, risky L2
	// action is considered. Skipping L2 entirely in the same pass is right;
	// skipping it *forever* is not — the previous `continue` meant the
	// managed-stop path could never execute, so ADR-014's one intervention was
	// wired but dead. L2 is taken on a later tick, only when policy named an
	// owned target and only once per rate-limit window.
	l2 := make([]model.Candidate, 0, 1)
	for i, c := range planned {
		if c.Kind == model.ActionStopOwned {
			l2 = append(l2, c)
			continue
		}
		id := fmt.Sprintf("%s-%d-%d", c.Kind, sys.At.Unix(), i)
		ar := p.Exec.Execute(ctx, executor.Decision{ID: id, IssuedAt: sys.At, Candidate: c})
		results = append(results, ar)
		if p.Verify != nil && ar.State == "executed" {
			p.Verify.Schedule(id, st.Score, 2*time.Minute, sys.At)
		}
		if p.Ledger != nil {
			_ = p.Ledger.Append(map[string]any{
				"kind": "action_result", "at": sys.At, "action_id": id,
				"action": string(c.Kind), "state": ar.State,
				"verified": "pending", "evidence": ar.Evidence,
				"expect_mb": c.ExpectBytes >> 20,
			})
		}
	}
	// L2 pass. The executor's guard revalidates mode, age, ownership and the
	// pre-authorization token before any signal is sent (ST-5/6/22/24), so a
	// candidate that slipped through here still cannot reach a foreign pid.
	for i, c := range l2 {
		id := fmt.Sprintf("l2-%s-%d-%d", c.Kind, sys.At.Unix(), i)
		ar := p.Exec.Execute(ctx, executor.Decision{ID: id, IssuedAt: sys.At, Candidate: c})
		results = append(results, ar)
		if p.Verify != nil && ar.State == "executed" {
			p.Verify.Schedule(id, st.Score, 60*time.Second, sys.At)
		}
		if p.Ledger != nil {
			_ = p.Ledger.Append(map[string]any{
				"kind": "action_result", "at": sys.At, "action_id": id,
				"action": string(c.Kind), "state": ar.State, "verified": "pending",
				"evidence": ar.Evidence, "target": c.TargetID, "tier": "L2",
			})
		}
	}
	return results
}

// PressureView is the subset of the estimator's output the pipeline needs,
// kept as a value so the pipeline never imports the estimator's types.
type PressureView struct {
	Level   model.PressureLevel
	Score   float64
	Quality model.Quality
}

// dominant picks the highest-confidence verdict, preferring suspicion on a
// tie: an unexcluded leak is more actionable than a workload-growth label.
func dominant(vs []classify.Verdict) classify.Verdict {
	if len(vs) == 0 {
		return classify.Verdict{Bucket: classify.Unknown, Basis: []string{"no group exceeded the growth floor in this window"}}
	}
	sort.SliceStable(vs, func(i, j int) bool {
		if vs[i].Confidence != vs[j].Confidence {
			return vs[i].Confidence > vs[j].Confidence
		}
		return suspicionRank(vs[i].Bucket) > suspicionRank(vs[j].Bucket)
	})
	return vs[0]
}

func suspicionRank(b classify.Bucket) int {
	switch b {
	case classify.LeakSuspicion:
		return 3
	case classify.WorkloadGrowth:
		return 2
	case classify.CacheOrRetrievable:
		return 1
	}
	return 0
}

// dominantGroupInfo reports the growth of the group the verdict came from, so
// a decision record can be reconstructed without re-reading attribution.
func dominantGroupInfo(vs []classify.Verdict) (growthMB int64, group string) {
	if len(vs) == 0 {
		return 0, ""
	}
	d := dominant(vs)
	return d.GrowthMB, d.GroupKey
}

// LastGroups returns the most recent window's per-group growth, for `status`.
func (p *Pipeline) LastGroups() []GroupSummary {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]GroupSummary, len(p.lastGroups))
	copy(out, p.lastGroups)
	return out
}

// LastWindow returns the most recent closed attribution window.
func (p *Pipeline) LastWindow() attribution.Window {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastWindow
}
