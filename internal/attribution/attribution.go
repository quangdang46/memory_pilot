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

// Time is when the process started.
func (id Identity) Time() time.Time {
	return time.Unix(id.StartSec, id.StartUsec*1000)
}

func (id Identity) Key() string {
	return fmt.Sprintf("%d:%d.%d:%s", id.Pid, id.StartSec, id.StartUsec, id.Exe)
}

type series struct {
	id   Identity
	ppid int32
	// hadParent records that this process was once observed under a parent
	// other than launchd. macOS reparents an orphan to pid 1, so "ppid was >1
	// and is now 1" is the only direct evidence that its parent exited. Without
	// this, "no parent series" is ambiguous: the parent may simply be outside
	// the 75% coverage this machine gives.
	hadParent bool
	// cpuUnknown records that at least one sample of this process had no CPU
	// accounting. Its quiet period is then unknown, not long — the difference
	// between "nothing is using this" and "we could not see whether anything
	// was using this", which L1 must never conflate.
	cpuUnknown bool
	prevBytes  int64 // phys_footprint, not resident: see footprintAt
	prevCPU    float64
	cpuDelta   float64 // accumulated CPU seconds in the window
	// lastCPU is when this process last burned CPU. The relief engine needs it
	// per process: a group can be busy while one member has been idle for days,
	// and that member is the releasable one (measured: three rust-analyzer
	// processes, 2.7 GB, zero CPU for 48 hours, inside a working claude group).
	lastCPU   time.Time
	alive     bool
	delta     int64 // accumulated positive growth in window
	release   int64 // accumulated frees, tracked separately
	deadTicks int   // consecutive samples the process was absent
	// group is the anchor this process belonged to, remembered so a process
	// that exits can still be charged to the right group: by the time it is
	// swept it is absent from the snapshot, so walking the parent chain can no
	// longer find its ancestry.
	group string

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
	// seenIdentities records every identity already counted as an arrival.
	// Series are swept after two dead ticks, so a process hidden by a coverage
	// gap (75% visibility on this machine) returns with no series while
	// start > firstSnapAt is still true, and would be counted afresh each time
	// it flickers. Measured: one steady 300 MB process flickering produced
	// -1500 MB of net; node swung +2878 MB while holding 3.2 GB steady.
	seenIdentities map[string]bool
	// lastBytes remembers the footprint an identity held when its series was
	// swept. A process hidden by a coverage gap is not gone; if it reappears,
	// the release charged at sweep time must be given back, or every gap
	// shrinks the group's net by the process's whole size. Measured: a steady
	// 300 MB process flickering in and out read -1500 MB of net growth.
	lastBytes    map[string]int64
	lastCoverage model.Coverage
	lastQuality  model.Quality

	// History is the long-horizon record the classifier needs. A 5-minute
	// report window can never satisfy a 20-minute "sustained growth"
	// requirement, so a 300 s window always classified as "too short" and
	// the daemon was honest but useless. History accumulates across windows
	// and resets only when the group itself resets (see Reset).
	history map[string]*history
	// windowStats holds each group's series stats as of the moment the window
	// closed. The pipeline must classify the window that just ended, but
	// closing it resets every counter — so the numbers have to be captured
	// first.
	windowStats map[string]SeriesStats
	now         time.Time
}

// history spans multiple report windows. Buckets keep the window boundary
// from erasing evidence of what happened before it.
// trendPoint is one sample of a group's total footprint.
type trendPoint struct {
	at    time.Time
	bytes int64
}

// trendWindowS is how much history the slope is fitted over. Long enough that
// per-tick noise averages out, short enough to react within a report window.
const trendWindowS = 1200

type history struct {
	firstSeen    time.Time
	lastSeen     time.Time // last sample where the group had a live member
	lastGrowth   time.Time // last sample where the group grew
	lastActivity time.Time // last sample where any member burned CPU
	growthBytes  int64     // total positive growth since firstSeen (gross)
	releaseBytes int64     // total frees since firstSeen (gross)
	// Trend samples of the group's total footprint. The group's size is
	// measurable each tick; its *growth* cannot be accumulated from per-member
	// deltas at 75% coverage without drifting, and five attempts to do so were
	// each wrong in a different direction (73 GB, 16250 MB, 2846 MB, 507 MB,
	// and a baseline that went negative). A slope over a short window is a
	// direct measurement of the thing the question asks about: is this group
	// still getting bigger?
	trend        []trendPoint
	trendStop    time.Time // when the current uninterrupted climb began
	slopeBps     float64   // bytes per second, least squares over the window
	climbing     bool      // true when the trend is positive and consistent
	peakProcs    int       // high-water process count
	activeS      int64
	quietS       int64
	peakGrowthMB int64
}

func New(windowS int64) *Attributor {
	return &Attributor{series: map[string]*series{},
		seenIdentities: map[string]bool{}, lastBytes: map[string]int64{},
		groups:  map[string]string{},
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
			// Count an arrival once per identity, ever — not once per series.
			if !a.seenIdentities[key] &&
				(a.firstSnapAt.IsZero() || time.Unix(id.StartSec, id.StartUsec*1000).After(a.firstSnapAt)) {
				d = now
			}
			// If this identity was swept while only hidden by a coverage gap,
			// undo the release that sweep charged. A process that really exited
			// and a new one with the same identity cannot be distinguished, but
			// identity includes start time, so a reappearance is the same
			// process — the sweep was wrong.
			if back, ok := a.lastBytes[key]; ok && back > 0 {
				if h := a.history[a.groupFor(id)]; h != nil {
					h.releaseBytes -= back
				}
				delete(a.lastBytes, key)
			}
			s = &series{id: id, ppid: p.Identity.Ppid, alive: true}
			a.series[key] = s
			// A pre-existing member is part of the group's starting size from
			// the moment it is first seen. Adding it here — once per identity —
			// keeps the baseline independent of which sample caught which
			// member, which a snapshot of the first total could not do: node's
			// first window saw a fraction of its processes and every later one
			// looked like +293 MB of growth.
		}
		s.prevBytes = now
		s.alive = true
		s.ppid = p.Identity.Ppid // reparenting is recorded, membership is sticky (§4.2)
		if s.ppid > 1 {
			s.hadParent = true
		}
		if d > 0 {
			s.delta += d
		} else {
			s.release += -d
		}

		// Activity sample: CPU-time delta is the only "is it working?" signal
		// available without pwrites into another process's memory.
		cpu := p.CpuUserS + p.CpuSystemS
		if p.CpuUnavailable {
			// Zero CPU here means "not measured". Treating it as silence is how
			// an unreadable process becomes a releasable one.
			s.cpuUnknown = true
		}
		cpuD := cpu - s.prevCPU
		s.prevCPU = cpu
		s.cpuDelta += cpuD
		// Two thresholds, because they answer different questions.
		//
		// activityCPUS is the classifier's: did this process work at all in
		// this window? Measured over 60 s on this machine, a Rust LSP and a
		// Chrome renderer use 8.9 s and 5.3 s — real work, either way.
		//
		// lastCPU drives "quiet for N minutes", which L1 turns into "nobody is
		// using this, closing it loses nothing". That claim needs the process to
		// be genuinely asleep, not merely slow: with the same 20 ms threshold a
		// process polling every second and using 2 s per minute reads as "no CPU
		// for 40 minutes". quietCPUS is that bar.
		if cpuD > quietCPUS || (cpuD > activityCPUS && s.lastCPU.IsZero()) {
			s.lastCPU = snap.At
		} else if s.lastCPU.IsZero() {
			// First sample: we have no evidence it ever worked, so the quiet
			// period starts now rather than at the zero time.
			s.lastCPU = snap.At
		}
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
		s.group = gid
		if a.history[gid] == nil {
			a.history[gid] = &history{firstSeen: snap.At}
		}
		a.history[gid].lastSeen = snap.At
		// Only material growth resets the plateau clock. A few kilobytes of
		// jitter is a page fault, not a group still climbing: measured, 1 KB
		// every third tick reset PlateauS to 30 s and made a group flat for
		// 25 minutes read as "sustained growth with no plateau" — the exact
		// wording that classified probe-plateau, probe-sawtooth and
		// probe-children as leaks in run18.
		if d >= growthNoiseBytes {
			a.history[gid].lastGrowth = snap.At
		}
		if active {
			if !tickActive[gid] {
				tickActive[gid] = true
				a.history[gid].lastActivity = snap.At
			}
		}
		// growthBytes and releaseBytes are GROSS, and that is deliberate: a
		// sawtooth must show "allocated 400 MB, freed 400 MB" so the cycling
		// branch can see it, and net growth would be zero.
		//
		// But gross growth cannot be used as "how much this group grew" for a
		// large group: 46 node processes each moving a few MB per sample added
		// up to 11 GB after an hour and 73 GB after four, on a 16 GB machine
		// whose node processes held 3.2 GB in total. netGrowthBytes is the
		// cumulative sum of signed changes, which is what the size of a group
		// actually is over time.
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
	// Process count and total footprint per group. The byte total includes
	// every member we still have a series for, whether or not it was visible in
	// this particular sample: at 75% coverage, a total that only counts the
	// visible members jumps by gigabytes between ticks and turns a flat group
	// into -1077 MB of "growth".
	tickProcs := map[string]int{}
	tickBytes := map[string]int64{}
	for _, s := range a.series {
		if s.alive {
			gid := a.groupFor(s.id)
			tickProcs[gid]++
		}
	}
	for _, s := range a.series {
		// deadTicks < 2 means "not yet swept": the member is still part of the
		// group even when this sample missed it. Using s.alive here made the
		// total fall by the size of every hidden member and rise again when it
		// reappeared — at 75% coverage that read as -1077 MB on a flat group.
		if s.deadTicks < 2 && s.prevBytes > 0 {
			tickBytes[a.groupFor(s.id)] += s.prevBytes
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
		// peakProcs is a high-water mark over the recent past, not a permanent
		// record. A group that spawned 38 processes once during a build and
		// holds 1 now is not accumulating; with a lifetime maximum it would
		// qualify as accumulation for the rest of the daemon's life. Measured:
		// bash, 1 process on the machine, reported "process count reached 38"
		// every window after a single busy moment.
		//
		// It decays one process per quiet tick instead of dropping outright, so
		// a genuine accumulation — which keeps its children — stays at its
		// high-water mark while a transient spike fades.
		if n := tickProcs[gid]; n > h.peakProcs {
			h.peakProcs = n
		} else if h.peakProcs > n {
			h.peakProcs--
		}
		// Net growth is the group's change in total size, recomputed each tick
		// rather than accumulated from per-member deltas.
		//
		// Summing deltas is only equal to the total's change when every member
		// is visible in every sample. At 75% coverage it is not: thirty-five
		// node processes wobbling a few MB each summed to 507 MB of "growth"
		// over thirty minutes in which the whole group moved 9 MB — a 56x
		// overstatement, and an advisory repeating every window about a group
		// that was flat. Recomputing from the total is immune to that.
		if tot, ok := tickBytes[gid]; ok && tot > 0 {
			h.addTrend(snap.At, tot)
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
		//
		// The decision is made per GROUP, once every member has been seen, not
		// here per process: splitting a group by which of its processes moved
		// this window handed the classifier a process count from one half and
		// growth from the other. Measured in run20 on probe-children — 21 real
		// processes, reported as 15, then 21, then 1 while all 21 were running.
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
		// Now that every member is counted, decide where the group belongs.
		// A group that moved nothing this window is judged (Flat) but not
		// ranked; one that moved is ranked and judged.
		if g.GrowthBytes == 0 && g.ReleaseBytes == 0 {
			addFlat(&w, *g)
			continue
		}
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

// addFlat records a whole group that did not move this window, for re-judging.
// It takes the finished Group so the process count is the group's real one,
// not the number of members that happened to be flat.
func addFlat(w *Window, g Group) {
	for i := range w.Flat {
		if w.Flat[i].AnchorKey == g.AnchorKey {
			w.Flat[i].Processes += g.Processes
			return
		}
	}
	w.Flat = append(w.Flat, g)
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
		// LongGrowthBytes is the same long horizon as ReleaseBytes. They must
		// be compared with each other: ReleaseBytes accumulates across windows
		// while GrowthBytes covers only the last one, so "released 130 MB while
		// growing 0 MB" compared 25 minutes of frees against 5 minutes of
		// allocation and called a group that had accumulated 288 MB a cycling
		// cache (run20, probe-children).
		st.LongGrowthBytes = h.growthBytes
		st.NetGrowthBytes = h.trendGrowthBytes()
		st.SlopeBytesS = h.slopeBps
		st.Climbing = h.climbing
		st.LongPeakProcesses = h.peakProcs
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
		//
		// A group that has never grown while we watched has been flat for as
		// long as it has existed — not for zero seconds. Leaving PlateauS at 0
		// made the plateau branch unreachable for exactly those groups, and
		// they then satisfied the leak branch instead (it only needs quiet time
		// and duration), so "never grew" read as "suspicious growth". This is
		// the same mistake as the zero-value QuietS that once read as 292 years,
		// with the opposite sign.
		if !h.lastGrowth.IsZero() {
			st.PlateauS = int64(a.now.Sub(h.lastGrowth).Seconds())
		} else {
			st.PlateauS = int64(a.now.Sub(h.firstSeen).Seconds())
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

// growthNoiseBytes is the least increase that counts as "still growing" for
// the plateau clock. Below it, page-level jitter would keep resetting a group
// that is, for every practical purpose, flat.
const growthNoiseBytes = 1 << 20

const activityCPUS = 0.02 // 20 ms of CPU between samples counts as active

// quietCPUS is what counts as "still using it" for the quiet clock that L1
// reads. 0.5 s of CPU between 15 s snapshots is ~3% of one core: below that a
// process is not doing work, above it, it is. Measured on this machine over
// 60 s: every large process used 0.4 s to 8.9 s, and none of them was idle.
const quietCPUS = 0.5

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

	GrowthBytes  int64
	ReleaseBytes int64
	// LongGrowthBytes and LongPeakProcesses cover the same horizon as
	// ReleaseBytes: the whole time the group has been watched. Comparing a
	// window's growth against a lifetime's release is not a comparison.
	LongGrowthBytes   int64
	LongPeakProcesses int
	// NetGrowthBytes is what the group is on pace to add across the trend
	// window, from the fitted slope — not an accumulated sum, which drifts at
	// partial coverage.
	NetGrowthBytes   int64
	Climbing         bool
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

// TerminalAnchors are the anchors whose children are whatever the user chose to
// run, not what the application spawned for itself. A terminal with 86
// descendants and 8 GB under it is a normal working machine, not accumulation:
// measured here, wezterm-gui held exactly that, and the subprocess advisory it
// produced named the user's own session back at them.
//
// These groups are still counted, still ranked and still appear in `report`.
// They simply never become advisories, because the advisory would be telling
// the user about the work they started.
var TerminalAnchors = map[string]bool{
	"Terminal": true, "iTerm2": true, "wezterm-gui": true, "Alacritty": true,
	"kitty": true, "tmux": true, "sshd": true,
}

// IsTerminalGroup reports whether a group anchor is a terminal or shell
// session rather than an application.
func IsTerminalGroup(anchor string) bool { return TerminalAnchors[anchor] }

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
		// A process that exits gives its memory back to the system, and that is
		// release — the mirror of the rule that a process arriving is growth
		// (see Observe). Without it the accounting only ever runs one way:
		// a terminal that spawns a compiler per build would accumulate its
		// peak footprint forever while holding nothing. Measured on this
		// machine: wezterm-gui read 632-787 MB of "growth" per 5-minute
		// window purely from short-lived children coming and going.
		if s.prevBytes > 0 {
			a.lastBytes[key] = s.prevBytes

			if h := a.history[s.group]; h != nil {
				h.releaseBytes += s.prevBytes
				// ...and the same amount leaves the group's net size. Adding an
				// arrival to net without subtracting the departure is the same
				// one-way accounting as defect 13, one layer up: node spawns a
				// ~220 MB helper per session and reaps it, and net grew by
				// 2846 MB in 285 seconds while the group itself held a steady
				// 4.3 GB. Release alone was not enough — net is a different
				// accumulator and needed the mirror entry too.
			}
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

// addTrend records the group's total and refits the slope.
//
// This replaces accumulated "growth" entirely. At 75% coverage, summing
// per-member deltas drifts no matter how the bookkeeping is arranged: five
// separate attempts produced 73 GB, 16250 MB, 2846 MB, 507 MB and a baseline
// that went negative, all for a group holding a steady 3.2 GB. A least-squares
// slope over the totals themselves cannot drift — it is a direct measurement
// of whether the group is still getting bigger.
func (h *history) addTrend(at time.Time, total int64) {
	h.trend = append(h.trend, trendPoint{at: at, bytes: total})
	cutoff := at.Add(-trendWindowS * time.Second)
	i := 0
	for i < len(h.trend) && h.trend[i].at.Before(cutoff) {
		i++
	}
	if i > 0 {
		h.trend = append(h.trend[:0], h.trend[i:]...)
	}
	h.slopeBps, h.climbing = fitSlope(h.trend)
}

// trendGrowthBytes is what the group is on pace to add across the trend
// window, which is the quantity the leak branch's threshold is expressed in.
func (h *history) trendGrowthBytes() int64 {
	if h.slopeBps <= 0 {
		return 0
	}
	return int64(h.slopeBps * trendWindowS)
}

// fitSlope returns the least-squares slope in bytes/second, and whether the
// climb is consistent enough to call a trend: the fit must explain most of the
// movement, and the endpoints must agree with it. A flat group with noise
// produces a near-zero slope and climbing=false.
func fitSlope(pts []trendPoint) (float64, bool) {
	if len(pts) < 8 {
		return 0, false
	}
	t0 := pts[0].at
	var sx, sy, sxx, sxy float64
	n := float64(len(pts))
	for _, p := range pts {
		x := p.at.Sub(t0).Seconds()
		y := float64(p.bytes)
		sx += x
		sy += y
		sxx += x * x
		sxy += x * y
	}
	den := n*sxx - sx*sx
	if den == 0 {
		return 0, false
	}
	slope := (n*sxy - sx*sy) / den
	inter := (sy - slope*sx) / n
	// R^2: how much of the variance the line explains.
	var ssTot, ssRes float64
	mean := sy / n
	for _, p := range pts {
		x := p.at.Sub(t0).Seconds()
		pred := slope*x + inter
		ssRes += (float64(p.bytes) - pred) * (float64(p.bytes) - pred)
		ssTot += (float64(p.bytes) - mean) * (float64(p.bytes) - mean)
	}
	r2 := 0.0
	if ssTot > 0 {
		r2 = 1 - ssRes/ssTot
	}
	// A trend needs a real climb and a fit that explains it. Half a megabyte
	// per second over the window is 600 MB — material; below that the group is
	// not what the user needs to hear about.
	const minSlopeBps = 200 << 10
	const minR2 = 0.5
	return slope, slope >= minSlopeBps && r2 >= minR2
}

// ensureHistory returns the group's long-horizon record, creating it if needed.
func (a *Attributor) ensureHistory(at time.Time, gid string) *history {
	h := a.history[gid]
	if h == nil {
		h = &history{firstSeen: at}
		a.history[gid] = h
	}
	return h
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
	// Snapshot the per-group series stats for the window that just closed,
	// BEFORE resetting. The pipeline classifies the window it closed; reading
	// SeriesFor afterwards returns the next window's counters, which are all
	// zero — so the classifier saw GrowthBytes=0 and SlopeBytesS=0 while the
	// window-recorded growth said 98 MB, and the plateau/accumulation branches
	// were unreachable for any group whose growth happened in a different
	// window than the read.
	a.windowStats = map[string]SeriesStats{}
	for gid := range a.history {
		a.windowStats[gid] = a.seriesForLocked(gid)
	}
	a.resetLocked()
	return w
}

// WindowStats returns the per-group stats captured when the window closed.
func (a *Attributor) WindowStats(anchor string) (SeriesStats, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	st, ok := a.windowStats[anchor]
	return st, ok
}

// MemberFact is one live process, for the relief engine's per-process
// assessment. Attribution knows each member's footprint and when it last
// burned CPU; the relief engine decides what that means.
type MemberFact struct {
	Pid         int32
	Exe         string
	Group       string
	Footprint   int64
	QuietFor    time.Duration
	ParentAlive bool
	ParentGone  bool
}

// quietWitnessFrom returns the earliest moment attribution can honestly claim
// to have watched this process without CPU use.
func (a *Attributor) quietWitnessFrom(s *series) time.Time {
	// A process that arrived while we were watching: the clock starts at its
	// birth, which we observed.
	arrival := s.id.Time()
	if !arrival.IsZero() && !a.firstSnapAt.IsZero() && arrival.After(a.firstSnapAt) {
		return arrival
	}
	// A process that was already running when the daemon started: the clock
	// starts when we started watching.
	if !a.firstSnapAt.IsZero() {
		return a.firstSnapAt
	}
	return time.Time{}
}

// Members returns every live process with the facts the relief engine needs.
//
// The group a process belongs to is not a filter here: an abandoned process
// inside a protected group (an agent's idle rust-analyzer) is exactly what the
// engine must be able to see.
func (a *Attributor) Members() []MemberFact {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]MemberFact, 0, len(a.series))
	for _, s := range a.series {
		if !s.alive || s.prevBytes <= 0 {
			continue
		}
		// QuietFor is silence we witnessed, never silence we assume. The
		// fallback used to be the process's own start time, which for a
		// long-running process is days — so a daemon restart made every
		// candidate read "no CPU for 5m30s", the exact age of the daemon.
		// Measured in the first post-restart L1 run: 33 candidates, all of
		// them with identical quiet time, none of them actually observed.
		//
		// The witness starts when we first saw the process in this situation:
		// since it arrived, or since the oldest snapshot we hold.
		quiet := time.Duration(0)
		if witness := a.quietWitnessFrom(s); !witness.IsZero() {
			if since := a.now.Sub(witness); since > 0 {
				quiet = since
			}
		}
		if !s.lastCPU.IsZero() {
			if silent := a.now.Sub(s.lastCPU); silent > 0 && silent < quiet {
				quiet = silent // it did work more recently than we have watched
			}
		}
		// ParentGone means the parent we saw has exited. macOS reparents an
		// orphan to pid 1, so the evidence is "was under a parent, now under
		// launchd". Measured in the first live L1 run: 39 candidates including
		// Finder, the VM and every Chrome helper, all labelled "parent gone"
		// because their ppid is 1 — which is just how launchd starts a service.
		_, parentAlive := a.series[identityKeyOf(a, s.ppid)]
		if s.ppid <= 1 {
			parentAlive = false
		}
		parentGone := s.hadParent && s.ppid <= 1
		if s.cpuUnknown {
			// Unknown quiet period: report the process, but do not let it look
			// idle. Zero QuietFor keeps it out of every L1 branch that requires
			// elapsed silence.
			quiet = 0
		}
		out = append(out, MemberFact{
			Pid: s.id.Pid, Exe: s.id.Exe, Group: s.group,
			Footprint: s.prevBytes, QuietFor: quiet,
			ParentAlive: parentAlive, ParentGone: parentGone,
		})
	}
	return out
}
