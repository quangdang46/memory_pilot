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
	prevBytes int64
	alive     bool
	delta     int64 // accumulated positive growth in window
	release   int64 // accumulated frees, tracked separately
}

type Group struct {
	AnchorKey    string
	Label        string
	GrowthBytes  int64
	ReleaseBytes int64
	Processes    int
	PeakDeltaRate float64
	LargestPid   int32
}

type Window struct {
	At             time.Time
	WindowS        int64
	Groups         []Group // ranked by growth, never labeled "cause"
	LargeButStable []Group // high RSS, zero delta — never in growth ranking
	ObservedGrowth int64   // system headroom delta
	AccountedGrowth int64  // Σ positive per-process deltas
	CoverageRatio  float64 // may exceed 1 (shared pages) — never normalized
	CompressorBytes int64  // system-level contributor line (§3.2)
	Coverage       model.Coverage
	Quality        model.Quality
	Note           string // honest-language note attached to every report
}

type Attributor struct {
	series   map[string]*series
	groups   map[string]string // identity key -> anchor group id (sticky)
	windowS  int64
	lastSys  *model.SystemSample
	observedGrowth int64
	lastCoverage   model.Coverage
	lastQuality    model.Quality
}

func New(windowS int64) *Attributor {
	return &Attributor{series: map[string]*series{}, groups: map[string]string{}, windowS: windowS}
}

// Observe feeds one process snapshot + system sample. Deltas are computed only
// across identical identities (never across identity change — §2 rule 4).
func (a *Attributor) Observe(snap model.ProcessSnapshot, sys model.SystemSample) {
	for _, p := range snap.Processes {
		id := Identity{Pid: p.Identity.Pid,
			StartSec: p.Identity.StartTime.Unix(), StartUsec: int64(p.Identity.StartTime.Nanosecond() / 1000),
			Exe:      p.Identity.Executable}
		key := id.Key()
		s, ok := a.series[key]
		if !ok {
			a.series[key] = &series{id: id, prevBytes: int64(p.ResidentBytes), alive: true}
			continue
		}
		now := int64(p.ResidentBytes)
		d := now - s.prevBytes
		s.prevBytes = now
		s.alive = true
		if d > 0 {
			s.delta += d
		} else {
			s.release += -d
		}
	}
	// mark disappeared series ended (rule 2) — deltas stop accumulating
	for _, s := range a.series {
		s.alive = false
	}
	// second pass marks seen ones alive again
	for _, p := range snap.Processes {
		id := Identity{Pid: p.Identity.Pid,
			StartSec: p.Identity.StartTime.Unix(), StartUsec: int64(p.Identity.StartTime.Nanosecond() / 1000),
			Exe:      p.Identity.Executable}
		if s, ok := a.series[id.Key()]; ok {
			s.alive = true
		}
	}
	// system reconciliation
	if a.lastSys != nil {
		dh := int64(a.lastSys.FreePages*a.lastSys.PageBytes) - int64(sys.FreePages*sys.PageBytes)
		if dh < 0 {
			dh = 0
		}
		a.observedGrowth = dh
	}
	a.lastSys = &sys
	a.lastCoverage = snap.Coverage
	a.lastQuality = snap.Quality
}

// Window closes the current attribution window (called at window boundaries).
func (a *Attributor) Window(sysNow model.SystemSample) Window {
	w := Window{
		At: sysNow.At, WindowS: a.windowS,
		ObservedGrowth: a.observedGrowth,
		CompressorBytes: int64(sysNow.CompressorPages) * int64(sysNow.PageBytes),
		Coverage:       a.lastCoverage,
		Quality:        a.lastQuality,
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
		if g.PeakDeltaRate == 0 {
			g.PeakDeltaRate = float64(s.delta) / float64(a.windowS)
		}
		if s.id.Pid != 0 && (g.LargestPid == 0 || s.delta > g.GrowthBytes/1) {
			if g.LargestPid == 0 {
				g.LargestPid = s.id.Pid
			}
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

// groupFor: nearest-anchor with sticky membership (§4).
func (a *Attributor) groupFor(id Identity) string {
	if g, ok := a.groups[id.Key()]; ok {
		return g // sticky within window
	}
	anchor := nearestAnchor(id)
	a.groups[id.Key()] = anchor
	return anchor
}

// nearestAnchor — without ppid telemetry yet, anchor = executable basename
// when known (anchor table match), else the process itself. ppid lineage is
// added when the platform layer exposes it (recorded as TODO in bead nqu).
func nearestAnchor(id Identity) string {
	for _, a := range AnchorTable {
		if id.Exe == a {
			return a
		}
	}
	if id.Exe != "" {
		return id.Exe
	}
	return fmt.Sprintf("pid-%d", id.Pid)
}

func (a *Attributor) groupLabel(gid string) string { return gid }

// AnchorTable — built-in group-worthy names (§4.1 b).
var AnchorTable = []string{
	"iTerm2", "Terminal", "sshd", "Code Helper", "node", "python3", "zsh",
	"Cursor Helper", "mempilotd",
}
