package main

import (
	"testing"
	"time"

	"memory_pilot/internal/model"
)

// Every test here is a near-miss observed on the live machine. The tool kills
// processes, so a rule that only works in theory is not a rule.

// snapAt is the instant every fixture snapshot is taken. A process's age is
// the distance from its StartTime to this, so fixtures must derive StartTime
// from the age they mean — hard-coding both silently made every fixture the
// same age, and the young-process test passed against code that did not
// implement the rule.
var snapAt = time.Unix(1793000000, 0)

func proc(pid, ppid int32, name, exe string, mb int, ageS, cpuS float64) model.ProcessSample {
	return model.ProcessSample{
		Identity: model.ProcessIdentity{
			Pid: pid, Ppid: ppid, Name: name, Executable: exe,
			StartTime: snapAt.Add(-time.Duration(ageS) * time.Second),
		},
		FootprintBytes: uint64(mb) << 20,
		CpuUserS:       cpuS,
		Quality:        model.QualGood,
	}
}

func snapOf(procs ...model.ProcessSample) model.ProcessSnapshot {
	return model.ProcessSnapshot{
		At:        snapAt,
		Processes: procs,
		Quality:   model.QualGood,
	}
}

// Measured live: pid 58684 was `claude --resume d3786ac4…`, the owner's active
// session, and proc_pidpath returned 0 for it so its identity was only the
// version directory "2.1.310". It appeared as an anonymous 337 MB process.
func TestUnidentifiableProcessIsNeverKilled(t *testing.T) {
	snap := snapOf(proc(58684, 1, "2.1.310", "2.1.310", 337, 7200, 0.5))
	got := selectVictims(snap, 256<<20, nil)
	if len(got) != 0 {
		t.Fatalf("selected a process with no readable image path: %+v", got[0])
	}
}

// Measured live: the running OpenCode binary is "…/bin/opencode.exe" — the
// npm package keeps the Windows suffix on macOS — and an exact-name list let
// it through as releasable.
func TestAgentWithExeSuffixIsProtected(t *testing.T) {
	exe := "/Users/x/node_modules/.pnpm/@opencode+cli@2.0.32/node_modules/@opencode/cli/bin/opencode.exe"
	snap := snapOf(proc(91083, 1, "opencode.exe", exe, 508, 400000, 133))
	if got := selectVictims(snap, 256<<20, nil); len(got) != 0 {
		t.Fatalf("selected a coding agent: %+v", got[0])
	}
}

// The terminal the work lives in is never a candidate.
func TestTerminalAndDesktopAreProtected(t *testing.T) {
	snap := snapOf(
		proc(100, 1, "wezterm-gui", "/Applications/WezTerm.app/Contents/MacOS/wezterm-gui", 900, 90000, 1),
		proc(101, 1, "Finder", "/System/Library/CoreServices/Finder.app/Contents/MacOS/Finder", 800, 90000, 1),
		proc(102, 1, "WindowServer", "/System/Library/PrivateFrameworks/SkyLight.framework/WindowServer", 700, 90000, 1),
	)
	if got := selectVictims(snap, 256<<20, nil); len(got) != 0 {
		t.Fatalf("selected a protected process: %+v", got[0])
	}
}

// A process using CPU is working. A leak allocates and does nothing; it does
// not burn cycles.
func TestBusyProcessIsNotACandidate(t *testing.T) {
	// 20 minutes of CPU over 300 minutes of life: clearly working.
	snap := snapOf(proc(200, 1, "worker", "/opt/bin/worker", 900, 18000, 1200))
	if got := selectVictims(snap, 256<<20, nil); len(got) != 0 {
		t.Fatalf("selected a working process: %+v", got[0])
	}
}

// Measured in a dry run: a rust-analyzer at "49m of 50m life" and a Chrome
// helper at "10m of 12m life" both qualified under a 120 s idle bar. Neither
// is a leak; they are young.
func TestYoungProcessIsNotACandidate(t *testing.T) {
	// 49 minutes old, 0.2 s CPU — exactly the rust-analyzer case.
	snap := snapOf(proc(22896, 1, "rust-analyzer", "/opt/rust-analyzer", 831, 49*60, 0.2))
	if got := selectVictims(snap, 256<<20, nil); len(got) != 0 {
		t.Fatalf("selected a 49-minute-old process: %+v", got[0])
	}
}

// The real case worth acting on: an old, large, idle, orphaned process.
func TestRealOrphanIsSelected(t *testing.T) {
	snap := snapOf(proc(96329, 1, "node", "/usr/local/bin/node", 1909, 264000, 24.2))
	got := selectVictims(snap, 256<<20, nil)
	if len(got) != 1 || got[0].pid != 96329 {
		t.Fatalf("did not select a genuine orphan: %+v", got)
	}
}

// Orphans come first. Measured live: the two largest candidates had live
// parents, so without ordering the biggest number would always be an ambiguous
// one.
func TestOrphansRankAboveParentedCandidates(t *testing.T) {
	snap := snapOf(
		proc(300, 1, "node", "/opt/bin/node", 1909, 264000, 24),   // huge, orphan
		proc(301, 302, "node", "/opt/bin/node", 1000, 264000, 24), // parented
		proc(302, 1, "node", "/opt/bin/node", 100, 264000, 24),    // the parent
	)
	got := selectVictims(snap, 256<<20, nil)
	if len(got) < 2 {
		t.Fatalf("expected at least two candidates, got %d", len(got))
	}
	if got[0].pid != 300 {
		t.Fatalf("largest orphan is not first: %+v", got[0])
	}
}

// Missing CPU evidence must never authorise a kill (SI-4).
func TestUnknownCpuIsNotEvidenceOfIdleness(t *testing.T) {
	p := proc(400, 1, "node", "/opt/bin/node", 900, 264000, 0)
	p.CpuUnavailable = true
	if got := selectVictims(snapOf(p), 256<<20, nil); len(got) != 0 {
		t.Fatalf("selected a process whose CPU could not be read: %+v", got[0])
	}
}

// The idle estimate can never exceed the process's own lifetime.
func TestIdleNeverExceedsLifetime(t *testing.T) {
	for _, c := range []struct{ cpu, age float64 }{
		{0, 3600}, {0.2, 2940}, {24, 264000}, {5000, 3600},
	} {
		idle := idleFrom(c.cpu, c.age)
		if idle > time.Duration(c.age)*time.Second {
			t.Errorf("cpu=%.1f age=%.0f: idle %s exceeds lifetime", c.cpu, c.age, idle)
		}
	}
}

// The -keep flag adds names without removing any built-in protection.
func TestExtraKeepIsAdditive(t *testing.T) {
	snap := snapOf(proc(500, 1, "mybuild", "/opt/bin/mybuild", 900, 264000, 1))
	if got := selectVictims(snap, 256<<20, nil); len(got) != 1 {
		t.Fatal("baseline: the process should be selectable")
	}
	keep := map[string]bool{"mybuild": true}
	if got := selectVictims(snap, 256<<20, keep); len(got) != 0 {
		t.Fatalf("-keep did not protect it: %+v", got[0])
	}
	// And the built-in list still applies with an extra keep in place.
	term := snapOf(proc(501, 1, "wezterm-gui", "/Applications/WezTerm.app/Contents/MacOS/wezterm-gui", 900, 264000, 1))
	if got := selectVictims(term, 256<<20, keep); len(got) != 0 {
		t.Fatalf("-keep weakened the built-in list: %+v", got[0])
	}
}
