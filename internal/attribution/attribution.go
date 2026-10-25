// Package attribution: MP-ALG-004. Identity-scoped deltas, never summed RSS;
// coverage is reported, never normalized to 100%; compression is a system-
// level contributor line; "large but stable" is separate from growth ranking.
package attribution

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"memory_pilot/internal/model"
)

// Identity — pid alone is not identity (§2).
type Identity struct {
	Pid       int32
	StartSec  int64
	StartUsec int64
	Exe       string
}

func (id Identity) Key() string {
	return fmt.Sprintf("%d:%d.%d:%s", id.Pid, id.StartSec, id.StartUsec, id.Exe)
}

type series struct {
	id        Identity
	ppid      int32
	prevBytes int64 // phys_footprint, not resident: see footprintAt
	prevCPU   float64
	cpuDelta  float64 // accumulated CPU seconds in the window
	alive     bool
	delta     int64 // accumulated positive growth in window
	release   int64 // accumulated frees, tracked separately
	deadTicks int   // consecutive samples the process was absent

	// Activity correlation counters (MP-ALG-005): a group that grows while
	// its processes burn CPU is doing work; a group that grows while they sit
	// idle is the only state that can support an "unexplained" reading.
	samples     int
	activeTicks int
	growTicks   int
	bothTicks   int
}

type Group struct {
	AnchorKey     string
	Label         string
	GrowthBytes   int64
	ReleaseBytes  int64
	Processes     int
	PeakDeltaRate float64
	LargestPid    int32
	TopExe        string // executable of the largest single contributor
	peakDelta     int64
}

type Window struct {
	At              time.Time
	WindowS         int64
	Groups          []Group // ranked by growth, never labeled "cause"
	LargeButStable  []Group // high RSS, zero delta — never in growth ranking
	ObservedGrowth  int64   // system headroom delta
	AccountedGrowth int64   // Σ positive per-process deltas
	CoverageRatio   float64 // may exceed 1 (shared pages) — never normalized
	CompressorBytes int64   // system-level contributor line (§3.2)
	Coverage        model.Coverage
	Quality         model.Quality
	Note            string // honest-language note attached to every report

	// Flat are groups that did not move this window. Excluded from the growth
	// ranking (a stable process is not a problem), but still eligible for
	// classification: a group that grew and then stopped is a cache, and
	// re-judging it is the only way its earlier verdict can be corrected.
	Flat []Group
}

type Attributor struct {
	// mu guards every field below. Two goroutines feed this type on different
	// cadences: the process sampler (15 s) appends snapshots while the system
	// sampler (5 s) closes windows and reads series. Without a lock that is a
	// concurrent map read and write — which crashed the live daemon with
	// "fatal error: concurrent map read and map write" the first time the
	// pipeline actually ran end to end.
	mu             sync.Mutex
	series         map[string]*series
	groups         map[string]string // identity key -> anchor group id (sticky)
	parents        map[int32]int32   // pid -> ppid from the latest snapshot
	windowS        int64
	sampleDT       float64 // seconds between the last two snapshots
	lastSys        *model.SystemSample
	windowSysStart *model.SystemSample
	lastSnapAt     time.Time
	firstSnapAt    time.Time // first snapshot ever; separates "arrived" from "was always here"
	observedGrowth int64
	lastCoverage   model.Coverage
	lastQuality    model.Quality

	// History is the long-horizon record the classifier needs. A 5-minute
	// report window can never satisfy a 20-minute "sustained growth"
	// requirement, so a 300 s window always classified as "too short" and
	// the daemon was honest but useless. History accumulates across windows
	// and resets only when the group itself resets (see Reset).
	history map[string]*history
	now     time.Time
}

// history spans multiple report windows. Buckets keep the window boundary
// from erasing evidence of what happened before it.
type history struct {
	firstSeen    time.Time
	lastSeen     time.Time // last sample where the group had a live member
	lastGrowth   time.Time // last sample where the group grew
	lastActivity time.Time // last sample where any member burned CPU
	growthBytes  int64     // total positive growth since firstSeen
	releaseBytes int64     // total frees since firstSeen
	peakProcs    int       // high-water process count
	activeS      int64
	quietS       int64
	peakGrowthMB int64
}

func New(windowS int64) *Attributor {
	return &Attributor{series: map[string]*series{}, groups: map[string]string{},
		parents: map[int32]int32{}, history: map[string]*history{}, windowS: windowS}
}

// Observe feeds one process snapshot + system sample. Deltas are computed only
// across identical identities (never across identity change — §2 rule 4).
func (a *Attributor) Observe(snap model.ProcessSnapshot, sys model.SystemSample) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.now = snap.At
	a.parents = make(map[int32]int32, len(snap.Processes))
	for _, p := range snap.Processes {
		a.parents[p.Identity.Pid] = p.Identity.Ppid
	}
	if !a.lastSnapAt.IsZero() {
		if dt := snap.At.Sub(a.lastSnapAt).Seconds(); dt > 0 {
			a.sampleDT = dt
		}
	}
	if a.firstSnapAt.IsZero() {
		a.firstSnapAt = snap.At
	}
	a.lastSnapAt = snap.At
	// Per-tick, per-group activity flags. The tick is the unit: a group is
	// "active" if any member burned CPU, and "quiet" if no member did. Summing
	// per-process seconds inflated big groups by their process count.
	tickActive := map[string]bool{}
	tickGrew := map[string]bool{}
	for _, p := range snap.Processes {
		id := Identity{Pid: p.Identity.Pid,
			StartSec: p.Identity.StartTime.Unix(), StartUsec: int64(p.Identity.StartTime.Nanosecond() / 1000),
			Exe: p.Identity.Executable}
		key := id.Key()
		s, ok := a.series[key]
		now := int64(footprintAt(p))
		d := int64(0)
		if ok {
			d = now - s.prevBytes
		}
		if !ok {
			// A process first seen is baseline, not growth — with one
			// exception: a process that *started while we were watching*
			// arrives holding its memory, and that arrival is growth. The
			// group grew by the size of a new member.
			//
			// Both halves matter. Counting every first sighting as growth
			// turns the daemon's own startup into 20 GB of "growth" (the
			// machine was already full of processes); counting none makes
			// process accumulation invisible — measured in run16,
			// probe-children held 127 MB across nine processes while the
			// window reported growth 5 MB, because each child was spawned at
			// full size and only its later change was ever counted.
			if a.firstSnapAt.IsZero() || time.Unix(id.StartSec, id.StartUsec*1000).After(a.firstSnapAt) {
				d = now
			}
			s = &series{id: id, ppid: p.Identity.Ppid, alive: true}
			a.series[key] = s
		}
		s.prevBytes = now
		s.alive = true
		s.ppid = p.Identity.Ppid // reparenting is recorded, membership is sticky (§4.2)
		if d > 0 {
			s.delta += d
		} else {
			s.release += -d
		}

		// Activity sample: CPU-time delta is the only "is it working?" signal
		// available without pwrites into another process's memory.
		cpu := p.CpuUserS + p.CpuSystemS
		cpuD := cpu - s.prevCPU
		s.prevCPU = cpu
		s.cpuDelta += cpuD
		s.samples++
		active := cpuD > activityCPUS
		grew := d > 0
		if active {
			s.activeTicks++
		}
		if grew {
			s.growTicks++
		}
		if active && grew {
			s.bothTicks++
		}

		// Long-horizon record (survives window boundaries). Per-process
		// activity is folded into the group *after* the loop: adding
		// sampleDT once per process multiplied a 61-process group's quiet
		// time by 61, which is how "quiet for 45 minutes" turned into
		// "quiet for 11814 s" and made every big group look like a leak.
		gid := a.groupFor(id)
		if a.history[gid] == nil {
			a.history[gid] = &history{firstSeen: snap.At}
		}
		a.history[gid].lastSeen = snap.At
		if d > 0 {
			a.history[gid].lastGrowth = snap.At
		}
		if active {
			if !tickActive[gid] {
				tickActive[gid] = true
				a.history[gid].lastActivity = snap.At
			}
		}
		if grew {
			tickGrew[gid] = true
			a.history[gid].growthBytes += d
		} else if d < 0 {
			// Release must be accumulated over the long horizon too. The
			// window-scoped release resets every 5 minutes, so a group that
			// allocates and frees repeatedly (a sawtooth) showed release=0 in
			// every window where the freeing happened to land in the previous
			// one — and was classified a leak. Measured: probe-sawtooth
			// reported release=0 in all four windows while continuously
			// releasing memory.
			a.history[gid].releaseBytes += -d
		}
		a.history[gid].peakGrowthMB = maxInt64(a.history[gid].peakGrowthMB, d>>20)
	}
	// mark disappeared series ended (rule 2) — deltas stop accumulating
	for _, s := range a.series {
		s.alive = false
	}
	// second pass marks seen ones alive again
	for _, p := range snap.Processes {
		id := Identity{Pid: p.Identity.Pid,
			StartSec: p.Identity.StartTime.Unix(), StartUsec: int64(p.Identity.StartTime.Nanosecond() / 1000),
			Exe: p.Identity.Executable}
		if s, ok := a.series[id.Key()]; ok {
			s.alive = true
		}
	}
	// Process count per group this tick, for accumulation detection.
	tickProcs := map[string]int{}
	for _, s := range a.series {
		if s.alive {
			tickProcs[a.groupFor(s.id)]++
		}
	}
	// Fold this tick into each group's long-horizon record, once per tick.
	dt := int64(a.sampleDT)
	for gid, h := range a.history {
		active := tickActive[gid]
		grew := tickGrew[gid]
		if active {
			h.activeS += dt
		} else if grew {
			h.quietS += dt
		}
		if n := tickProcs[gid]; n > h.peakProcs {
			h.peakProcs = n
		}
	}
	a.retireGoneGroups(tickProcs, snap.At)
	// Retire dead series. These maps are keyed by (pid, start time), so they
	// grow with every process the machine ever starts: builds, shells, helper
	// XPC services. Without this the daemon leaked a few hundred bytes per
	// process for its entire lifetime — the exact failure mode this product
	// exists to report, in itself. A dead series is kept for one grace tick so
	// a momentary visibility gap is not read as a process exit.
	a.sweepDeadSeries()
	// System reconciliation: headroom lost across the *window*, not across the
	// last tick. The old code replaced this with the newest tick's delta, so
	// the "observed" figure compared 15 seconds of system movement against
	// 300 seconds of accumulated process growth — the ratio was meaningless
	// and usually read as 0.
	//
	// Measured from the window's opening sample, not by summing per-tick
	// drops: summing would count a sawtooth (down 100, up 100, down 100) as
	// 200 MB of growth when the window actually ended where it started.
	if a.windowSysStart == nil {
		a.windowSysStart = &sys
	}
	start := a.windowSysStart
	if dh := int64(start.FreePages*start.PageBytes) - int64(sys.FreePages*sys.PageBytes); dh > 0 {
		a.observedGrowth = dh
	} else {
		a.observedGrowth = 0
	}
	a.lastSys = &sys
	a.lastCoverage = snap.Coverage
	a.lastQuality = snap.Quality
}

// Window closes the current attribution window (called at window boundaries).
func (a *Attributor) Window(sysNow model.SystemSample) Window {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.windowLocked(sysNow)
}

func (a *Attributor) windowLocked(sysNow model.SystemSample) Window {
	w := Window{
		At: sysNow.At, WindowS: a.windowS,
		ObservedGrowth:  a.observedGrowth,
		CompressorBytes: int64(sysNow.CompressorPages) * int64(sysNow.PageBytes),
		Coverage:        a.lastCoverage,
		Quality:         a.lastQuality,
	}
	byGroup := map[string]*Group{}
	for _, s := range a.series {
		if !s.alive {
			continue
		}
		// A group that grew and then stopped still has a verdict from when it
		// was climbing, and the only way to correct it is to keep it visible
		// to the classifier. It must not join the growth ranking (that would
		// tell the user to worry about a process that is stable), so flat
		// groups are collected separately and judged, not ranked.
		if s.delta == 0 && s.release == 0 {
			addFlat(&w, a, s.id)
			continue
		}
		gid := a.groupFor(s.id)
		g := byGroup[gid]
		if g == nil {
			g = &Group{AnchorKey: gid, Label: a.groupLabel(gid)}
			byGroup[gid] = g
		}
		g.GrowthBytes += s.delta
		g.ReleaseBytes += s.release
		g.Processes++
		if rate := float64(s.delta) / float64(a.windowS); rate > g.PeakDeltaRate {
			g.PeakDeltaRate = rate
		}
		if s.id.Pid != 0 && s.delta > g.peakDelta {
			g.peakDelta = s.delta
			g.LargestPid = s.id.Pid
			g.TopExe = s.id.Exe
		}
	}
	for _, g := range byGroup {
		w.Groups = append(w.Groups, *g)
		w.AccountedGrowth += g.GrowthBytes
	}
	sort.Slice(w.Groups, func(i, j int) bool {
		if w.Groups[i].GrowthBytes != w.Groups[j].GrowthBytes {
			return w.Groups[i].GrowthBytes > w.Groups[j].GrowthBytes
		}
		return w.Groups[i].PeakDeltaRate > w.Groups[j].PeakDeltaRate // burst outranks drift
	})
	if w.ObservedGrowth > 0 {
		w.CoverageRatio = float64(w.AccountedGrowth) / float64(w.ObservedGrowth)
	}
	// large-but-stable: resident high, zero growth this window
	for _, s := range a.series {
		if s.alive && s.delta == 0 && s.prevBytes > 500<<20 {
			w.LargeButStable = append(w.LargeButStable, Group{
				AnchorKey: a.groupFor(s.id), Label: s.id.Exe, Processes: 1,
			})
		}
	}
	w.Note = honestNote(w)
	return w
}

// addFlat records a group that did not move this window, for re-judging.
func addFlat(w *Window, a *Attributor, id Identity) {
	gid := a.groupFor(id)
	for i := range w.Flat {
		if w.Flat[i].AnchorKey == gid {
			w.Flat[i].Processes++
			return
		}
	}
	w.Flat = append(w.Flat, Group{AnchorKey: gid, Label: a.groupLabel(gid), Processes: 1})
}

func honestNote(w Window) string {
	return fmt.Sprintf("%d groups grew by a total of %d MB; system headroom fell %d MB. Shared pages are counted per-process, so process sums can exceed the system delta. Compressed pages (%d MB in compressor) belong to no process.",
		len(w.Groups), w.AccountedGrowth>>20, w.ObservedGrowth>>20, w.CompressorBytes>>20)
}

// Reset starts a fresh window: accumulated deltas and the sticky membership
// map are dropped, process series survive so the next delta is still computed
// against the last observed size (otherwise every window boundary would look
// like every process was born, erasing its growth). A window that never
// resets accumulates one day of drift and reports it as "the last 5 minutes".
func (a *Attributor) Reset() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.resetLocked()
}

func (a *Attributor) resetLocked() {
	for _, s := range a.series {
		s.delta, s.release = 0, 0
		s.cpuDelta = 0
		s.samples, s.activeTicks, s.growTicks, s.bothTicks = 0, 0, 0, 0
	}
	a.groups = map[string]string{}
	a.observedGrowth = 0
	a.windowSysStart = nil
}

// ProcessCount reports how many process series are alive, so a caller can
// distinguish "no data yet" from "a window where nothing grew".
func (a *Attributor) ProcessCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := 0
	for _, s := range a.series {
		if s.alive {
			n++
		}
	}
	return n
}

// SeriesFor returns per-process growth stats for one group anchor, in the
// shape the classifier consumes (MP-ALG-005). Activity comes from CPU-time
// deltas: a group that grows while its processes burn CPU is doing work; one
// that grows while they sit idle is the only state that can support an
// "unexplained" reading. Without this signal every growing group would be
// classified identically, which is what made reports useless.
func (a *Attributor) SeriesFor(anchor string) SeriesStats {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.seriesForLocked(anchor)
}

func (a *Attributor) seriesForLocked(anchor string) SeriesStats {
	var totalGrowth, totalRelease int64
	var procs int64
	// Membership is decided by the anchor, not by whether this particular
	// window happens to show growth: after a Reset a steadily-growing group
	// has delta==0 for one tick, and dropping it there would report "no
	// growth" for a process that has been climbing for 20 minutes.
	for _, s := range a.series {
		if !s.alive || a.groupFor(s.id) != anchor {
			continue
		}
		totalGrowth += s.delta
		totalRelease += s.release
		procs++
	}
	st := SeriesStats{GroupKey: anchor, DurationS: a.windowS,
		GrowthBytes: totalGrowth, ReleaseBytes: totalRelease,
		ProcessCount: int(procs)}
	if a.windowS > 0 {
		st.SlopeBytesS = float64(totalGrowth) / float64(a.windowS)
	}
	st.CoverageRatio = a.lastCoverage.VisibleRatio()
	// Long-horizon evidence: the classifier needs a 20-minute sustained-growth
	// window, which a 5-minute report window can never supply on its own.
	if h := a.history[anchor]; h != nil {
		span := a.now.Sub(h.firstSeen).Seconds()
		st.DurationS = int64(span)
		st.ActivityS = h.activeS
		st.QuietS = h.quietS
		// span must be a real observation interval. A group first seen on the
		// tick being read has a span of microseconds, and dividing by that
		// reported 5.9e13 B/s — a rate no process can reach, from two normal
		// samples. Comparing against the sample interval is not enough: two
		// snapshots taken microseconds apart have a sampleDT just as small.
		// Below one second the window's own slope is the honest answer.
		if span >= 1 {
			st.SlopeBytesS = float64(h.growthBytes) / span
			st.ReleaseBytes = h.releaseBytes
		}
		if h.lastGrowth.After(h.lastActivity) && !h.lastActivity.IsZero() {
			st.QuietS = int64(a.now.Sub(h.lastActivity).Seconds())
		} else if h.lastActivity.IsZero() {
			// Never observed working: quiet for as long as we have watched,
			// not for the zero-value time (which would read as ~292 years).
			st.QuietS = int64(a.now.Sub(h.firstSeen).Seconds())
		}
		if h.activeS+h.quietS > 0 {
			st.ActivityCorr = float64(h.activeS) / float64(h.activeS+h.quietS)
		}
		// Plateau: how long since this group last grew. The classifier's
		// cache branch keys on it, and it was never computed before, so that
		// branch was unreachable and every cache read as a leak.
		if !h.lastGrowth.IsZero() {
			st.PlateauS = int64(a.now.Sub(h.lastGrowth).Seconds())
		}
		st.PeakProcessCount = h.peakProcs
	}
	if st.PeakProcessCount < st.ProcessCount {
		st.PeakProcessCount = st.ProcessCount
	}
	return st
}

// sampleIntervalS is the observed spacing between process snapshots.
func (a *Attributor) sampleIntervalS() float64 {
	if a.sampleDT <= 0 {
		return 0
	}
	return a.sampleDT
}

const activityCPUS = 0.02 // 20 ms of CPU between samples counts as active

// SeriesStats is the classifier's input shape. It is restated here so
// attribution can produce it without importing the classifier; the pipeline
// converts between the two (analysis layers stay peers, never cyclic).
type SeriesStats struct {
	GroupKey      string
	SlopeBytesS   float64
	Accel         float64
	PlateauS      int64
	ActivityCorr  float64
	ActivityS     int64
	QuietS        int64
	CacheRole     bool
	CeilingBytes  float64
	DurationS     int64
	CoverageRatio float64

	GrowthBytes      int64
	ReleaseBytes     int64
	ProcessCount     int
	PeakProcessCount int
}

// groupFor: nearest-anchor with sticky membership (§4).
func (a *Attributor) groupFor(id Identity) string {
	if g, ok := a.groups[id.Key()]; ok {
		return g // sticky within window
	}
	anchor := a.nearestAnchor(id)
	a.groups[id.Key()] = anchor
	return anchor
}

// nearestAnchor walks the parent chain to the nearest group anchor (§4.1).
//
// Only *ancestors* can be the anchor, never the process itself: "node" and
// "zsh" are in the anchor table, so treating self-matches as anchors would
// merge ten node processes from five unrelated sessions into one blob — the
// exact failure §4.3 calls out. A process with no anchor ancestor is its own
// group (a session root that launchd started directly).
func (a *Attributor) nearestAnchor(id Identity) string {
	seen := map[int32]bool{}
	cur := id
	for depth := 0; depth < maxAnchorDepth; depth++ {
		if cur.Pid <= 1 || seen[cur.Pid] {
			break
		}
		seen[cur.Pid] = true
		ppid, ok := a.parents[cur.Pid]
		if !ok || ppid <= 1 || ppid == cur.Pid {
			break
		}
		key := identityKeyOf(a, ppid)
		if key == "" {
			break
		}
		parent := a.series[key].id
		if isAnchorName(parent.Exe) {
			return parent.Exe
		}
		cur = parent
	}
	if id.Exe != "" {
		return id.Exe
	}
	return fmt.Sprintf("pid-%d", id.Pid)
}

const maxAnchorDepth = 24

// identityKeyOf returns the identity key currently registered for a pid.
func identityKeyOf(a *Attributor, pid int32) string {
	for k, s := range a.series {
		if s.id.Pid == pid && s.alive {
			return k
		}
	}
	return ""
}

func isAnchorName(name string) bool {
	for _, a := range AnchorTable {
		if name == a {
			return true
		}
	}
	return false
}

func (a *Attributor) groupLabel(gid string) string { return gid }

// AnchorTable — names that define a group boundary (§4.1 b).
//
// Only workloads, never shells or interpreters. "zsh", "node" and "python3"
// were here originally, which made every process its own anchor (blob-merging
// unrelated sessions) and produced group labels like "zsh" that tell the user
// nothing. A shell is a plumbing stage between a terminal and the workload;
// the workload is what the report should name. Names are compared against
// pbi_comm, which the kernel truncates to MAXCOMLEN (16) bytes.
var AnchorTable = []string{
	// terminals
	"iTerm2", "Terminal", "wezterm-gui", "Alacritty", "kitty", "tmux", "sshd",
	// editors / IDEs
	"Code", "Code Helper", "Cursor", "Cursor Helper",
	// apps that carry long-lived sessions
	"Google Chrome", "Safari", "Docker",
	// coding agents and this daemon
	"claude", "mempilotd",
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// maxRetiredSeries bounds the dead-series bookkeeping itself: a machine that
// forks thousands of short-lived processes must not grow this map either.
const maxRetiredSeries = 4096

// sweepDeadSeries removes series for processes that have exited, after one
// grace tick. Their accumulated growth was already reported in the window that
// closed, and the group history keeps the long-horizon view, so nothing the
// user can see is lost by dropping them.
func (a *Attributor) sweepDeadSeries() {
	for key, s := range a.series {
		if s.alive {
			s.deadTicks = 0
			continue
		}
		s.deadTicks++
		if s.deadTicks < 2 {
			continue
		}
		delete(a.series, key)
		delete(a.groups, key)
	}
	// Hard cap: if a burst of short-lived processes still outruns the sweep,
	// drop the oldest dead entries rather than grow without bound.
	if len(a.series) > maxRetiredSeries*4 {
		type deadSeries struct {
			key string
			at  int
		}
		var deads []deadSeries
		for key, s := range a.series {
			if !s.alive {
				deads = append(deads, deadSeries{key, s.deadTicks})
			}
		}
		sort.Slice(deads, func(i, j int) bool { return deads[i].at > deads[j].at })
		for _, d := range deads[:len(deads)/2] {
			delete(a.series, d.key)
			delete(a.groups, d.key)
		}
	}
	// Group history: keep the most recently active groups only. History is
	// what the classifier reads; a group nobody has seen in an hour is no
	// longer evidence about the present.
	if len(a.history) > maxRetiredSeries {
		type gh struct {
			gid string
			at  time.Time
		}
		all := make([]gh, 0, len(a.history))
		for gid, h := range a.history {
			all = append(all, gh{gid, h.firstSeen})
		}
		sort.Slice(all, func(i, j int) bool { return all[i].at.After(all[j].at) })
		for _, e := range all[maxRetiredSeries:] {
			delete(a.history, e.gid)
		}
	}
}

// goneGraceS is how long a group's history survives after its last live
// member was seen. It must exceed a plausible sampler stall so a momentary
// visibility gap does not erase real evidence, and it must be short enough that
// a new group never inherits an old one's window.
const goneGraceS = 120

// retireGoneGroups drops the long-horizon record of a group with no live
// members. History is keyed by anchor *name*, which is not unique in time:
// probes, browsers and shells all reuse names across sessions. Observed live
// in run15 — a group that started at 11:40 reported DurationS=884s at 11:45
// because a run12 group of the same name had been recorded an hour earlier and
// nothing retired it. DurationS gates the leak branch, so an inherited window
// lets a brand-new process be called a leak on its first appearance: exactly
// the warning the owner asked never to see again.
func (a *Attributor) retireGoneGroups(alive map[string]int, now time.Time) {
	if len(a.history) == 0 {
		return
	}
	for gid, h := range a.history {
		if alive[gid] > 0 {
			h.lastSeen = now
			continue
		}
		ref := h.lastSeen
		if ref.IsZero() {
			ref = h.firstSeen
		}
		if now.Sub(ref) >= goneGraceS*time.Second {
			delete(a.history, gid)
		}
	}
}

// footprintAt is the memory this process actually holds.
//
// It must be phys_footprint, not resident_size. Measured on the target machine
// (evidence/2026-10-25-metric-vanishes): a process holding a fixed 200 MB and
// doing nothing moved 217 MB by resident_size across 13 samples and 10 MB by
// phys_footprint; a process that pushed the system into pressure fell from
// 71 MB to 8 MB resident while its footprint stayed at 428 MB. Resident pages
// are reclaimed (swapped, compressed) precisely when the machine is under
// pressure — the only condition this product exists for — so measuring them
// makes a real leak read as a shrinking process.
//
// Fallback: a snapshot from a source that only reports resident size (tests,
// replay corpora) still works, because there footprint == 0 is not a real
// reading.
func footprintAt(p model.ProcessSample) uint64 {
	if p.FootprintBytes > 0 {
		return p.FootprintBytes
	}
	return p.ResidentBytes
}

// CloseWindowAndReset reads the window and clears its accumulation in one
// critical section. Calling Window() and then Reset() separately lets the
// process sampler append between them, so deltas that arrived in between are
// silently discarded — and it is two lock acquisitions for one logical step.
func (a *Attributor) CloseWindowAndReset(sysNow model.SystemSample) Window {
	a.mu.Lock()
	defer a.mu.Unlock()
	w := a.windowLocked(sysNow)
	a.resetLocked()
	return w
}
