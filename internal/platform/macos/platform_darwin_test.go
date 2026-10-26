//go:build darwin

package macos

import (
	"context"
	"strings"
	"testing"
	"time"
)

// The live collector is only useful if it fills the fields the rate math and
// identity rules depend on. These assertions run against the real host.

func TestLiveSystemSampleHasMonotonicClock(t *testing.T) {
	p, err := New()
	if err != nil {
		t.Fatal(err)
	}
	a, err := p.SampleSystem(context.Background())
	if err != nil {
		t.Fatalf("SampleSystem: %v", err)
	}
	if a.MonotonicMs == 0 {
		t.Fatal("MonotonicMs is 0 — dwell gating and every rate would be dead")
	}
	time.Sleep(120 * time.Millisecond)
	b, err := p.SampleSystem(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if b.MonotonicMs <= a.MonotonicMs {
		t.Errorf("monotonic clock did not advance: %d → %d (wall %v → %v)",
			a.MonotonicMs, b.MonotonicMs, a.At, b.At)
	}
	if b.PageBytes == 0 || b.MachStatsQuality != "good" {
		t.Errorf("live sample degraded: page=%d quality=%s", b.PageBytes, b.MachStatsQuality)
	}
}

func TestLiveProcessIdentities(t *testing.T) {
	p, err := New()
	if err != nil {
		t.Fatal(err)
	}
	snap, err := p.Processes(context.Background())
	if err != nil {
		t.Fatalf("Processes: %v", err)
	}
	if len(snap.Processes) == 0 {
		t.Fatal("no processes returned")
	}
	named, withStart, withPpid, withRSS := 0, 0, 0, 0
	for _, s := range snap.Processes {
		if s.Identity.Executable != "" {
			named++
		}
		if !s.Identity.StartTime.IsZero() {
			withStart++
		}
		if s.Identity.Ppid != 0 {
			withPpid++
		}
		if s.ResidentBytes > 0 {
			withRSS++
		}
	}
	n := len(snap.Processes)
	// Readable names/start times need no privilege; a large shortfall means
	// the bsdinfo call is failing and identity grouping is silently blind.
	if named < n*9/10 {
		t.Errorf("only %d/%d processes have a name", named, n)
	}
	if withStart < n*9/10 {
		t.Errorf("only %d/%d processes have a start time — pid reuse would double-count", withStart, n)
	}
	if withPpid < n*9/10 {
		t.Errorf("only %d/%d processes have a ppid — grouping by ancestry is impossible", withPpid, n)
	}
	if withRSS < n/2 {
		t.Errorf("only %d/%d processes have resident bytes", withRSS, n)
	}
	// Identity must actually distinguish processes, not collapse to one series.
	seen := map[string]int{}
	for _, s := range snap.Processes {
		seen[s.Identity.Executable]++
	}
	if len(seen) < 10 {
		t.Errorf("only %d distinct executable names — identity is degenerate", len(seen))
	}
}

func TestLiveCommandNameNonEmpty(t *testing.T) {
	p, err := New()
	if err != nil {
		t.Fatal(err)
	}
	snap, _ := p.Processes(context.Background())
	unnamed := 0
	for _, s := range snap.Processes {
		if s.Identity.Executable == "" && s.Identity.Name == "" {
			unnamed++
		}
		// Name is comm, bounded at MAXCOMLEN (16). Executable is the full image
		// path, which is what identification needs — comm reported three
		// `claude` processes as "2.1.310", the bundle name.
		if s.Identity.Name != "" && len(s.Identity.Name) > 16 {
			t.Errorf("comm longer than MAXCOMLEN: %q", s.Identity.Name)
		}
	}
	// The full path must be present for most processes; comm is a fallback for
	// the ones proc_pidpath refuses.
	paths := 0
	for _, s := range snap.Processes {
		if strings.HasPrefix(s.Identity.Executable, "/") {
			paths++
		}
	}
	if paths < len(snap.Processes)/2 {
		t.Errorf("only %d/%d processes have a full path", paths, len(snap.Processes))
	}
	// A handful of zombies and kernel threads have no readable image. They
	// must stay rare: if most processes are nameless the sampler is broken.
	if unnamed > len(snap.Processes)/10 {
		t.Errorf("%d/%d processes have neither a path nor a comm",
			unnamed, len(snap.Processes))
	}
}
