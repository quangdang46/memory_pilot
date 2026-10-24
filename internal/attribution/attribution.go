// Package attribution: MP-ALG-004. Identity-scoped deltas, never summed RSS;
// coverage is reported, never normalized to 100%; compression is a system-
// level contributor line; "large but stable" is separate from growth ranking.
package attribution

import (
	"fmt"
	"sort"
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
	prevBytes int64
	prevCPU   float64
	cpuDelta  float64 // accumulated CPU seconds in the window
	alive     bool
	delta     int64 // accumulated positive growth in window
	release   int64 // accumulated frees, tracked separately

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
}

type Attributor struct {
	series         map[string]*series
	groups         map[string]string // identity key -> anchor group id (sticky)
	parents        map[int32]int32   // pid -> ppid from the latest snapshot
	windowS        int64
	sampleDT       float64 // seconds between the last two snapshots
	lastSys        *model.SystemSample
	windowSysStart *model.SystemSample
	lastSnapAt     time.Time
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
	lastGrowth   time.Time // last sample where the group grew
	lastActivity time.Time // last sample where any member burned CPU
	growthBytes  int64     // total positive growth since firstSeen
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
		if !ok {
			a.series[key] = &series{id: id, ppid: p.Identity.Ppid, prevBytes: int64(p.ResidentBytes), alive: true}
			continue
		}
		now := int64(p.ResidentBytes)
		d := now - s.prevBytes
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
	}
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
	w := Window{
		At: sysNow.At, WindowS: a.windowS,
		ObservedGrowth:  a.observedGrowth,
		CompressorBytes: int64(sysNow.CompressorPages) * int64(sysNow.PageBytes),
		Coverage:        a.lastCoverage,
		Quality:         a.lastQuality,
	}
	byGroup := map[string]*Group{}
	for _, s := range a.series {
		if !s.alive || s.delta == 0 {
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
	var totalGrowth int64
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
		procs++
	}
	st := SeriesStats{GroupKey: anchor, DurationS: a.windowS, ProcessCount: int(procs)}
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
		if span > 0 {
			st.SlopeBytesS = float64(h.growthBytes) / span
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
	ProcessCount  int
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
