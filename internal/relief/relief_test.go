package relief

import (
	"strings"
	"testing"
	"time"
)

func facts(group string, mb int64, quiet time.Duration) GroupFacts {
	return GroupFacts{
		Group:     group,
		Footprint: mb << 20,
		QuietFor:  quiet,
	}
}

// T-PR-1: a small abandoned process never outranks a large busy one for
// permission. The busy one is not eligible at all.
func TestBusyProcessIsNeverACandidate(t *testing.T) {
	busy := facts("Code", 3000, 5*time.Second)
	c := Assess(busy)
	if c.State != StateUseful {
		t.Errorf("a process that used CPU 5s ago is %q, want useful: %v",
			c.State, c.Evidence)
	}
	if c.ExpectedReliefBytes != 0 {
		t.Error("a useful process was given relief value")
	}
}

// The owner's actual case: VS Code holding 3 GB while compiling.
func TestWorkingIDEIsNotAWarning(t *testing.T) {
	c := Assess(facts("Code", 3146, 30*time.Second))
	if c.State != StateUseful {
		t.Errorf("Code compiling was classified %q: %v", c.State, c.Evidence)
	}
}

// And Chrome loading pages, which produced two advisories at 22:56.
func TestLoadingBrowserIsNotAWarning(t *testing.T) {
	for _, q := range []time.Duration{0, 30 * time.Second, 120 * time.Second} {
		c := Assess(facts("Google Chrome", 2405, q))
		if c.State == StateAbandoned {
			t.Errorf("Chrome quiet %v classified abandoned", q)
		}
	}
}

// Terminals are excluded by rule: their children are the owner's own work.
func TestTerminalIsNeverACandidate(t *testing.T) {
	f := facts("wezterm-gui", 2000, 30*time.Minute)
	f.IsTerminal = true
	c := Assess(f)
	if c.State != StateUseful {
		t.Errorf("terminal classified %q: %v", c.State, c.Evidence)
	}
}

// T-PR-3: a coding agent is never eligible without explicit authorization.
func TestAgentIsNeverACandidate(t *testing.T) {
	f := facts("claude", 1500, time.Hour)
	f.IsAgent = true
	c := Assess(f)
	if c.State != StateUseful {
		t.Errorf("coding agent classified %q: %v", c.State, c.Evidence)
	}
}

// Small processes are not worth a line even when abandoned.
func TestSmallProcessIsNotWorthReporting(t *testing.T) {
	f := facts("helper", 40, time.Hour)
	f.ParentGone = true
	c := Assess(f)
	if c.State != StateUseful {
		t.Errorf("40 MB process classified %q", c.State)
	}
}

// A real orphan: quiet 20 minutes, parent gone, large.
func TestVerifiedOrphanIsAbandoned(t *testing.T) {
	f := facts("opencode.exe", 800, 20*time.Minute)
	f.ParentGone = true
	c := Assess(f)
	if c.State != StateAbandoned {
		t.Errorf("quiet 20m, parent gone: %q, want abandoned: %v", c.State, c.Evidence)
	}
	if c.InterruptionRisk > 0.3 {
		t.Errorf("risk %.2f for a verified orphan", c.InterruptionRisk)
	}
}

// Quiet but with a live parent is suspicious, not abandoned: a session may own it.
func TestQuietButParentAliveIsOnlySuspicious(t *testing.T) {
	c := Assess(facts("node", 900, 20*time.Minute))
	if c.State != StateSuspicious {
		t.Errorf("quiet 20m with a live parent: %q, want suspicious", c.State)
	}
	if c.InterruptionRisk < 0.5 {
		t.Errorf("risk %.2f is too low when a session may still own it",
			c.InterruptionRisk)
	}
}

// The honest sentence when relief cannot cover the gap.
func TestInsufficientReliefSaysSo(t *testing.T) {
	f := facts("leftover-build", 300, 20*time.Minute)
	f.ParentGone = true
	c := Assess(f)
	r := &Recommendation{
		Severity:   "Prevent",
		Shortfall:  2 << 30, // 2 GB
		Candidates: []Candidate{c},
	}
	r.TotalRelief = c.ExpectedReliefBytes
	txt := r.Text()
	if !strings.Contains(txt, "will NOT cover") {
		t.Errorf("insufficient relief not stated:\n%s", txt)
	}
	if !strings.Contains(txt, "2048 MB") && !strings.Contains(txt, "2 GB") {
		t.Errorf("the gap is not quantified:\n%s", txt)
	}
}

// And when it does cover it, say that instead.
func TestSufficientReliefSaysSo(t *testing.T) {
	f := facts("leftover-build", 3000, 20*time.Minute)
	f.ParentGone = true
	c := Assess(f)
	r := &Recommendation{Severity: "Prevent", Shortfall: 2 << 30, Candidates: []Candidate{c}}
	r.TotalRelief = c.ExpectedReliefBytes
	if !strings.Contains(r.Text(), "covers it") {
		t.Errorf("sufficient relief not stated:\n%s", r.Text())
	}
}

// With nothing releasable, say that plainly rather than staying silent.
func TestNothingReleasableIsStated(t *testing.T) {
	r := &Recommendation{Severity: "Prevent", Shortfall: 2 << 30}
	txt := r.Text()
	if !strings.Contains(txt, "nothing can be released safely") {
		t.Errorf("empty recommendation did not say why:\n%s", txt)
	}
}

// Every candidate carries evidence. A number without a reason is a claim.
func TestCandidatesAlwaysCarryEvidence(t *testing.T) {
	cases := []GroupFacts{
		facts("big-idle", 1000, 20*time.Minute),
		facts("working", 1000, time.Second),
		facts("tiny", 10, time.Hour),
	}
	for _, f := range cases {
		c := Assess(f)
		if len(c.Evidence) == 0 {
			t.Errorf("%s: candidate has no evidence", f.Group)
		}
	}
}

// Rank prefers relief per unit of risk.
func TestRankPrefersReliefPerRisk(t *testing.T) {
	cheap := Candidate{Group: "cheap", ExpectedReliefBytes: 500 << 20,
		InterruptionRisk: 0.1, AbandonmentConfidence: 0.9}
	risky := Candidate{Group: "risky", ExpectedReliefBytes: 600 << 20,
		InterruptionRisk: 0.9, AbandonmentConfidence: 0.6}
	cs := []Candidate{risky, cheap}
	Rank(cs)
	if cs[0].Group != "cheap" {
		t.Errorf("ranked %s first; relief per risk should favour cheap", cs[0].Group)
	}
}

// The gate that stops the tool narrating work: growth alone is not news.
func TestGrowthAloneIsNotRecommended(t *testing.T) {
	if ShouldRecommend("leak_suspicion", true, false) {
		t.Error("a growing group with no releasable candidate was recommended")
	}
	if !ShouldRecommend("leak_suspicion", true, true) {
		t.Error("a real candidate was not recommended")
	}
}

// The case that motivated this: three rust-analyzer processes holding 2.7 GB
// with zero CPU for two days, inside a `claude` group that was excluded
// wholesale because the agent itself is protected.
func TestAbandonedChildInsideProtectedGroupIsFound(t *testing.T) {
	f := GroupFacts{
		Group:     "claude",
		IsAgent:   true, // the group is protected
		Footprint: 4000 << 20,
		Members: []Member{
			{Pid: 11265, Exe: "rust-analyzer", Footprint: 1017 << 20,
				QuietFor: 48 * time.Hour, ParentAlive: true},
			{Pid: 21432, Exe: "rust-analyzer", Footprint: 859 << 20,
				QuietFor: 48 * time.Hour, ParentAlive: true},
			{Pid: 37060, Exe: "rust-analyzer", Footprint: 821 << 20,
				QuietFor: 13 * time.Hour, ParentAlive: true},
		},
	}
	// The group itself is still excluded.
	if g := Assess(f); g.State != StateUseful {
		t.Errorf("group classified %q; the agent itself must stay protected", g.State)
	}
	// But its idle children are candidates.
	cs := AssessMembers(f)
	if len(cs) != 3 {
		t.Fatalf("%d candidates, want 3 — the group-level exclusion swallowed them", len(cs))
	}
	total := int64(0)
	for _, c := range cs {
		total += c.ExpectedReliefBytes
		if len(c.Evidence) == 0 {
			t.Errorf("%s: no evidence", c.Group)
		}
	}
	if total < 2<<30 {
		t.Errorf("total relief %d MB, want ~2700", total>>20)
	}
}

// A working member is never a candidate, however large.
func TestBusyMemberIsNotACandidate(t *testing.T) {
	f := GroupFacts{Group: "claude", Members: []Member{
		{Pid: 1, Exe: "rust-analyzer", Footprint: 2000 << 20,
			QuietFor: 2 * time.Second, ParentAlive: true},
	}}
	if cs := AssessMembers(f); len(cs) != 0 {
		t.Errorf("a process using CPU 2s ago produced %d candidates", len(cs))
	}
}

// Small members are ignored even when idle.
func TestSmallIdleMemberIgnored(t *testing.T) {
	f := GroupFacts{Group: "claude", Members: []Member{
		{Pid: 1, Exe: "helper", Footprint: 40 << 20,
			QuietFor: 2 * time.Hour, ParentAlive: false},
	}}
	if cs := AssessMembers(f); len(cs) != 0 {
		t.Errorf("40 MB idle process produced a candidate")
	}
}

// An orphaned member (parent gone, quiet long) is abandoned.
func TestOrphanedMemberIsAbandoned(t *testing.T) {
	f := GroupFacts{Group: "x", Members: []Member{
		{Pid: 9, Exe: "build", Footprint: 500 << 20,
			QuietFor: 30 * time.Minute, ParentAlive: false},
	}}
	cs := AssessMembers(f)
	if len(cs) != 1 || cs[0].State != StateAbandoned {
		t.Errorf("orphaned idle member: %+v", cs)
	}
}
