// mempilotd — the daemon. Constructs the graph per module-contracts §7:
// no package self-registers; construction validates config; failure ⇒ exit 3
// before any goroutine starts.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"memory_pilot/internal/attribution"
	"memory_pilot/internal/classify"
	"memory_pilot/internal/clock"
	"memory_pilot/internal/config"
	"memory_pilot/internal/executor"
	"memory_pilot/internal/forecast"
	"memory_pilot/internal/ipc"
	"memory_pilot/internal/ledger"
	model "memory_pilot/internal/model"
	"memory_pilot/internal/pipeline"
	"memory_pilot/internal/platform/macos"
	"memory_pilot/internal/policy"
	"memory_pilot/internal/pressure"
	"memory_pilot/internal/relief"
	"memory_pilot/internal/reporting"
	"memory_pilot/internal/selfmonitor"
	"memory_pilot/internal/store"
	"memory_pilot/internal/supervisor"
	"memory_pilot/internal/verify"
	"memory_pilot/launchd"
	"memory_pilot/platform"
)

func main() {
	dataRoot := flag.String("data-root", defaultRoot(), "daemon data directory (SI-3 boundary)")
	cfgPath := flag.String("config", "", "optional config file")
	flag.Parse()

	if err := run(*dataRoot, *cfgPath); err != nil {
		fmt.Fprintln(os.Stderr, "mempilotd:", err)
		os.Exit(exitFor(err))
	}
}

func defaultRoot() string {
	home, _ := os.UserHomeDir()
	return home + "/.local/share/mempilot"
}

func exitFor(err error) int {
	if _, ok := err.(configError); ok {
		return 3
	}
	return 1
}

type configError struct{ err error }

func (c configError) Error() string { return c.err.Error() }

func run(dataRoot, cfgPath string) error {
	var cfg config.Config
	if cfgPath != "" {
		c, err := config.Load(cfgPath)
		if err != nil {
			return configError{err}
		}
		cfg = c
	} else {
		cfg = config.DefaultConfig(dataRoot)
	}
	cfg.DataRoot = dataRoot
	if err := cfg.Validate(); err != nil {
		return configError{err}
	}

	lock, err := supervisor.SingleInstance(cfg.DataRoot)
	if err != nil {
		return err
	}
	defer lock.Close()

	st, err := store.New(cfg.DataRoot)
	if err != nil {
		return err
	}
	_ = st
	lg, err := ledger.Open(cfg.DataRoot, "ledger")
	if err != nil {
		return err
	}
	plat, err := platform.NewPlatform()
	if err != nil {
		return err
	}

	clk := clock.Real()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mon := selfmonitor.New(clk, cfg.Overrides)

	est := pressure.New()
	fc := forecast.New(criticalFreeBytes(plat))
	eng := policy.NewEngine(policy.Mode(cfg.Mode))
	reg := executor.NewRegistry()
	guard := &executor.ActionGuard{
		Mode:     func() policy.Mode { return policy.Mode(cfg.Mode) },
		Registry: reg, MaxAge: 30 * time.Second,
	}
	ex := executor.New(guard)
	// Real side effects for the self-relief actions. Reporting "executed" for
	// an action that released nothing is a false success (SI-5); these
	// actors measure what they changed.
	var ownCache atomic.Int64
	ex.Actors = executor.Actors{
		TrimOwnBuffers: func() (executor.Effect, error) {
			freed := ownCache.Swap(0)
			if freed == 0 {
				return executor.Effect{Note: "no own buffers held", Changed: false}, nil
			}
			debug.FreeOSMemory() // return released pages to the OS, not just the Go heap
			return executor.Effect{Bytes: freed, Note: "own aggregation caches released", Changed: true}, nil
		},
		RotateOwnLedger: func() (executor.Effect, error) {
			before := lg.SizeBytes()
			if err := lg.Rotate(); err != nil {
				return executor.Effect{}, err
			}
			return executor.Effect{Note: "ledger rotated", Changed: true,
				Bytes: before}, nil
		},
	}
	ver := verify.New()
	pipe := pipeline.New(pipeline.Config{WindowS: attributionWindowS},
		lg, eng, ex, ver)

	handler := &daemonHandler{lg: lg, mon: mon, dataRoot: cfg.DataRoot, mode: cfg.Mode,
		pipe: pipe, registry: reg, plat: plat, startedAt: time.Now()}

	// supervisor main loop: two-cadence collection, quality-stamped, into ledger
	go mon.Run(ctx, lg.SizeBytes)
	sysDone := make(chan struct{})
	go func() {
		defer close(sysDone)
		t := clk.NewTicker(cfg.Overrides.SystemSampleEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C():
				s, err := plat.SampleSystem(ctx)
				if err != nil {
					// record the failure as an event — missing evidence is visible (SI-4)
					lg.Append(map[string]any{"kind": "system_sample_error", "at": time.Now().UTC(), "error": err.Error()})
					continue
				}
				lg.Append(map[string]any{"kind": "system", "sample": s})
				handler.runPipeline(est, fc, ver, plat, s)
			}
		}
	}()
	procDone := make(chan struct{})
	go func() {
		defer close(procDone)
		t := clk.NewTicker(cfg.Overrides.ProcessSampleEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C():
				snap, err := plat.Processes(ctx)
				if err != nil {
					lg.Append(map[string]any{"kind": "process_sample_error", "at": time.Now().UTC(), "error": err.Error()})
					continue
				}
				// Per-process detail never went to disk before: the ledger
				// recorded only counts, so every attribution question ("which
				// group grew?") was unanswerable after the fact. The pipeline
				// summarizes it per window; this line keeps the coverage
				// evidence for that window.
				lg.Append(map[string]any{"kind": "processes", "coverage": snap.Coverage, "n": len(snap.Processes), "at": snap.At})
				// Two sources of process facts: our own sampling, and launchd's
				// list of jobs. The second is what separates an orphan from a
				// service — macOS reparents both to pid 1. Two seconds, once
				// per process tick, and a failure is passed as "unknown" so no
				// orphan claim is invented from a missing answer.
				if pids, ok := macos.LaunchdJobPids(ctx); ok {
					handler.pipe.Attr.SetLaunchdJobs(pids, true)
				} else {
					handler.pipe.Attr.SetLaunchdJobs(nil, false)
				}
				handler.pipe.ObserveProcesses(snap, handler.lastSystem())
				// Reap owned workloads whose wrapper died without admit_exit
				// (forced kill, crash). Reuses this snapshot rather than paying
				// for a second full process scan on the faster system tick. A
				// dead pid must not stay stop-authorized: the kernel reuses pids.
				if handler.registry != nil {
					live := make(map[int32]bool, len(snap.Processes))
					for _, p := range snap.Processes {
						live[p.Identity.Pid] = true
					}
					handler.mu.Lock()
					n := handler.registry.Reap(func(pid int32) bool { return live[pid] })
					if n > 0 {
						handler.pipe.Owned = handler.registry.List()
					}
					handler.mu.Unlock()
					if n > 0 {
						_ = lg.Append(map[string]any{"kind": "admission_reaped", "at": snap.At, "count": n})
					}
				}
			}
		}
	}()

	// IPC server with the handler backed by live state
	socket := cfg.DataRoot + "/mempilot.sock"
	ipcSrv, err := ipc.Serve(ctx, socket, handler)
	if err != nil {
		return err
	}

	// signal handling: shutdown order = intake → collector → ledger flush → IPC
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	<-sig
	cancel()
	<-sysDone
	<-procDone
	lg.Close()
	ipcSrv.Path()
	os.Remove(socket)
	return nil
}

// runPipeline: collector → estimator → forecast → attribution → classification
// → policy → executor → ledger. Every decision is reconstructible from the
// ledger alone (bead 7eb), and an executed action always carries a result row.
func (h *daemonHandler) runPipeline(est *pressure.Estimator, fc *forecast.Forecaster,
	ver *verify.Verifier, plat platform.Platform, s model.SystemSample) {
	lg := h.lg
	totalPages := uint64(0)
	if info := plat.Info(); info.TotalRAMBytes > 0 && s.PageBytes > 0 {
		totalPages = info.TotalRAMBytes / s.PageBytes
	}
	st := est.Update(pressure.Input{
		Sample: s, TotalPages: totalPages,
		SwapOK: true, DiskOK: false,
	})
	if st.Transition != nil {
		lg.Append(map[string]any{"kind": "pressure_transition", "from": st.Transition.From,
			"to": st.Transition.To, "reason": st.Transition.Reason, "score": st.Score, "at": s.At})
	}
	fo := fc.Update(forecast.Sample{
		MonotonicMs:     s.MonotonicMs,
		HeadroomBytes:   float64(s.FreePages) * float64(s.PageBytes),
		CompressorPages: float64(s.CompressorPages),
	})
	lg.Append(map[string]any{"kind": "decision", "at": s.At,
		"state": st.State, "provenance": st.Provenance, "score": st.Score,
		"tte_s": fo.TteS, "confidence": fo.Confidence, "actionable": fo.Actionable})
	h.mu.Lock()
	h.lastState, h.lastProvenance, h.lastScore = string(st.State), st.Provenance, st.Score
	h.lastLevel = int(s.KernelLevel)
	h.lastFreeMB = float64(s.FreePages) * float64(s.PageBytes) / 1e6
	h.lastTteS, h.lastLowerS, h.lastConf = fo.TteS, fo.LowerBoundS, fo.Confidence
	h.lastCalibrated, h.lastActionable = fo.Calibrated, fo.Actionable
	h.lastSample = s
	h.lastForecast = forecast.ToModel(fo, s.At)
	h.mu.Unlock()

	// Window boundary: classify what grew, then let policy decide. Polar
	// before this point the daemon wrote "attribution pending" every 5 s and
	// never classified anything — the report the user actually needs.
	// Resolve verification windows that have elapsed. Without this the
	// verifier only ever accumulated scheduled actions and no action ever got
	// a confirmed/refuted verdict.
	if n := ver.ResolveDue(st.Score, s.At); n > 0 {
		_ = lg.Append(map[string]any{"kind": "verification", "at": s.At,
			"resolved": n, "score_after": st.Score})
	}

	verdicts := h.maybeCloseWindow(s)
	results := h.pipe.Decide(context.Background(), s,
		pipeline.PressureView{Level: s.KernelLevel, Score: st.Score, Quality: s.MachStatsQuality},
		forecast.ToModel(fo, s.At), verdicts)
	h.recordVerdicts(s, verdicts, results)
	h.recommendRelief(s, st.Score)
}

// recommendRelief runs L1 when the machine is actually under pressure.
//
// This is the tier the design specifies and the product was missing: not "this
// group grew", but "closing these processes would return this much, and here is
// whether that covers the gap". It runs only at Warn and above, because that is
// when the answer matters — at Normal, every group is the cost of doing work
// and there is nothing to decide.
func (h *daemonHandler) recommendRelief(s model.SystemSample, score float64) {
	if s.KernelLevel < model.LevelWarn {
		return
	}
	// One recommendation per cooldown. The condition changes slowly and the
	// answer does not get more true by repeating it.
	//
	// The cooldown is claimed only when a recommendation is actually emitted.
	// Claiming it up front spent the whole 20 minutes on a window with nothing
	// to say: measured after the 00:17 restart, the daemon was at Warn for 55 s
	// — with one second of process history, so no member could be eligible —
	// and that empty window would have silenced the first one that had a name
	// to offer.
	h.mu.Lock()
	tooSoon := !h.lastReliefAt.IsZero() && s.At.Sub(h.lastReliefAt) < reliefCooldown
	h.mu.Unlock()
	if tooSoon {
		return
	}

	r := h.buildRecommendation(s)
	if r == nil || len(r.Candidates) == 0 {
		return
	}
	cs := r.Candidates
	text := r.Text()
	if viol := classify.PhraseTest(text); len(viol) > 0 {
		text = "relief recommendation withheld by the phrase test"
	}
	h.emitReliefIfChanged(s.At, text, r.TotalRelief>>20, r.Shortfall>>20, len(cs), s.KernelLevel)
}

// emitReliefIfChanged records a recommendation only when it differs from the
// last one delivered.
//
// The cooldown bounds how often the daemon computes; this bounds how often the
// owner is told the same thing. Measured: five identical recommendations in
// eighteen minutes, naming the same five pids and the same 601 MB, differing
// only in the shortfall — the repeating warning the owner asked never to see
// again, produced by the tool rather than by the machine.
func (h *daemonHandler) emitReliefIfChanged(at time.Time, text string, reliefMB, shortfallMB int64, cands int, level model.PressureLevel) {
	h.mu.Lock()
	same := reliefActionKey(text) == h.lastReliefText
	escalated := level > h.lastReliefLevel
	h.lastReliefAt = at
	h.lastReliefText = reliefActionKey(text)
	h.lastReliefLevel = level
	h.mu.Unlock()
	// Escalation is news even when the answer has not changed. Measured: the
	// machine reached Critical for the first time in this project while the
	// recommendation was byte-identical to one delivered at Warn, so the dedup
	// held it back — and Critical is exactly when the owner needs to know the
	// gap cannot be closed.
	if same && !escalated {
		return
	}
	_ = h.lg.Append(map[string]any{"kind": "relief_recommendation", "at": at,
		"kernel_level": int(level), "escalated": escalated,
		"shortfall_mb": shortfallMB, "total_relief_mb": reliefMB,
		"sufficient": shortfallMB <= reliefMB, "candidates": cands, "text": text})
}

// buildRecommendation is the whole L1 computation, with no gate on pressure and
// no cooldown. recommendRelief wraps it for the automatic path; the `relief` IPC
// op calls it directly so the owner can ask "what would you do now?" without
// waiting for the machine to reach Warn. Same members, same bounds, same
// sentence — the answer does not depend on who asked.
func (h *daemonHandler) buildRecommendation(s model.SystemSample) *relief.Recommendation {
	members := h.pipe.Attr.Members()
	if len(members) == 0 {
		return nil
	}
	facts := make([]relief.Member, 0, len(members))
	for _, m := range members {
		facts = append(facts, relief.Member{
			Pid: m.Pid, Exe: m.Exe, Footprint: m.Footprint,
			QuietFor: m.QuietFor, ParentAlive: m.ParentAlive, ParentGone: m.ParentGone,
			CpuTotalS: m.CpuTotalS, AgeS: m.AgeS,
		})
	}
	// Bound every quiet claim by what the sampler actually recorded. Seven
	// minutes after a restart, unmodified, this produced 43 processes "idle for
	// 6m45s" — the age of the harness, not the silence of the processes.
	observed := time.Duration(h.pipe.Attr.QuietBoundS()) * time.Second
	cs := relief.AssessMembers(relief.GroupFacts{Members: facts, ObservedFor: observed})
	if len(cs) == 0 {
		return &relief.Recommendation{At: s.At, Severity: pressureName(s.KernelLevel)}
	}
	relief.Rank(cs)

	shortfall := int64(0)
	if info := h.plat.Info(); info.TotalRAMBytes > 0 {
		// The gap is what would have to be returned to leave the machine
		// comfortable again: the shortfall against a fifth of RAM free.
		free := int64(s.FreePages) * int64(s.PageBytes)
		want := int64(0.2 * float64(info.TotalRAMBytes))
		if free < want {
			shortfall = want - free
		}
	}
	r := &relief.Recommendation{At: s.At, Severity: pressureName(s.KernelLevel),
		Shortfall: shortfall, Candidates: cs}
	for _, c := range cs {
		if c.State == relief.StateAbandoned {
			r.TotalRelief += c.ExpectedReliefBytes
		}
	}
	return r
}

// reliefActionKey reduces a recommendation to what the owner would act on: the
// named pids and the relief they add up to. The shortfall moves by tens of MB
// between samples while nothing the owner can do has changed — measured, two
// recommendations five minutes apart differed only in "3134 MB" against "3202
// MB", with the same five pids and the same 601 MB. Comparing whole sentences
// treated that noise as news.
func reliefActionKey(text string) string {
	var b strings.Builder
	for _, line := range strings.Split(text, "\n") {
		// The per-process lines and the total; not the shortfall.
		if strings.Contains(line, "(pid ") || strings.HasPrefix(line, "closing ") {
			b.WriteString(strings.TrimSpace(line))
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// pressureName renders a level for the record.
func pressureName(l model.PressureLevel) string {
	switch l {
	case model.LevelNormal:
		return "Normal"
	case model.LevelWarn:
		return "Warn"
	case model.LevelCritical:
		return "Critical"
	}
	return "Unknown"
}

// reliefCooldown bounds how often L1 speaks. Twenty minutes is long enough
// that a recommendation is news each time it appears, and short enough that a
// worsening situation is re-reported within one reporting window.
// reliefCooldown bounds how often the automatic L1 answer is written.
//
// Measured on this machine over one day: 40 Warn episodes, median 0.3 minutes,
// longest 8.3 minutes, none reaching 20. A 20-minute cooldown therefore
// answered every episode at most once — and, because the first sample of an
// episode is also when the daemon has the least history, usually answered it
// at its weakest. Five minutes covers the same ground three times over and
// still cannot repeat a recommendation the owner has already had time to read.
const reliefCooldown = 5 * time.Minute

// attributionWindowS is the reporting window (5 min). Short enough that a
// report describes "just now"; long enough that a 15 s snapshot cadence gives
// it 20 samples per process.
const attributionWindowS = 300

// maybeCloseWindow closes the attribution window on its boundary and returns
// the verdicts for that window.
func (h *daemonHandler) maybeCloseWindow(s model.SystemSample) []classify.Verdict {
	h.mu.Lock()
	if !h.windowStart.IsZero() && s.At.Sub(h.windowStart) < attributionWindowS*time.Second {
		h.mu.Unlock()
		return nil
	}
	h.windowStart = s.At
	h.mu.Unlock()
	if h.pipe.Attr.ProcessCount() == 0 {
		return nil // nothing observed yet: not a "quiet window", just no data
	}
	return h.pipe.CloseWindow(s)
}

// recordVerdicts writes the advisory that names the growing groups. This is
// the sentence the user sees instead of the old "pending Phase 3 wiring" one.
func (h *daemonHandler) recordVerdicts(s model.SystemSample, vs []classify.Verdict, results []model.ActionResult) {
	if len(vs) == 0 {
		return
	}
	// L1, from process-relief-engine.md: record the finding, prepare a plan,
	// and do not act. The design is explicit that an advisory is owed to the
	// owner only when there is something they can do about it — otherwise the
	// tool is narrating their own work back at them.
	//
	// Measured before this change: 98 advisories in 45 minutes, every one at
	// Normal pressure, most naming groups that were actively working (VS Code
	// at 62% CPU, Chrome loading pages). That is the warning board the owner
	// asked never to see again.
	advisoryAt := h.advisoriesThisWindow(s.At)
	for _, v := range vs {
		// Only the shapes that need attention become advisories. A cache that
		// released as much as it grew, or growth explained by CPU work, is a
		// normal machine doing normal things; announcing it every window is
		// how a warning list becomes noise the user stops reading. Those
		// verdicts stay in the ledger and in `report` - they are just not
		// warnings.
		// A terminal's children are the work the user started, not something
		// the application spawned for itself. "wezterm-gui is accumulating
		// subprocesses" describes every terminal on every machine; measured
		// here at 86 descendants and 8 GB, all of it the owner's own editors,
		// agents and builds. The group is still measured and still reported.
		if attribution.IsTerminalGroup(v.GroupKey) {
			continue
		}
		switch v.Bucket {
		case classify.LeakSuspicion, classify.SubprocessAccumulation:
			// worth surfacing
		case classify.WorkloadGrowth:
			// only if it is large enough to matter
			if v.GrowthMB < advisoryWorkloadMB {
				continue
			}
		default:
			continue // cache, unknown: recorded, not announced
		}
		// Growth alone is not news while the machine is comfortable. An
		// advisory says "here is something you could do", and a growing group
		// with nothing releasable offers nothing. Rate-limited to one per group
		// per hour: repeating the same sentence every five minutes is how a
		// warning list becomes something the reader stops seeing.
		if !relief.ShouldRecommend(v.Bucket, s.KernelLevel >= model.LevelWarn, false) {
			continue
		}
		if last, ok := advisoryAt[v.GroupKey]; ok &&
			s.At.Sub(last) < advisoryRepeatEvery {
			continue
		}
		strength := "informational"
		if v.Bucket == classify.LeakSuspicion {
			strength = "recommendation"
		}
		text := classify.RenderVerdict(v, v.GrowthMB)
		if viol := classify.PhraseTest(text); len(viol) > 0 {
			// SI-5 guard: never let a conclusion slip into report text.
			text = "group " + v.GroupKey + " growth recorded; wording suppressed by the phrase test"
		}
		// Record the pressure this was emitted at. Without it every later
		// question — "was that advisory at Warn or at Normal?", "did the gate
		// open?" — needs a timestamp join against the system samples, and the
		// answer is only as good as the samples that happen to bracket it.
		// Measured: four advisories had to be attributed to Warn by matching
		// them to samples within 5 s, and that is not evidence a reader can
		// check from the record itself.
		_ = h.lg.Append(map[string]any{"kind": "advisory", "at": s.At,
			"kernel_level": int(s.KernelLevel), "free_mb": (s.FreePages * s.PageBytes) >> 20,
			"strength": strength, "text": text,
			"group": v.GroupKey, "bucket": v.Bucket, "confidence": v.Confidence})
		h.markAdvised(v.GroupKey, s.At)
	}
	for _, ar := range results {
		_ = h.lg.Append(map[string]any{"kind": "action_effect", "at": s.At,
			"action": string(ar.Kind), "action_id": ar.ActionID,
			"state": ar.State, "evidence": ar.Evidence})
	}
}

// advisoryWorkloadMB is the size above which explained growth is still worth
// one line. Below it the verdict stays in the ledger only.
const advisoryWorkloadMB = 2048

// advisoryRepeatEvery bounds how often the same group can be announced. An
// advisory is a request for the owner's attention; repeating it every window
// for a condition that has not changed spends that attention for nothing.
const advisoryRepeatEvery = time.Hour

// advisoriesThisWindow returns the last time each group was announced.
//
// In memory, so a restart resets the limit. That is acceptable: a restart is
// itself a discontinuity in what the owner has been told, and re-announcing
// once afterwards costs one line rather than a ledger read on every window.
func (h *daemonHandler) advisoriesThisWindow(now time.Time) map[string]time.Time {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.lastAdvisory == nil {
		h.lastAdvisory = map[string]time.Time{}
	}
	out := make(map[string]time.Time, len(h.lastAdvisory))
	for k, v := range h.lastAdvisory {
		out[k] = v
	}
	return out
}

// markAdvised records that a group has been announced.
func (h *daemonHandler) markAdvised(group string, at time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.lastAdvisory == nil {
		h.lastAdvisory = map[string]time.Time{}
	}
	h.lastAdvisory[group] = at
}

// criticalFreeBytes: F_C fallback critical fraction of RAM (constants.md §1).
func criticalFreeBytes(p platform.Platform) float64 {
	if info := p.Info(); info.TotalRAMBytes > 0 {
		return 0.02 * float64(info.TotalRAMBytes)
	}
	return 335e6
}

type daemonHandler struct {
	lg        *ledger.Ledger
	mon       *selfmonitor.Monitor
	dataRoot  string
	mode      string
	pipe      *pipeline.Pipeline
	registry  *executor.Registry
	plat      platform.Platform
	startedAt time.Time

	mu                             sync.Mutex
	lastAdvisory                   map[string]time.Time
	lastReliefAt                   time.Time
	lastReliefText                 string
	lastReliefLevel                model.PressureLevel
	windowStart                    time.Time
	lastSample                     model.SystemSample
	lastForecast                   model.Forecast
	lastState                      string
	lastProvenance                 string
	lastScore                      float64
	lastLevel                      int
	lastFreeMB                     float64
	lastTteS, lastLowerS, lastConf float64
	lastCalibrated, lastActionable bool
}

// lastSystem returns the most recent system sample, for the process cadence
// which needs a headroom reading to reconcile against.
func (h *daemonHandler) lastSystem() model.SystemSample {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.lastSample
}

// UptimeSeconds reports daemon uptime for the status line.
func (h *daemonHandler) UptimeSeconds() int64 {
	return int64(time.Since(h.startedAt).Seconds())
}

func (h *daemonHandler) Handle(ctx context.Context, req ipc.Request) (ipc.Response, error) {
	switch req.Op {
	case "status":
		s := h.mon.Snapshot()
		h.mu.Lock()
		lastState, lastScore, lastProv := h.lastState, h.lastScore, h.lastProvenance
		lastLevel, lastFreeMB := h.lastLevel, h.lastFreeMB
		lastTte, lastLower, lastConf := h.lastTteS, h.lastLowerS, h.lastConf
		lastCal, lastAct := h.lastCalibrated, h.lastActionable
		h.mu.Unlock()

		lastPressure, score := "Unknown", 0.5
		if lastState != "" {
			lastPressure, score = lastState, lastScore
		}
		line := reporting.RenderStatus(reporting.StatusLine{
			PID: int32(os.Getpid()), UptimeS: h.UptimeSeconds(), Mode: h.mode,
			Pressure: lastPressure, Score: score, Provenance: lastProv,
			Reliable: lastState != "", SelfRSSMB: s.RSSMB, SelfCPUPct: s.CPUPct,
			LedgerMB: s.LedgerMB,
		})
		raw, _ := json.Marshal(map[string]any{
			"healthy": h.mon.Healthy(), "rss_mb": s.RSSMB, "cpu_pct": s.CPUPct,
			"goroutines": s.Goroutines, "ledger_mb": s.LedgerMB, "detail": s.Budget.Detail,
			"kernel_level": lastLevel, "free_mb": lastFreeMB, "line": line,
			"uptime_s": h.UptimeSeconds(), "mode": h.mode,
			"tte_s": lastTte, "lower_s": lastLower, "confidence": lastConf,
			"calibrated": lastCal, "actionable": lastAct,
			"groups": h.pipe.LastGroups(),
		})
		return ipc.Response{V: ipc.ProtocolVersion, OK: true, Data: raw}, nil
	case "forecast":
		h.mu.Lock()
		tte, lower, conf := h.lastTteS, h.lastLowerS, h.lastConf
		cal, act := h.lastCalibrated, h.lastActionable
		fo := h.lastForecast
		h.mu.Unlock()
		line := reporting.RenderForecast(reporting.ForecastLine{
			TteS: tte, LowerS: lower, Confidence: conf, Calibrated: cal, Actionable: act})
		raw, _ := json.Marshal(map[string]any{"tte_s": tte, "lower_s": lower,
			"confidence": conf, "calibrated": cal, "actionable": act,
			"rate_bps": fo.RateBytesS, "line": line})
		return ipc.Response{V: ipc.ProtocolVersion, OK: true, Data: raw}, nil
	case "relief":
		// The same computation the automatic path uses, with the pressure gate
		// and the cooldown removed. An owner asking "what could be released
		// now?" should not have to wait for the machine to reach Warn, and the
		// answer must not depend on who asked.
		s := h.lastSample
		s.At = time.Now()
		if s.PageBytes == 0 {
			if info := h.plat.Info(); info.TotalRAMBytes > 0 {
				s.PageBytes = info.PageSizeBytes
			}
		}
		r := h.buildRecommendation(s)
		if r == nil {
			raw, _ := json.Marshal(map[string]any{"text": "no processes observed yet"})
			return ipc.Response{V: ipc.ProtocolVersion, OK: true, Data: raw}, nil
		}
		text := r.Text()
		if viol := classify.PhraseTest(text); len(viol) > 0 {
			text = "relief recommendation withheld by the phrase test"
		}
		raw, _ := json.Marshal(map[string]any{
			"severity": r.Severity, "shortfall_mb": r.Shortfall >> 20,
			"total_relief_mb": r.TotalRelief >> 20, "sufficient": r.Sufficient(),
			"candidates": len(r.Candidates), "text": text})
		return ipc.Response{V: ipc.ProtocolVersion, OK: true, Data: raw}, nil
	case "report", "explain", "policy":
		recs, err := h.lg.ReadAll()
		if err != nil {
			return ipc.Response{V: ipc.ProtocolVersion, Error: err.Error()}, nil
		}
		want := map[string]bool{"decision": true, "pressure_transition": true,
			"advisory": true, "action_result": true, "action_effect": true,
			"attribution": true, "classification": true,
			"policy_candidates": true, "policy_no_action": true}
		// Scan by age, not by record count. At a 5 s system cadence plus a
		// 15 s process cadence, the newest 50 records cover barely two
		// minutes — less than one attribution window — so `report` found
		// nothing to show no matter how long the daemon had been running.
		// Keep the count bound for pathological bursts, and stop once the
		// records are old enough to cover several windows.
		const maxScan = 60000
		cutoff := time.Now().Add(-45 * time.Minute)
		var out []json.RawMessage
		scanned := 0
		for i := len(recs) - 1; i >= 0 && scanned < maxScan; i-- {
			scanned++
			var probe struct {
				Kind string    `json:"kind"`
				At   time.Time `json:"at"`
			}
			if json.Unmarshal(recs[i], &probe) != nil {
				continue
			}
			if !probe.At.IsZero() && probe.At.Before(cutoff) {
				break
			}
			if want[probe.Kind] {
				out = append(out, recs[i])
			}
		}
		raw, _ := json.Marshal(out)
		return ipc.Response{V: ipc.ProtocolVersion, OK: true, Data: raw}, nil
	case "doctor":
		probs := launchd.Doctor(h.dataRoot)
		if _, err := os.Stat(h.dataRoot + "/ledger.0001.jsonl"); err != nil {
			// The old code called st.Name() on a nil FileInfo, which panics
			// inside the IPC goroutine whenever the ledger was missing — the
			// diagnosis failed exactly when it was needed.
			probs = append(probs, "ledger missing/unwritable: "+err.Error())
		}
		raw, _ := json.Marshal(probs)
		return ipc.Response{V: ipc.ProtocolVersion, OK: true, Data: raw}, nil
	case "admit", "admit_exit":
		return h.handleAdmit(ctx, req)
	default:
		return ipc.Response{V: ipc.ProtocolVersion, Error: "unknown op"}, nil
	}
}

// handleAdmit records a managed launch (ADR-014). Two things make this safe:
// the pid comes from the wrapper's own spawn (never from a process scan), and
// the peer pid must match it — a different process cannot register someone
// else's pid as stop-authorized.
func (h *daemonHandler) handleAdmit(ctx context.Context, req ipc.Request) (ipc.Response, error) {
	peer, hasPeer := ipc.PeerPIDFrom(ctx)
	var arg struct {
		Pid        int32   `json:"pid"`
		AllowStop  bool    `json:"allow_stop"`
		Decision   string  `json:"decision"`
		HeadroomMB float64 `json:"headroom_mb"`
	}
	if err := json.Unmarshal(req.Arg, &arg); err != nil {
		return ipc.Response{V: ipc.ProtocolVersion, Error: "bad admit arg"}, nil
	}
	h.mu.Lock()
	reg := h.registry
	h.mu.Unlock()
	if reg == nil {
		return ipc.Response{V: ipc.ProtocolVersion, Error: "registry unavailable"}, nil
	}
	switch req.Op {
	case "admit":
		if arg.Pid <= 0 {
			return ipc.Response{V: ipc.ProtocolVersion, Error: "pid required"}, nil
		}
		// The wrapper process is the spawned child's parent. Anything else —
		// including a local script that dials the socket directly — is a
		// foreign caller trying to grant itself stop authorization over
		// someone else's pid (SI-1). Refuse and record the attempt. Fail
		// closed: without a peer pid we cannot prove parentage, and the only
		// process that legitimately calls this op always can be identified.
		parent := h.parentOf(arg.Pid)
		if !hasPeer || parent != peer {
			_ = h.lg.Append(map[string]any{"kind": "admission_refused", "at": time.Now(),
				"pid": arg.Pid, "peer_pid": peer, "actual_parent": parent,
				"peer_known": hasPeer,
				"reason":     "peer is not the parent of the registered pid (SI-1)"})
			return ipc.Response{V: ipc.ProtocolVersion,
				Error: "refused: caller is not the parent of that pid"}, nil
		}
		// Identity is (pid, start); the wrapper's child is young, so a
		// bsdinfo read gives us the real start time to revalidate against.
		start, exe := h.processIdentity(arg.Pid)
		auth := ""
		if arg.AllowStop && stopArmedForMode(h.mode, true) {
			auth = "allow-stop"
		}
		reg.Register(executor.OwnedPid{Pid: arg.Pid, Pgid: arg.Pid,
			StartSec: start.Unix(), StartUsec: int64(start.Nanosecond() / 1000),
			Auth: auth})
		_ = h.lg.Append(map[string]any{"kind": "admission", "at": time.Now(),
			"pid": arg.Pid, "exe": exe, "allow_stop": arg.AllowStop,
			"stop_armed": auth != "", "decision": arg.Decision,
			"headroom_mb": arg.HeadroomMB, "peer_pid": peer, "peer_verified": hasPeer})
	case "admit_exit":
		reg.Forget(arg.Pid)
		_ = h.lg.Append(map[string]any{"kind": "admission_exit", "at": time.Now(),
			"pid": arg.Pid, "peer_pid": peer})
	}
	h.mu.Lock()
	h.pipe.Owned = reg.List()
	h.mu.Unlock()
	raw, _ := json.Marshal(map[string]any{"ok": true})
	return ipc.Response{V: ipc.ProtocolVersion, OK: true, Data: raw}, nil
}

// processIdentity reads the start time of a pid we just spawned. Reading it
// from the platform keeps one implementation of the identity tuple.
func (h *daemonHandler) processIdentity(pid int32) (time.Time, string) {
	if h.plat == nil {
		return time.Now(), ""
	}
	snap, err := h.plat.Processes(context.Background())
	if err != nil {
		return time.Now(), ""
	}
	for _, p := range snap.Processes {
		if p.Identity.Pid == pid {
			return p.Identity.StartTime, p.Identity.Executable
		}
	}
	return time.Now(), ""
}

// parentOf returns the parent pid of a process, read from the same collector
// that supplies attribution (one implementation of process identity). -1
// means "unknown"; the admit path treats unknown as refuse.
func (h *daemonHandler) parentOf(pid int32) int32 {
	if h.plat == nil {
		return -1
	}
	snap, err := h.plat.Processes(context.Background())
	if err != nil {
		return -1
	}
	for _, p := range snap.Processes {
		if p.Identity.Pid == pid {
			return p.Identity.Ppid
		}
	}
	return -1
}

// stopArmedForMode: prior authorization is the user's flag, but it only arms
// in canary or enforce (ADR-014 §2 item 7). Observe records the request
// without ever arming it, so a mode change cannot retroactively authorize a
// stop that was launched under observe.
func stopArmedForMode(mode string, allowStop bool) bool {
	if !allowStop {
		return false
	}
	return mode == "canary" || mode == "enforce"
}
