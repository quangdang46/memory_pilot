package attribution

import (
	"strings"
	"testing"
	"time"

	"memory_pilot/internal/model"
)

var t0 = time.Unix(1700000000, 0)

func snap(pids map[[2]any]uint64) model.ProcessSnapshot {
	s := model.ProcessSnapshot{At: time.Now(), Quality: model.QualGood, Coverage: model.Coverage{Attempted: len(pids), Succeeded: len(pids)}}
	for k, b := range pids {
		pid := k[0].(int32)
		start := t0
		if k[1] != nil {
			start = k[1].(time.Time)
		}
		s.Processes = append(s.Processes, model.ProcessSample{
			Identity: model.ProcessIdentity{Pid: pid, StartTime: start}, ResidentBytes: b, Quality: model.QualGood})
	}
	return s
}

func sys(freePages uint64) model.SystemSample {
	return model.SystemSample{At: time.Now(), PageBytes: 16384, FreePages: freePages,
		CompressorPages: 50000, MachStatsQuality: model.QualGood, KernelLevelQuality: model.QualGood}
}

// Identity change on same pid is a new series — no delta across the change.
func TestIdentityChangeNoDelta(t *testing.T) {
	a := New(60)
	a.Observe(snap(map[[2]any]uint64{{int32(100), t0}: 1 << 30}), sys(200000))
	reborn := t0.Add(30 * time.Second)
	a.Observe(snap(map[[2]any]uint64{{int32(100), reborn}: 2 << 30}), sys(150000))
	w := a.Window(sys(150000))
	// reborn process observed once: delta recorded only across identical identity.
	// new identity first sighting = no delta; ended series keeps its history.
	if w.AccountedGrowth != 0 {
		t.Errorf("accounted %d want 0 (no cross-identity delta)", w.AccountedGrowth)
	}
}

func TestSameIdentityDelta(t *testing.T) {
	a := New(60)
	a.Observe(snap(map[[2]any]uint64{{int32(100), t0}: 1 << 30}), sys(200000))
	a.Observe(snap(map[[2]any]uint64{{int32(100), t0}: 2 << 30}), sys(180000))
	w := a.Window(sys(180000))
	if w.AccountedGrowth != 1<<30 {
		t.Errorf("accounted %d want 1GB", w.AccountedGrowth)
	}
}

// Coverage is never normalized: sums can exceed the system delta.
func TestCoverageNotNormalized(t *testing.T) {
	a := New(60)
	a.Observe(snap(map[[2]any]uint64{{int32(1), t0}: 1 << 30, {int32(2), t0}: 1 << 30}), sys(300000))
	a.Observe(snap(map[[2]any]uint64{{int32(1), t0}: 2 << 30, {int32(2), t0}: 2 << 30}), sys(250000))
	w := a.Window(sys(250000))
	if w.AccountedGrowth != 2<<30 {
		t.Fatalf("accounted %d", w.AccountedGrowth)
	}
	if w.CoverageRatio <= 1.0 {
		t.Errorf("coverage %v should exceed 1 (double-counting is normal)", w.CoverageRatio)
	}
	if !strings.Contains(w.Note, "can exceed the system delta") {
		t.Errorf("note missing: %s", w.Note)
	}
	if !strings.Contains(w.Note, "belong to no process") {
		t.Errorf("compression note missing: %s", w.Note)
	}
}

// Large but stable processes never appear in the growth ranking.
func TestLargeButStable(t *testing.T) {
	a := New(60)
	a.Observe(snap(map[[2]any]uint64{{int32(1), t0}: 600 << 20, {int32(2), t0}: 10 << 20}), sys(300000))
	a.Observe(snap(map[[2]any]uint64{{int32(1), t0}: 600 << 20, {int32(2), t0}: 200 << 20}), sys(300000))
	w := a.Window(sys(300000))
	for _, g := range w.Groups {
		if g.AnchorKey == "pid-1" {
			t.Errorf("stable process ranked as growth")
		}
	}
	if len(w.LargeButStable) == 0 {
		t.Errorf("large-but-stable section missing")
	}
}

// Grouping must follow ancestry, not bare executable name: ten `node`
// processes in two sessions are two groups, not one blob (MP-ALG-004 §4.3).
func TestGroupingByAncestryNotName(t *testing.T) {
	a := New(60)
	// Two terminal anchors, each with two node children.
	mk := func(entries [][4]any) model.ProcessSnapshot {
		s := model.ProcessSnapshot{At: time.Now(), Quality: model.QualGood}
		s.Coverage = model.Coverage{Attempted: len(entries), Succeeded: len(entries)}
		for _, e := range entries {
			s.Processes = append(s.Processes, model.ProcessSample{
				Identity: model.ProcessIdentity{Pid: e[0].(int32), Ppid: e[1].(int32),
					StartTime: t0, Executable: e[2].(string)},
				ResidentBytes: uint64(e[3].(int64)), Quality: model.QualGood})
		}
		return s
	}
	first := [][4]any{
		{int32(100), int32(1), "iTerm2", int64(100 << 20)},
		{int32(101), int32(100), "node", int64(100 << 20)},
		{int32(102), int32(100), "node", int64(100 << 20)},
		{int32(200), int32(1), "Terminal", int64(100 << 20)},
		{int32(201), int32(200), "node", int64(100 << 20)},
		{int32(202), int32(200), "node", int64(100 << 20)},
	}
	second := [][4]any{
		{int32(100), int32(1), "iTerm2", int64(100 << 20)},
		{int32(101), int32(100), "node", int64(300 << 20)},
		{int32(102), int32(100), "node", int64(300 << 20)},
		{int32(200), int32(1), "Terminal", int64(100 << 20)},
		{int32(201), int32(200), "node", int64(120 << 20)},
		{int32(202), int32(200), "node", int64(100 << 20)},
	}
	a.Observe(mk(first), sys(300000))
	a.Observe(mk(second), sys(280000))
	w := a.Window(sys(280000))

	groups := map[string]int64{}
	for _, g := range w.Groups {
		groups[g.AnchorKey] = g.GrowthBytes
	}
	if groups["iTerm2"] != 400<<20 {
		t.Errorf("iTerm2 group growth %d MB, want 400", groups["iTerm2"]>>20)
	}
	if groups["Terminal"] != 20<<20 {
		t.Errorf("Terminal group growth %d MB, want 20", groups["Terminal"]>>20)
	}
	if len(w.Groups) != 2 {
		t.Errorf("want 2 session groups, got %d: %+v", len(w.Groups), w.Groups)
	}
}

// A grandchild must reach the anchor through its parent chain.
func TestGroupingWalksGrandchildren(t *testing.T) {
	a := New(60)
	mk := func(sizes [2]int64) model.ProcessSnapshot {
		return model.ProcessSnapshot{At: time.Now(), Quality: model.QualGood,
			Coverage: model.Coverage{Attempted: 3, Succeeded: 3},
			Processes: []model.ProcessSample{
				{Identity: model.ProcessIdentity{Pid: 10, Ppid: 1, StartTime: t0, Executable: "iTerm2"}, ResidentBytes: uint64(50 << 20), Quality: model.QualGood},
				{Identity: model.ProcessIdentity{Pid: 11, Ppid: 10, StartTime: t0, Executable: "zsh"}, ResidentBytes: uint64(10 << 20), Quality: model.QualGood},
				{Identity: model.ProcessIdentity{Pid: 12, Ppid: 11, StartTime: t0, Executable: "node"}, ResidentBytes: uint64(sizes[0]), Quality: model.QualGood},
			}}
	}
	a.Observe(mk([2]int64{10 << 20, 0}), sys(300000))
	a.Observe(mk([2]int64{210 << 20, 0}), sys(290000))
	w := a.Window(sys(290000))
	if len(w.Groups) != 1 || w.Groups[0].AnchorKey != "iTerm2" {
		t.Fatalf("grandchild not attributed to iTerm2: %+v", w.Groups)
	}
	if w.Groups[0].GrowthBytes != 200<<20 {
		t.Errorf("growth %d MB, want 200", w.Groups[0].GrowthBytes>>20)
	}
}

// A window boundary must not erase growth: Reset drops accumulation but keeps
// the series' last observed size, so the next delta is still measured.
func TestResetKeepsSeriesBaseline(t *testing.T) {
	a := New(60)
	a.Observe(snap(map[[2]any]uint64{{int32(100), t0}: 100 << 20}), sys(300000))
	a.Observe(snap(map[[2]any]uint64{{int32(100), t0}: 200 << 20}), sys(290000))
	if w := a.Window(sys(290000)); w.AccountedGrowth != 100<<20 {
		t.Fatalf("first window %d MB, want 100", w.AccountedGrowth>>20)
	}
	a.Reset()
	w2 := a.Window(sys(290000))
	if w2.AccountedGrowth != 0 {
		t.Errorf("reset window still reports %d MB", w2.AccountedGrowth>>20)
	}
	a.Observe(snap(map[[2]any]uint64{{int32(100), t0}: 260 << 20}), sys(280000))
	if w3 := a.Window(sys(280000)); w3.AccountedGrowth != 60<<20 {
		t.Errorf("post-reset delta %d MB, want 60 (baseline must survive reset)", w3.AccountedGrowth>>20)
	}
}

// SeriesFor reports growth for exactly one anchor.
func TestSeriesForAnchor(t *testing.T) {
	a := New(300)
	mk := func(it, term uint64) model.ProcessSnapshot {
		return model.ProcessSnapshot{At: time.Now(), Quality: model.QualGood,
			Coverage: model.Coverage{Attempted: 4, Succeeded: 4},
			Processes: []model.ProcessSample{
				{Identity: model.ProcessIdentity{Pid: 10, Ppid: 1, StartTime: t0, Executable: "iTerm2"}, ResidentBytes: 50 << 20},
				{Identity: model.ProcessIdentity{Pid: 11, Ppid: 10, StartTime: t0, Executable: "node"}, ResidentBytes: it},
				{Identity: model.ProcessIdentity{Pid: 20, Ppid: 1, StartTime: t0, Executable: "Terminal"}, ResidentBytes: 50 << 20},
				{Identity: model.ProcessIdentity{Pid: 21, Ppid: 20, StartTime: t0, Executable: "python3"}, ResidentBytes: term},
			}}
	}
	a.Observe(mk(100<<20, 100<<20), sys(300000))
	a.Observe(mk(400<<20, 120<<20), sys(280000))
	it := a.SeriesFor("iTerm2")
	ter := a.SeriesFor("Terminal")
	// 300 MB over 300 s
	if it.SlopeBytesS < 0.9e6 || it.SlopeBytesS > 1.1e6 {
		t.Errorf("iTerm2 slope %v B/s, want ~1e6", it.SlopeBytesS)
	}
	if ter.SlopeBytesS > 0.1e6 {
		t.Errorf("Terminal slope %v B/s, want ~0.07e6", ter.SlopeBytesS)
	}
	// Both members of each group count, not only the one that grew last tick:
	// the group is the unit of reporting.
	if it.ProcessCount != 2 || ter.ProcessCount != 2 {
		t.Errorf("process counts it=%d term=%d", it.ProcessCount, ter.ProcessCount)
	}
	if it.CoverageRatio <= 0 {
		t.Errorf("coverage ratio not reported")
	}
}

// Evidence must survive window boundaries: a 5-minute report window can never
// satisfy a 20-minute sustained-growth requirement on its own, so the
// long-horizon record has to carry it across windows.
func TestHistorySurvivesWindowReset(t *testing.T) {
	a := New(60)
	base := time.Now().Add(-30 * time.Minute)
	var it uint64 = 100 << 20
	for i := 0; i < 120; i++ { // 30 minutes at 15 s
		at := base.Add(time.Duration(i) * 15 * time.Second)
		it += 5 << 20
		s := model.ProcessSnapshot{At: at, Quality: model.QualGood,
			Coverage: model.Coverage{Attempted: 2, Succeeded: 2},
			Processes: []model.ProcessSample{
				{Identity: model.ProcessIdentity{Pid: 10, Ppid: 1, StartTime: t0, Executable: "iTerm2"},
					ResidentBytes: 50 << 20, Quality: model.QualGood},
				{Identity: model.ProcessIdentity{Pid: 11, Ppid: 10, StartTime: t0, Executable: "node"},
					ResidentBytes: it, CpuUserS: float64(i) * 0.5, Quality: model.QualGood},
			}}
		a.Observe(s, sys(200000))
		if i%20 == 19 { // close a window every 5 minutes
			a.Window(sys(200000))
			a.Reset()
		}
	}
	st := a.SeriesFor("iTerm2")
	if st.DurationS < 1200 {
		t.Errorf("duration %ds < 1200: history did not survive window resets", st.DurationS)
	}
	if st.SlopeBytesS <= 0 {
		t.Errorf("no growth recorded across windows")
	}
	if st.ActivityCorr <= 0.5 {
		t.Errorf("activity correlation %v: growth was CPU-correlated and should read as work", st.ActivityCorr)
	}
}

// Observed growth must be measured across the window, not across the last
// tick, and a sawtooth must not be summed into fake growth.
func TestObservedGrowthAcrossWindow(t *testing.T) {
	a := New(60)
	free := uint64(200000)
	a.Observe(snap(map[[2]any]uint64{{int32(1), t0}: 100 << 20}), sys(free))
	// Rise, fall back, then fall 50 000 pages net across the window.
	for _, f := range []uint64{150000, 200000, 150000, 200000, 150000} {
		a.Observe(snap(map[[2]any]uint64{{int32(1), t0}: 100 << 20}), sys(f))
	}
	w := a.Window(sys(150000))
	want := int64(200000-150000) * 16384 // net across window
	if w.ObservedGrowth != want {
		t.Errorf("observed growth %d, want %d (window start → now, not per-tick sum)",
			w.ObservedGrowth, want)
	}
}

// A process that grows steadily with CPU work is workload growth, not a
// suspicion — the classification the reports were missing.
func TestSustainedCpuCorrelatedGrowthClassified(t *testing.T) {
	a := New(300)
	base := time.Now().Add(-25 * time.Minute)
	for i := 0; i < 100; i++ {
		at := base.Add(time.Duration(i) * 15 * time.Second)
		s := model.ProcessSnapshot{At: at, Quality: model.QualGood,
			Coverage: model.Coverage{Attempted: 2, Succeeded: 2},
			Processes: []model.ProcessSample{
				{Identity: model.ProcessIdentity{Pid: 10, Ppid: 1, StartTime: t0, Executable: "iTerm2"},
					ResidentBytes: 50 << 20, Quality: model.QualGood},
				{Identity: model.ProcessIdentity{Pid: 11, Ppid: 10, StartTime: t0, Executable: "node"},
					ResidentBytes: uint64(100<<20) + uint64(i)*(8<<20),
					CpuUserS:      float64(i) * 1.0, Quality: model.QualGood},
			}}
		a.Observe(s, sys(uint64(300000-i*100)))
	}
	st := a.SeriesFor("iTerm2")
	if st.DurationS < 1200 {
		t.Fatalf("duration %ds too short to classify", st.DurationS)
	}
	if st.ActivityCorr < 0.5 {
		t.Errorf("activity correlation %v should read as work", st.ActivityCorr)
	}
	if st.SlopeBytesS <= 0 {
		t.Errorf("no slope")
	}
}

// Quiet time is elapsed time, not (processes × elapsed time). With 60 idle
// children in one group, the old per-process accumulation reported ~30 hours
// of "quiet" after ~30 minutes of observation and marked every large group as
// a leak suspicion.
func TestQuietTimeNotMultipliedByProcessCount(t *testing.T) {
	a := New(300)
	base := time.Now().Add(-30 * time.Minute)
	const n = 60
	for i := 0; i < 120; i++ { // 30 minutes at 15 s
		at := base.Add(time.Duration(i) * 15 * time.Second)
		snap := model.ProcessSnapshot{At: at, Quality: model.QualGood,
			Coverage: model.Coverage{Attempted: n + 1, Succeeded: n + 1}}
		snap.Processes = append(snap.Processes, model.ProcessSample{
			Identity:      model.ProcessIdentity{Pid: 10, Ppid: 1, StartTime: t0, Executable: "iTerm2"},
			ResidentBytes: 50 << 20, Quality: model.QualGood})
		for j := 0; j < n; j++ {
			snap.Processes = append(snap.Processes, model.ProcessSample{
				Identity: model.ProcessIdentity{Pid: int32(100 + j), Ppid: 10, StartTime: t0, Executable: "node"},
				// Every child grows a little; none burns CPU.
				ResidentBytes: uint64(10<<20) + uint64(i)*1024,
				Quality:       model.QualGood})
		}
		a.Observe(snap, sys(200000))
	}
	st := a.SeriesFor("iTerm2")
	observed := a.now.Sub(base).Seconds()
	if float64(st.QuietS) > observed+120 {
		t.Errorf("quiet %ds exceeds observed window %ds — per-process inflation",
			st.QuietS, int64(observed))
	}
	if st.QuietS < 600 {
		t.Errorf("quiet %ds should still reach the 600s threshold honestly", st.QuietS)
	}
}

// The series map is keyed by (pid, start time), so it grows with every process
// the machine starts. Before eviction it never shrank: a daemon running for a
// day on a build-heavy machine held an entry for every compiler, shell and
// helper that had ever run. The product must not leak while reporting leaks.
func TestDeadSeriesAreEvicted(t *testing.T) {
	a := New(60)
	base := time.Now()
	// 500 short-lived processes come and go over 20 ticks.
	for tick := 0; tick < 20; tick++ {
		at := base.Add(time.Duration(tick) * 15 * time.Second)
		var procs []model.ProcessSample
		// One survivor.
		procs = append(procs, model.ProcessSample{
			Identity:      model.ProcessIdentity{Pid: 1, Ppid: 1, StartTime: t0, Executable: "launchd"},
			ResidentBytes: 10 << 20, Quality: model.QualGood})
		// 25 fresh processes this tick, each dies immediately.
		for j := 0; j < 25; j++ {
			pid := int32(1000 + tick*100 + j)
			procs = append(procs, model.ProcessSample{
				Identity: model.ProcessIdentity{Pid: pid, Ppid: 1,
					StartTime: base.Add(time.Duration(tick) * time.Second), Executable: "clang"},
				ResidentBytes: 5 << 20, Quality: model.QualGood})
		}
		s := model.ProcessSnapshot{At: at, Quality: model.QualGood,
			Coverage:  model.Coverage{Attempted: len(procs), Succeeded: len(procs)},
			Processes: procs}
		a.Observe(s, sys(200000))
		a.Window(sys(200000))
		a.Reset()
	}
	// One more tick with only the survivor: the processes from the previous
	// tick are now genuinely gone (a process seen in the latest snapshot is
	// "alive" by definition, which is why the assertion needs this tick).
	final := model.ProcessSnapshot{At: base.Add(20 * 15 * time.Second),
		Quality: model.QualGood, Coverage: model.Coverage{Attempted: 1, Succeeded: 1},
		Processes: []model.ProcessSample{{Identity: model.ProcessIdentity{
			Pid: 1, Ppid: 1, StartTime: t0, Executable: "launchd"},
			ResidentBytes: 10 << 20, Quality: model.QualGood}}}
	a.Observe(final, sys(200000))
	a.Observe(final, sys(200000)) // grace tick
	// 500 dead processes were seen; only the live ones should remain.
	live := 0
	for _, s := range a.series {
		if s.alive {
			live++
		}
	}
	if live != 1 {
		t.Errorf("live series %d, want 1", live)
	}
	if n := len(a.series); n > 60 {
		t.Errorf("series map holds %d entries after 500 short-lived processes — leaking", n)
	}
}

// Measured on the target machine: a process holding 200 MB and doing nothing
// moved 217 MB by resident_size but 10 MB by phys_footprint. Growth must be
// computed from the footprint, or an idle process reads as a leak and a real
// leak reads as a shrinking process.
func TestGrowthUsesFootprintNotResident(t *testing.T) {
	a := New(300)
	base := time.Now()
	mk := func(at time.Time, footprint, resident uint64) model.ProcessSnapshot {
		return model.ProcessSnapshot{At: at, Quality: model.QualGood,
			Coverage: model.Coverage{Attempted: 1, Succeeded: 1},
			Processes: []model.ProcessSample{{
				Identity: model.ProcessIdentity{Pid: 7, Ppid: 1, StartTime: t0,
					Executable: "leaky"},
				FootprintBytes: footprint,
				ResidentBytes:  resident,
				Quality:        model.QualGood}}}
	}
	// Steady 100 MB footprint. Resident flaps wildly, as it does under
	// pressure (measured range on this machine: 8 MB to 225 MB).
	a.Observe(mk(base, 100<<20, 200<<20), sys(200000))
	residents := []uint64{25 << 20, 190 << 20, 8 << 20, 225 << 20, 12 << 20}
	for i, r := range residents {
		at := base.Add(time.Duration(i+1) * 15 * time.Second)
		a.Observe(mk(at, 100<<20, r), sys(200000))
	}
	w := a.Window(sys(200000))
	if w.AccountedGrowth != 0 {
		t.Errorf("idle process reported %d MB of growth from resident noise",
			w.AccountedGrowth>>20)
	}
	if g := a.SeriesFor("leaky"); g.SlopeBytesS > 0.1e6 {
		t.Errorf("slope %v B/s from resident noise, want ~0", g.SlopeBytesS)
	}
}

// The converse: footprint climbs while resident collapses. This is what a real
// leak looks like on a machine that is already swapping. It must be visible.
func TestLeakVisibleWhileResidentCollapses(t *testing.T) {
	a := New(300)
	base := time.Now()
	var foot uint64 = 60 << 20
	var res uint64 = 70 << 20
	for i := 0; i < 20; i++ {
		at := base.Add(time.Duration(i) * 15 * time.Second)
		s := model.ProcessSnapshot{At: at, Quality: model.QualGood,
			Coverage: model.Coverage{Attempted: 1, Succeeded: 1},
			Processes: []model.ProcessSample{{
				Identity: model.ProcessIdentity{Pid: 7, Ppid: 1, StartTime: t0,
					Executable: "leaky"},
				FootprintBytes: foot, ResidentBytes: res, Quality: model.QualGood}}}
		a.Observe(s, sys(200000))
		foot += 10 << 20
		if res > 20<<20 {
			res -= 5 << 20 // being reclaimed, as under pressure
		}
	}
	st := a.SeriesFor("leaky")
	if st.SlopeBytesS < 0.5e6 {
		t.Errorf("leak invisible: slope %v B/s while footprint grew 10 MB/15 s",
			st.SlopeBytesS)
	}
}
