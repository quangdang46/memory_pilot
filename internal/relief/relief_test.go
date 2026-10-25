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
	if ShouldRecommend("leak_suspicion", false, false) {
		t.Error("a growing group with no releasable candidate was recommended")
	}
	if !ShouldRecommend("leak_suspicion", false, true) {
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
			QuietFor: 2 * time.Hour, ParentAlive: false, ParentGone: true},
	}}
	if cs := AssessMembers(f); len(cs) != 0 {
		t.Errorf("40 MB idle process produced a candidate")
	}
}

// An orphaned member (parent gone, quiet long) is abandoned.
func TestOrphanedMemberIsAbandoned(t *testing.T) {
	f := GroupFacts{Group: "x", Members: []Member{
		{Pid: 9, Exe: "build", Footprint: 500 << 20,
			QuietFor: 30 * time.Minute, ParentAlive: false, ParentGone: true},
	}}
	cs := AssessMembers(f)
	if len(cs) != 1 || cs[0].State != StateAbandoned {
		t.Errorf("orphaned idle member: %+v", cs)
	}
}

// The recommendation must be actionable even when nothing is verified
// abandoned. "3 candidates below the bar" is the non-answer this tier replaced:
// an idle process holding 2.7 GB is the only thing on the machine the owner can
// act on, and the sentence must say what closing it returns.
func TestIdleProcessesAreNamedWithTheirRelief(t *testing.T) {
	f := GroupFacts{Group: "claude", IsAgent: true, Members: []Member{
		{Pid: 11265, Exe: "rust-analyzer", Footprint: 1017 << 20,
			QuietFor: 48 * time.Hour, ParentAlive: true},
		{Pid: 21432, Exe: "rust-analyzer", Footprint: 859 << 20,
			QuietFor: 48 * time.Hour, ParentAlive: true},
		{Pid: 37060, Exe: "rust-analyzer", Footprint: 821 << 20,
			QuietFor: 13 * time.Hour, ParentAlive: true},
		{Pid: 96329, Exe: "node", Footprint: 1803 << 20,
			QuietFor: time.Second, ParentAlive: true}, // busy: excluded
	}}
	cs := AssessMembers(f)
	Rank(cs)
	if len(cs) != 3 {
		t.Fatalf("%d candidates, want 3 (the busy node must not be one)", len(cs))
	}
	r := &Recommendation{Severity: "Prevent", Shortfall: 2 << 30, Candidates: cs}
	for _, c := range cs {
		r.TotalRelief += c.ExpectedReliefBytes
	}
	txt := r.Text()
	for _, want := range []string{"rust-analyzer", "2697", "would cover it"} {
		if !strings.Contains(txt, want) {
			t.Errorf("recommendation does not mention %q:\n%s", want, txt)
		}
	}
	if strings.Contains(txt, "node (pid 96329)") {
		t.Errorf("a busy process was named as releasable:\n%s", txt)
	}
}

// The first live L1 run (23:51) named Finder, the VM and every Chrome helper as
// releasable. Two separate causes: protected processes were eligible at all, and
// a process launchd started directly was read as "parent gone".
func TestSystemProcessesAreNeverCandidates(t *testing.T) {
	group := GroupFacts{Members: []Member{
		{Pid: 1, Exe: "Finder", Footprint: 300 << 20, QuietFor: time.Hour,
			ParentGone: true},
		{Pid: 2, Exe: "com.apple.Virtualization.VirtualMachine",
			Footprint: 1796 << 20, QuietFor: time.Hour, ParentGone: true},
		{Pid: 3, Exe: "Google Chrome Helper (Renderer)",
			Footprint: 900 << 20, QuietFor: time.Hour, ParentGone: true},
		{Pid: 4, Exe: "Code Helper (Renderer)", Footprint: 700 << 20,
			QuietFor: time.Hour, ParentGone: true},
		{Pid: 5, Exe: "WindowServer", Footprint: 800 << 20, QuietFor: time.Hour,
			ParentGone: true},
	}}
	if cs := AssessMembers(group); len(cs) != 0 {
		for _, c := range cs {
			t.Errorf("named %q as releasable", c.Group)
		}
	}
}

// An unobserved parent is not a dead parent. 25% of this machine is outside
// coverage, and calling that "parent gone" is the guess that produced the
// dangerous list.
func TestUnobservedParentIsNotAbandonment(t *testing.T) {
	f := GroupFacts{Members: []Member{
		{Pid: 9, Exe: "some-build", Footprint: 600 << 20,
			QuietFor: 30 * time.Minute}, // neither ParentAlive nor ParentGone
	}}
	cs := AssessMembers(f)
	if len(cs) != 1 {
		t.Fatalf("%d candidates, want 1", len(cs))
	}
	if cs[0].State == StateAbandoned {
		t.Errorf("unobserved parent read as abandoned: %v", cs[0].Evidence)
	}
	joined := strings.Join(cs[0].Evidence, " ")
	if !strings.Contains(joined, "not observed") {
		t.Errorf("evidence does not say the parent is unknown: %v", cs[0].Evidence)
	}
}

// The warning board was 98 advisories in 45 minutes at Normal pressure. Growth
// is the cost of work unless the machine is actually short of memory.
func TestAdvisoryNeedsPressureOrACandidate(t *testing.T) {
	if ShouldRecommend("leak_suspicion", false, false) {
		t.Error("growth advised at Normal pressure with nothing releasable")
	}
	if ShouldRecommend("subprocess_accumulation", false, false) {
		t.Error("accumulation advised at Normal pressure with nothing releasable")
	}
	if !ShouldRecommend("leak_suspicion", true, false) {
		t.Error("a leak suspicion at pressure was not advised")
	}
	if !ShouldRecommend("workload_growth", false, true) {
		t.Error("a releasable candidate at Normal pressure was not advised")
	}
	// Even under pressure, growth explained by work is not a decision the
	// owner can make; the relief path covers what they can act on.
	if ShouldRecommend("workload_growth", true, false) {
		t.Error("workload growth advised under pressure")
	}
}

// A recommendation that lists everything is a warning board with extra
// indentation. The count and total stay exact; the detail is capped.
func TestRecommendationNamesOnlyTheFirstFew(t *testing.T) {
	f := GroupFacts{Group: "x"}
	for i := 0; i < 30; i++ {
		f.Members = append(f.Members, Member{Pid: int32(i + 1), Exe: "worker",
			Footprint: 200 << 20, QuietFor: 30 * time.Minute, ParentGone: true})
	}
	cs := AssessMembers(f)
	Rank(cs)
	r := &Recommendation{Severity: "Warn", Shortfall: 1 << 30, Candidates: cs}
	for _, c := range cs {
		if c.State == StateAbandoned {
			r.TotalRelief += c.ExpectedReliefBytes
		}
	}
	txt := r.Text()
	if n := strings.Count(txt, "\n  worker (pid "); n != MaxNamed {
		t.Errorf("named %d processes, want %d", n, MaxNamed)
	}
	if !strings.Contains(txt, "and 22 more") {
		t.Errorf("the unnamed 22 are not accounted for:\n%s", txt)
	}
	if !strings.Contains(txt, "closing 30 abandoned") {
		t.Errorf("the count was truncated with the list:\n%s", txt)
	}
}

// "Covers it" is a claim about relief. Relief from a process nobody has
// verified as abandoned is an upper bound, and the design says never to
// present one as the other.
func TestUnverifiedReliefIsStatedAsAnUpperBound(t *testing.T) {
	f := facts("big-idle", 3000, 20*time.Minute) // parent alive, not abandoned
	c := Assess(f)
	r := &Recommendation{Severity: "Warn", Shortfall: 2 << 30, Candidates: []Candidate{c}}
	txt := r.Text()
	if !strings.Contains(txt, "at most") {
		t.Errorf("unverified relief stated as certain:\n%s", txt)
	}
	if strings.Contains(txt, "— this covers it") {
		t.Errorf("unverified relief claims to cover the gap:\n%s", txt)
	}
}
