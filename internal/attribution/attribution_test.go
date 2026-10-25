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

// Release must be tracked over the long horizon, not only within the current
// window. A sawtooth allocates and frees repeatedly; if the frees land in a
// window boundary its per-window release reads 0 and it is misread as a leak.
// Measured: probe-sawtooth reported release=0 in four consecutive windows
// while continuously releasing memory.
func TestReleaseAccumulatesAcrossResets(t *testing.T) {
	a := New(60)
	base := time.Now()
	it := int64(100 << 20)
	for i := 0; i < 40; i++ {
		at := base.Add(time.Duration(i) * 15 * time.Second)
		// grow 30 MB, then release 30 MB, alternating — no net accumulation
		if i%2 == 0 {
			it += 30 << 20
		} else {
			it -= 30 << 20
		}
		s := model.ProcessSnapshot{At: at, Quality: model.QualGood,
			Coverage: model.Coverage{Attempted: 1, Succeeded: 1},
			Processes: []model.ProcessSample{{
				Identity: model.ProcessIdentity{Pid: 5, Ppid: 1, StartTime: t0,
					Executable: "sawtooth"},
				FootprintBytes: uint64(it), Quality: model.QualGood}}}
		a.Observe(s, sys(200000))
		if i%4 == 3 { // close a window every minute
			a.Window(sys(200000))
			a.Reset()
		}
	}
	st := a.SeriesFor("sawtooth")
	if st.ReleaseBytes <= 0 {
		t.Errorf("release=0 after 40 alternating samples — cycling invisible")
	}
	if st.ReleaseBytes < st.GrowthBytes/2 {
		t.Errorf("release %d MB vs growth %d MB: sawtooth should release about as much as it grows",
			st.ReleaseBytes>>20, st.GrowthBytes>>20)
	}
}

// The live daemon crashed with "concurrent map read and map write": the
// process sampler (15 s) and the system sampler (5 s) both drive the
// Attributor from different goroutines. This test is the same shape; run it
// with -race to prove the lock is real.
func TestConcurrentObserveAndWindow(t *testing.T) {
	a := New(60)
	base := time.Now()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 300; i++ {
			at := base.Add(time.Duration(i) * 15 * time.Second)
			s := model.ProcessSnapshot{At: at, Quality: model.QualGood,
				Coverage: model.Coverage{Attempted: 2, Succeeded: 2},
				Processes: []model.ProcessSample{
					{Identity: model.ProcessIdentity{Pid: 1, Ppid: 0, StartTime: t0,
						Executable: "wezterm-gui"}, FootprintBytes: uint64(i) << 20},
					{Identity: model.ProcessIdentity{Pid: 2, Ppid: 1, StartTime: t0,
						Executable: "node"}, FootprintBytes: uint64(2*i) << 20},
				}}
			a.Observe(s, sys(200000))
		}
	}()
	for i := 0; i < 300; i++ {
		s := sys(200000)
		s.At = base.Add(time.Duration(i) * 5 * time.Second)
		a.CloseWindowAndReset(s)
		_ = a.SeriesFor("wezterm-gui")
		_ = a.ProcessCount()
	}
	<-done
}

// A group's long-horizon record must not outlive the group. Observed live:
// probes grouped under "probe-leak" in run15 (which began at 11:40) reported
// DurationS=884s at 11:45 — 578s more than they had existed — because a run12
// group with the same name had been recorded an hour earlier and its history
// was never retired. DurationS gates the leak branch (classify.go:145), so a
// freshly started group inherits the old one's evidence and can be called a
// leak without a single second of its own observation.
func TestHistoryRetiredWhenGroupDisappears(t *testing.T) {
	a := New(60)
	base := time.Now().Add(-2 * time.Hour)
	mk := func(at time.Time, pid int32, start time.Time, mb uint64) model.ProcessSnapshot {
		return model.ProcessSnapshot{At: at, Quality: model.QualGood,
			Coverage: model.Coverage{Attempted: 1, Succeeded: 1},
			Processes: []model.ProcessSample{{Identity: model.ProcessIdentity{
				Pid: pid, Ppid: 1, StartTime: start, Executable: "probe-leak"},
				FootprintBytes: mb << 20, Quality: model.QualGood}}}
	}
	// An old incarnation: observed for 30 minutes, then gone for a long while.
	for i := 0; i < 120; i++ {
		at := base.Add(time.Duration(i) * 15 * time.Second)
		a.Observe(mk(at, 100, t0, 100), sys(200000))
		a.CloseWindowAndReset(model.SystemSample{At: at, PageBytes: 16384,
			FreePages: 200000, MachStatsQuality: model.QualGood,
			KernelLevelQuality: model.QualGood})
	}
	if a.history["probe-leak"] == nil {
		t.Fatal("old incarnation left no history — test cannot reproduce the defect")
	}
	// Nothing of that group is running any more, for longer than the evidence
	// threshold, so the group is unambiguously gone rather than briefly hidden.
	gone := base.Add(30 * time.Minute)
	for i := 0; i < 40; i++ {
		at := gone.Add(time.Duration(i) * 15 * time.Second)
		a.Observe(model.ProcessSnapshot{At: at, Quality: model.QualGood,
			Coverage: model.Coverage{Attempted: 1, Succeeded: 1}}, sys(200000))
	}
	// A brand-new process with the same executable name starts now.
	fresh := gone.Add(10 * time.Minute)
	for i := 0; i < 8; i++ {
		at := fresh.Add(time.Duration(i) * 15 * time.Second)
		a.Observe(mk(at, 200, fresh, 100), sys(200000))
	}
	h := a.history["probe-leak"]
	if h == nil {
		t.Fatal("new group has no history")
	}
	if h.firstSeen.Before(fresh) {
		t.Errorf("history survived the group: firstSeen %v is before the new "+
			"process started %v — a new group would inherit the old group's "+
			"evidence window", h.firstSeen, fresh)
	}
}

// A process that starts while the daemon is watching arrives holding memory,
// and that arrival is growth. Measured in run16: probe-children held 127 MB
// across nine processes while the window reported 5 MB, because each child was
// spawned at full size and only its later change was counted — so the very
// shape the subprocess_accumulation bucket exists for could not be seen.
func TestNewProcessArrivalCountsAsGrowth(t *testing.T) {
	a := New(300)
	base := time.Now()
	// The daemon has been watching for a while: pid 1 predates it.
	daemonStart := base
	for i := 0; i < 4; i++ {
		at := base.Add(time.Duration(i) * 15 * time.Second)
		a.Observe(model.ProcessSnapshot{At: at, Quality: model.QualGood,
			Coverage: model.Coverage{Attempted: 1, Succeeded: 1},
			Processes: []model.ProcessSample{{Identity: model.ProcessIdentity{
				Pid: 1, Ppid: 1, StartTime: t0, Executable: "probe-children"},
				FootprintBytes: 3 << 20, Quality: model.QualGood}}}, sys(200000))
	}
	// A child starts now, at 15 MB, and then does not change.
	childStart := daemonStart.Add(60 * time.Second)
	for i := 0; i < 3; i++ {
		at := base.Add(time.Duration(4+i) * 15 * time.Second)
		a.Observe(model.ProcessSnapshot{At: at, Quality: model.QualGood,
			Coverage: model.Coverage{Attempted: 2, Succeeded: 2},
			Processes: []model.ProcessSample{
				{Identity: model.ProcessIdentity{Pid: 1, Ppid: 1, StartTime: t0,
					Executable: "probe-children"}, FootprintBytes: 3 << 20,
					Quality: model.QualGood},
				{Identity: model.ProcessIdentity{Pid: 2, Ppid: 1, StartTime: childStart,
					Executable: "probe-children"}, FootprintBytes: 15 << 20,
					Quality: model.QualGood}}}, sys(200000))
	}
	st := a.SeriesFor("probe-children")
	if got := st.GrowthBytes >> 20; got < 15 {
		t.Errorf("group growth %d MB, want >= 15: a child that started during "+
			"observation and holds 15 MB was not counted", got)
	}
}

// The other half: processes that already existed when the daemon started are
// baseline, not growth. Counting them made startup read as ~20 GB.
func TestPreexistingProcessesAreBaseline(t *testing.T) {
	a := New(300)
	base := time.Now()
	for i := 0; i < 4; i++ {
		at := base.Add(time.Duration(i) * 15 * time.Second)
		a.Observe(model.ProcessSnapshot{At: at, Quality: model.QualGood,
			Coverage: model.Coverage{Attempted: 2, Succeeded: 2},
			Processes: []model.ProcessSample{
				{Identity: model.ProcessIdentity{Pid: 1, Ppid: 1, StartTime: t0,
					Executable: "wezterm-gui"}, FootprintBytes: 500 << 20,
					Quality: model.QualGood},
				// A process that started before observation began.
				{Identity: model.ProcessIdentity{Pid: 2, Ppid: 1,
					StartTime: base.Add(-time.Hour), Executable: "node"},
					FootprintBytes: 800 << 20, Quality: model.QualGood}}}, sys(200000))
	}
	st := a.SeriesFor("wezterm-gui")
	if got := st.GrowthBytes >> 20; got != 0 {
		t.Errorf("steady group reports %d MB of growth: processes present at "+
			"startup are baseline, not growth", got)
	}
}

// A process that exits releases its memory. Without this the accounting only
// runs one way — arrivals add, departures never subtract — so a group that
// spawns short-lived children accumulates their peak footprint forever.
// Measured: wezterm-gui read 632-787 MB of "growth" per 5-minute window from
// compilers and helpers coming and going, while holding none of it.
func TestExitedProcessReleasesItsMemory(t *testing.T) {
	a := New(300)
	base := time.Now()
	// A long-lived parent.
	parent := model.ProcessSample{Identity: model.ProcessIdentity{
		Pid: 9001, Ppid: 1, StartTime: t0, Executable: "wezterm-gui"},
		FootprintBytes: 50 << 20, Quality: model.QualGood}
	// Build a snapshot: parent, plus one short-lived 200 MB child that appears
	// on tick 2 and is gone by tick 4.
	for i := 0; i < 8; i++ {
		at := base.Add(time.Duration(i) * 15 * time.Second)
		procs := []model.ProcessSample{parent}
		if i >= 2 && i < 4 {
			procs = append(procs, model.ProcessSample{Identity: model.ProcessIdentity{
				Pid: 9002, Ppid: 9001, StartTime: base.Add(2 * 15 * time.Second),
				Executable: "clang"}, FootprintBytes: 200 << 20,
				Quality: model.QualGood})
		}
		a.Observe(model.ProcessSnapshot{At: at, Quality: model.QualGood,
			Coverage:  model.Coverage{Attempted: len(procs), Succeeded: len(procs)},
			Processes: procs}, sys(200000))
	}
	h := a.history["wezterm-gui"]
	if h == nil {
		t.Fatal("no history for the terminal group")
	}
	const arrived = int64(200 << 20)
	if h.releaseBytes < arrived {
		t.Errorf("release %d MB, want >= %d MB: a 200 MB child exited and "+
			"nothing was counted as released — the group only ever grows",
			h.releaseBytes>>20, arrived>>20)
	}
}

// A group that has never grown while being watched has been flat for its whole
// life, not for zero seconds. Leaving PlateauS at 0 made the plateau branch
// unreachable for exactly those groups, and they fell through to the leak
// branch instead — "never grew" read as "suspicious growth".
func TestNeverGrownGroupReportsItsFullPlateau(t *testing.T) {
	a := New(300)
	base := time.Now().Add(-30 * time.Minute)
	for i := 0; i < 120; i++ { // 30 minutes, constant size
		at := base.Add(time.Duration(i) * 15 * time.Second)
		a.Observe(model.ProcessSnapshot{At: at, Quality: model.QualGood,
			Coverage: model.Coverage{Attempted: 1, Succeeded: 1},
			Processes: []model.ProcessSample{{Identity: model.ProcessIdentity{
				Pid: 600, Ppid: 1, StartTime: t0, Executable: "flat-app"},
				FootprintBytes: 200 << 20, Quality: model.QualGood}}}, sys(200000))
	}
	st := a.SeriesFor("flat-app")
	if st.PlateauS < 900 {
		t.Errorf("flat group reports PlateauS=%ds after 1800s of flatness: the "+
			"plateau branch cannot fire on a group that never grew", st.PlateauS)
	}
}

// A group's process count must be the group's, not the number of its members
// that happened to move this window. Splitting per process gave the classifier
// a count from one half of a group and growth from the other. Measured in
// run20 on probe-children: 21 real processes reported as 15, then 21, then 1
// while all 21 were still running.
func TestGroupProcessCountIsWholeGroup(t *testing.T) {
	a := New(300)
	base := time.Now()
	// Ten children under one parent: nine flat at 15 MB, one that grew 200 MB.
	mk := func(at time.Time, growMB uint64) model.ProcessSnapshot {
		procs := []model.ProcessSample{{Identity: model.ProcessIdentity{
			Pid: 1000, Ppid: 1, StartTime: t0, Executable: "probe-children"},
			FootprintBytes: 3 << 20, Quality: model.QualGood}}
		for i := 1; i <= 9; i++ {
			procs = append(procs, model.ProcessSample{Identity: model.ProcessIdentity{
				Pid: int32(1000 + i), Ppid: 1000, StartTime: t0,
				Executable: "probe-children"}, FootprintBytes: 15 << 20,
				Quality: model.QualGood})
		}
		procs = append(procs, model.ProcessSample{Identity: model.ProcessIdentity{
			Pid: 1100, Ppid: 1000, StartTime: t0, Executable: "probe-children"},
			FootprintBytes: (15 + growMB) << 20, Quality: model.QualGood})
		return model.ProcessSnapshot{At: at, Quality: model.QualGood,
			Coverage:  model.Coverage{Attempted: len(procs), Succeeded: len(procs)},
			Processes: procs}
	}
	a.Observe(mk(base, 0), sys(200000))
	a.Observe(mk(base.Add(15*time.Second), 200), sys(200000))
	w := a.Window(sys(200000))

	// The classifier reads SeriesFor, which is what must carry the whole group.
	st := a.SeriesFor("probe-children")
	if st.ProcessCount != 11 {
		t.Errorf("SeriesFor reports %d processes, want 11: the classifier is "+
			"shown growth from one member and a process count from another",
			st.ProcessCount)
	}
	// And the window must not present the same group twice, once as a mover
	// and once as flat: that is the split itself.
	inGroups, inFlat := false, false
	for _, g := range w.Groups {
		if g.AnchorKey == "probe-children" {
			inGroups = true
		}
	}
	for _, g := range w.Flat {
		if g.AnchorKey == "probe-children" {
			inFlat = true
		}
	}
	if inGroups && inFlat {
		t.Error("group appears both as a mover and as flat in one window")
	}
}

// A high-water process count must be a recent peak, not a lifetime record. A
// group that spawned 38 processes once during a build and holds 1 afterwards
// is not accumulating; with a permanent maximum it qualifies as accumulation
// for the rest of the daemon's life. Measured: bash, 1 process on the machine,
// reported "process count reached 38" every window after one busy moment.
func TestProcessPeakDecaysWhenGroupShrinks(t *testing.T) {
	a := New(300)
	base := time.Now()
	snap := func(at time.Time, n int) model.ProcessSnapshot {
		procs := []model.ProcessSample{{Identity: model.ProcessIdentity{
			Pid: 1, Ppid: 0, StartTime: t0, Executable: "launchd"},
			FootprintBytes: 10 << 20, Quality: model.QualGood}}
		for i := 0; i < n; i++ {
			procs = append(procs, model.ProcessSample{Identity: model.ProcessIdentity{
				Pid: int32(100 + i), Ppid: 1, StartTime: t0,
				Executable: "bash"}, FootprintBytes: 5 << 20, Quality: model.QualGood})
		}
		return model.ProcessSnapshot{At: at, Quality: model.QualGood,
			Coverage:  model.Coverage{Attempted: len(procs), Succeeded: len(procs)},
			Processes: procs}
	}
	// A busy moment: 30 bash processes.
	a.Observe(snap(base, 30), sys(200000))
	// Then it settles to 2 and stays there for a long while.
	for i := 1; i <= 40; i++ {
		a.Observe(snap(base.Add(time.Duration(i)*15*time.Second), 2), sys(200000))
	}
	st := a.SeriesFor("bash")
	if st.PeakProcessCount > 8 {
		t.Errorf("peak process count still %d after 10 minutes at 2 processes: "+
			"a single busy moment qualifies the group as accumulation forever",
			st.PeakProcessCount)
	}
}

// Real accumulation keeps its children, so its peak must survive.
func TestRealAccumulationKeepsItsPeak(t *testing.T) {
	a := New(300)
	base := time.Now()
	snap := func(at time.Time, n int) model.ProcessSnapshot {
		procs := []model.ProcessSample{{Identity: model.ProcessIdentity{
			Pid: 1, Ppid: 0, StartTime: t0, Executable: "launchd"},
			FootprintBytes: 10 << 20, Quality: model.QualGood}}
		for i := 0; i < n; i++ {
			procs = append(procs, model.ProcessSample{Identity: model.ProcessIdentity{
				Pid: int32(100 + i), Ppid: 1, StartTime: t0,
				Executable: "probe-children"}, FootprintBytes: 15 << 20,
				Quality: model.QualGood})
		}
		return model.ProcessSnapshot{At: at, Quality: model.QualGood,
			Coverage:  model.Coverage{Attempted: len(procs), Succeeded: len(procs)},
			Processes: procs}
	}
	for i := 0; i <= 40; i++ {
		a.Observe(snap(base.Add(time.Duration(i)*15*time.Second), 21), sys(200000))
	}
	st := a.SeriesFor("probe-children")
	if st.PeakProcessCount < 21 || st.ProcessCount < 21 {
		t.Errorf("21 held children: peak %d, count %d — accumulation lost its "+
			"signature", st.PeakProcessCount, st.ProcessCount)
	}
}

// A terminal's children are what the user chose to run. Measured on this
// machine: wezterm-gui had 86 descendants holding 8.4 GB — editors, agents and
// builds the owner started by hand. Calling that "accumulating subprocesses"
// tells the user about their own work.
func TestTerminalAnchorsAreRecognised(t *testing.T) {
	for _, name := range []string{"wezterm-gui", "Terminal", "iTerm2", "tmux", "sshd"} {
		if !IsTerminalGroup(name) {
			t.Errorf("%q is a terminal but IsTerminalGroup says no — its "+
				"subprocess advisory will still fire", name)
		}
	}
	// Applications must not be swallowed by the exemption.
	for _, name := range []string{"node", "Google Chrome", "Docker", "probe-children", "claude"} {
		if IsTerminalGroup(name) {
			t.Errorf("%q is not a terminal but was exempted from advisories", name)
		}
	}
}

// Spawning and reaping helpers must leave net size unchanged. Observed live:
// node spawned ~220 MB helpers per session and reaped them, and net grew by
// 2846 MB in 285 seconds while the group held a steady 4.3 GB. Arrivals added
// to net; departures only added to release. That is defect 13 one layer up.
func TestChurnDoesNotInflateNetSize(t *testing.T) {
	a := New(300)
	base := time.Now()
	parent := model.ProcessSample{Identity: model.ProcessIdentity{
		Pid: 5000, Ppid: 1, StartTime: t0, Executable: "node"},
		FootprintBytes: 1000 << 20, Quality: model.QualGood}

	// Twenty short-lived 200 MB helpers come and go under the parent.
	var held []int32
	for i := 0; i < 80; i++ {
		at := base.Add(time.Duration(i) * 15 * time.Second)
		procs := []model.ProcessSample{parent}
		// Spawn one, reap one, keeping the population steady.
		if i%2 == 0 {
			pid := int32(6000 + i)
			held = append(held, pid)
			procs = append(procs, model.ProcessSample{Identity: model.ProcessIdentity{
				Pid: pid, Ppid: 5000, StartTime: at, Executable: "node"},
				FootprintBytes: 200 << 20, Quality: model.QualGood})
		} else if len(held) > 0 {
			held = held[:len(held)-1] // this one is absent from the snapshot
		}
		for _, pid := range held {
			if i%2 == 0 && pid == held[len(held)-1] {
				continue // already added above
			}
			procs = append(procs, model.ProcessSample{Identity: model.ProcessIdentity{
				Pid: pid, Ppid: 5000, StartTime: base, Executable: "node"},
				FootprintBytes: 200 << 20, Quality: model.QualGood})
		}
		a.Observe(model.ProcessSnapshot{At: at, Quality: model.QualGood,
			Coverage:  model.Coverage{Attempted: len(procs), Succeeded: len(procs)},
			Processes: procs}, sys(200000))
	}
	st := a.SeriesFor("node")
	// The group held roughly 1 GB plus a couple of helpers throughout.
	if st.NetGrowthBytes > 1500<<20 {
		t.Errorf("net growth %d MB after 20 minutes of steady churn around "+
			"~1.2 GB — arrivals are counted but departures are not",
			st.NetGrowthBytes>>20)
	}
}
