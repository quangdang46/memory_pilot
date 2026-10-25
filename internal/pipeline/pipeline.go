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

	// Selection beyond "top N by bytes" (see candidates): a group is also
	// judged when its growth has endured, or when its shape says something
	// ("cycling", "accumulating processes").
	MinEvidenceS                int64   // default 1200: the classifier's own threshold
	MinSustainedBps             float64 // default 50 KB/s: slow but real
	MinProcessesForAccumulation int     // default 8
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
	if c.MinEvidenceS <= 0 {
		c.MinEvidenceS = 1200
	}
	if c.MinSustainedBps <= 0 {
		c.MinSustainedBps = 50 << 10
	}
	if c.MinProcessesForAccumulation <= 0 {
		c.MinProcessesForAccumulation = 8
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
	w := p.Attr.CloseWindowAndReset(sys)
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

	// Which groups get judged. Ranking by absolute growth alone loses the one
	// shape the product exists to find: on a busy machine a sustained 6 MB/s
	// grower never places against a browser reporting 2 GB per window. It was
	// measured — five labelled workloads ran 40 minutes and none was
	// classified while Chrome was classified four windows running
	// (evidence/2026-10-25-run3).
	//
	// So the candidate set is a union: the top N by absolute growth (the
	// original behaviour, kept), plus every group whose *sustained* growth is
	// material regardless of size, plus any group cycling or accumulating
	// processes. Ranking finds the big; coverage of the shapes finds the
	// dangerous.
	candidates := p.candidates(w)

	var verdicts []classify.Verdict
	for _, cand := range candidates {
		g := cand.group
		st := cand.stats
		// A group that has stopped growing is exactly the group whose earlier
		// verdict may be wrong: it was called a leak while climbing and nobody
		// re-examines it now that it plateaued. Filtering it out here is why a
		// cache can never be re-judged as a cache. So the floor applies only to
		// groups that are NEW to us; a group with prior history is re-judged
		// on the evidence it now presents.
		hasHistory := st.DurationS > 0
		if !hasHistory && st.GrowthBytes < p.Cfg.MinGroupGrowthBytes &&
			st.ReleaseBytes < p.Cfg.MinGroupGrowthBytes {
			continue // genuinely quiet newcomer; nothing to say
		}
		v := classify.Classify(classify.SeriesStats{
			GroupKey: st.GroupKey, SlopeBytesS: st.SlopeBytesS, Accel: st.Accel,
			PlateauS: st.PlateauS, ActivityCorr: st.ActivityCorr,
			ActivityS: st.ActivityS, QuietS: st.QuietS, CacheRole: st.CacheRole,
			CeilingBytes: st.CeilingBytes, DurationS: st.DurationS,
			CoverageRatio: st.CoverageRatio,
			// The two signals that were measured and then dropped on the
			// floor: release volume (clears a cycling cache) and process
			// count (explains growth that arrives as more processes).
			GrowthBytes: st.GrowthBytes, ReleaseBytes: st.ReleaseBytes,
			ProcessCount: st.ProcessCount, PeakProcessCount: st.PeakProcessCount,
			LongGrowthBytes: st.LongGrowthBytes, LongPeakProcesses: st.LongPeakProcesses,
			NetGrowthBytes: st.NetGrowthBytes,
		})
		decidedMB := decidedGrowthMB(v.Bucket, st.GrowthBytes, st.LongGrowthBytes)
		v.GrowthMB = decidedMB
		verdicts = append(verdicts, v)
		if p.Ledger != nil {
			_ = p.Ledger.Append(map[string]any{
				"kind": "classification", "at": sys.At, "group": v.GroupKey,
				"bucket": v.Bucket, "confidence": v.Confidence,
				"basis": v.Basis, "alternatives": v.Alternatives,
				"growth_mb": decidedMB, "processes": g.Processes,
				// The inputs behind the verdict, so a reader can check the
				// reasoning instead of taking the sentence on faith.
				"signals": map[string]any{
					"plateau_s": st.PlateauS, "duration_s": st.DurationS,
					"quiet_s": st.QuietS, "active_s": st.ActivityS,
					"activity_corr": st.ActivityCorr,
					"growth_bytes":  st.GrowthBytes, "release_bytes": st.ReleaseBytes,
					// The long-horizon pair the accumulation and cycling
					// branches actually decide on. Leaving them out made a
					// verdict impossible to check from its own record: a
					// subprocess advisory showed 134 MB of window growth
					// against a 256 MB threshold and looked like a bug.
					"long_growth_bytes":   st.LongGrowthBytes,
					"net_growth_bytes":    st.NetGrowthBytes,
					"long_peak_processes": st.LongPeakProcesses,
					"process_count":       st.ProcessCount,
					"peak_processes":      st.PeakProcessCount,
					"slope_bps":           st.SlopeBytesS,
				},
				"text": classify.RenderVerdict(v, g.GrowthBytes>>20),
			})
		}
	}
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

// candidate is one group selected for classification, with the series stats
// already computed (they are the expensive part and the selection needs them).
type candidate struct {
	group attribution.Group
	stats attribution.SeriesStats
	why   string // which rule selected it, recorded for explainability
}

// candidates decides which groups get judged.
//
// The rule that was here before — top N by absolute growth — was measured
// wrong on a loaded machine: five labelled workloads ran 40 minutes and none
// was classified, because a browser and a terminal reported thousands of MB
// per window and the probes reported tens. The processes being warned about
// and the processes growing without end were disjoint sets.
//
// Selection is now a union of three independent reasons to look at a group:
//
//  1. size     — top N by absolute growth (keeps the old behaviour)
//  2. endurance — sustained growth over the long horizon, whatever its size,
//     because a slow leak is exactly what outlives its catch
//  3. shape    — the group is cycling (releasing as much as it grows) or
//     accumulating processes, which are verdicts in themselves
//
// A group selected by any of these is classified once; the reason is recorded
// in the verdict's basis so a reader can see why it was examined.
// attrStats prefers the stats captured when the window closed. SeriesFor reads
// the live counters, which CloseWindowAndReset has already zeroed by the time
// the window is classified — so a group that grew 98 MB in the window it was
// judged on would present GrowthBytes=0, SlopeBytesS=0, and no branch that
// depends on growth could fire. Falls back for callers outside window close
// (tests, status queries between windows).
func (p *Pipeline) attrStats(anchor string) attribution.SeriesStats {
	if st, ok := p.Attr.WindowStats(anchor); ok {
		return st
	}
	return p.Attr.SeriesFor(anchor)
}

// decidedGrowthMB is the number a verdict's sentence may quote: the growth
// measured over the horizon that decided it. Accumulation and suspicion read
// lifetime growth, so quoting the current window produced "grew by 0 MB —
// unexplained sustained growth" (observed at 14:36 on 2.1.310, whose own basis
// said 409 MB). Workload and cache are window judgements and keep the window.
func decidedGrowthMB(b classify.Bucket, windowBytes, longBytes int64) int64 {
	switch b {
	case classify.SubprocessAccumulation, classify.LeakSuspicion:
		if longBytes > 0 {
			return longBytes >> 20
		}
	}
	return windowBytes >> 20
}

func (p *Pipeline) candidates(w attribution.Window) []candidate {
	seen := map[string]candidate{}
	add := func(g attribution.Group, why string) {
		if _, ok := seen[g.AnchorKey]; ok {
			return
		}
		seen[g.AnchorKey] = candidate{group: g, why: why}
	}

	// 1. size — unchanged behaviour, kept so big movers are never dropped.
	for i, g := range w.Groups {
		if i >= p.Cfg.TopN {
			break
		}
		add(g, "top-growth")
	}
	// 1b. re-judge groups with prior history, even if they are quiet now.
	// A group that grew and then plateaued has a verdict from when it was
	// climbing; without this it is never revisited and a stale "leak" can
	// never be corrected to "cache". These come from Window.Flat, which
	// deliberately keeps them out of the growth ranking.
	for _, g := range w.Flat {
		st := p.attrStats(g.AnchorKey)
		if st.DurationS > 0 {
			addWithStats(&seen, g, st, "re-judge")
		}
	}

	// 2 & 3. endurance and shape, over every group in the window.
	for _, g := range w.Groups {
		st := p.attrStats(g.AnchorKey)
		// endurance: growth that has continued for a long time at a rate
		// worth caring about. MinSustainedBps is deliberately low: the point
		// is to catch the group nobody notices, not the loud one.
		if st.DurationS >= p.Cfg.MinEvidenceS && st.SlopeBytesS >= p.Cfg.MinSustainedBps {
			addWithStats(&seen, g, st, "sustained-growth")
		}
		// shape: cycling groups are caches regardless of size.
		if st.ReleaseBytes > 0 && st.ReleaseBytes >= st.GrowthBytes &&
			st.GrowthBytes >= p.Cfg.MinGroupGrowthBytes {
			addWithStats(&seen, g, st, "cycling")
		}
		// shape: process accumulation.
		if st.PeakProcessCount >= p.Cfg.MinProcessesForAccumulation {
			addWithStats(&seen, g, st, "process-accumulation")
		}
	}

	out := make([]candidate, 0, len(seen))
	for _, c := range seen {
		if c.stats.GroupKey == "" {
			c.stats = p.attrStats(c.group.AnchorKey)
		}
		out = append(out, c)
	}
	// Stable order: biggest first, so the report reads naturally.
	sort.Slice(out, func(i, j int) bool {
		return out[i].group.GrowthBytes > out[j].group.GrowthBytes
	})
	return out
}

func addWithStats(seen *map[string]candidate, g attribution.Group,
	st attribution.SeriesStats, why string) {
	if _, ok := (*seen)[g.AnchorKey]; ok {
		return
	}
	(*seen)[g.AnchorKey] = candidate{group: g, stats: st, why: why}
}
