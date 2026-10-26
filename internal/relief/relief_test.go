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

// Nothing releasable and a real shortfall: say exactly that. This is the
// sentence a freshly restarted daemon owes the owner, instead of naming
// processes it has not finished watching.
func TestNothingReleasableWithShortfallIsHonest(t *testing.T) {
	r := &Recommendation{Severity: "Warn", Shortfall: 2048 << 20}
	txt := r.Text()
	if !strings.Contains(txt, "2048 MB") {
		t.Errorf("the gap is not quantified:\n%s", txt)
	}
	if !strings.Contains(txt, "nothing can be released safely") {
		t.Errorf("the absence of a release is not stated:\n%s", txt)
	}
	if strings.Contains(txt, "closing") {
		t.Errorf("an empty recommendation talks about closing things:\n%s", txt)
	}
}

// A quiet period longer than the observation window is a statement about the
// observer. Measured: seven minutes after a restart, 43 processes were reported
// "idle for 6m45s" — the harness's runtime, dressed as their silence.
func TestQuietBeyondObservationIsNotAClaim(t *testing.T) {
	f := GroupFacts{
		ObservedFor: 7 * time.Minute,
		Members: []Member{
			// Claims 19 hours of silence from a sampler that ran for 7 minutes.
			{Pid: 1, Exe: "rust-analyzer", Footprint: 900 << 20,
				QuietFor: 19 * time.Hour, ParentAlive: true},
			// Genuinely silent for longer than we watched: same problem.
			{Pid: 2, Exe: "node", Footprint: 800 << 20,
				QuietFor: 8 * time.Minute, ParentAlive: true},
			// Within the window and past the suspicious bar: a real candidate.
			{Pid: 3, Exe: "leftover-build", Footprint: 700 << 20,
				QuietFor: 6 * time.Minute, ParentAlive: true},
		},
	}
	cs := AssessMembers(f)
	if len(cs) != 1 {
		t.Fatalf("%d candidates, want only the one within the observation window", len(cs))
	}
	if !strings.Contains(cs[0].Group, "leftover-build") {
		t.Errorf("wrong candidate survived: %s", cs[0].Group)
	}
}

// And with no bound given, the old behaviour is unchanged: callers that know
// their own observation window are not second-guessed.
func TestUnboundedObservationKeepsCandidates(t *testing.T) {
	f := GroupFacts{Members: []Member{
		{Pid: 1, Exe: "build", Footprint: 900 << 20, QuietFor: 19 * time.Hour,
			ParentGone: true},
	}}
	if cs := AssessMembers(f); len(cs) != 1 {
		t.Errorf("%d candidates with no bound, want 1", len(cs))
	}
}

// The live orphan: a child whose parent exited, holding 900 MB, running for
// 45 minutes with 0.41 s of CPU in its whole life. The sampler had only been
// watching for 12 minutes, so the quiet clock could not carry the claim — but
// the process's own accounting can, and refusing it would mean never naming a
// real orphan that predates the daemon.
func TestLifetimeIdleEvidenceNamesARealOrphan(t *testing.T) {
	f := GroupFacts{
		ObservedFor: 12 * time.Minute, // harness started long after the process
		Members: []Member{
			{Pid: 48398, Exe: "orphanprobe", Footprint: 904 << 20,
				QuietFor: 45 * time.Minute, CpuTotalS: 0.41, AgeS: 2700,
				ParentGone: true},
		},
	}
	cs := AssessMembers(f)
	if len(cs) != 1 {
		t.Fatalf("%d candidates, want the real orphan", len(cs))
	}
	if cs[0].State != StateAbandoned {
		t.Errorf("orphan with 0.41 s of CPU in 45 min classified %q: %v",
			cs[0].State, cs[0].Evidence)
	}
	joined := strings.Join(cs[0].Evidence, " ")
	if !strings.Contains(joined, "idle by its own record") {
		t.Errorf("evidence does not say why the quiet claim holds: %v", cs[0].Evidence)
	}
}

// The same shape without the lifetime evidence is a guess, and stays out.
func TestBusyLifetimeIsNotIdle(t *testing.T) {
	f := GroupFacts{
		ObservedFor: 12 * time.Minute,
		Members: []Member{
			// 400 s of CPU in 45 minutes: working, whatever the quiet clock says.
			{Pid: 1, Exe: "node", Footprint: 900 << 20,
				QuietFor: 45 * time.Minute, CpuTotalS: 400, AgeS: 2700,
				ParentAlive: true},
		},
	}
	if cs := AssessMembers(f); len(cs) != 0 {
		t.Errorf("a process with 400 s of CPU was named releasable: %v", cs)
	}
}

// A process younger than an hour has not had the chance to prove idleness by
// lifetime CPU, however small the number.
func TestYoungProcessCannotClaimLifetimeIdle(t *testing.T) {
	f := GroupFacts{
		ObservedFor: 12 * time.Minute,
		Members: []Member{
			{Pid: 2, Exe: "build", Footprint: 900 << 20, QuietFor: 45 * time.Minute,
				CpuTotalS: 0.1, AgeS: 300, ParentAlive: true},
		},
	}
	if cs := AssessMembers(f); len(cs) != 0 {
		t.Errorf("a 5-minute-old process claimed lifetime idleness: %v", cs)
	}
}

// The worst false positive the engine can produce: offering to close the agent
// the owner is talking to. Measured live at 06:50 — three `claude` processes
// were named "2.1.310 (pid 47742)" because the group anchor was a session id,
// not the executable, so the group-level IsAgent flag never applied.
func TestCodingAgentsAreNeverNamed(t *testing.T) {
	f := GroupFacts{ObservedFor: 10 * time.Minute, Members: []Member{
		{Pid: 47742, Exe: "claude", Footprint: 419 << 20, QuietFor: 15 * time.Second,
			CpuTotalS: 21.8, AgeS: 28800, ParentAlive: true},
		{Pid: 58684, Exe: "claude", Footprint: 397 << 20, QuietFor: 15 * time.Second,
			CpuTotalS: 15.3, AgeS: 28700, ParentAlive: true},
		{Pid: 1, Exe: "opencode.exe", Footprint: 512 << 20, QuietFor: 15 * time.Second,
			CpuTotalS: 0.4, AgeS: 9000, ParentAlive: false, ParentGone: true},
		{Pid: 2, Exe: "codex", Footprint: 300 << 20, QuietFor: time.Hour,
			CpuTotalS: 1, AgeS: 9000, ParentGone: true},
	}}
	if cs := AssessMembers(f); len(cs) != 0 {
		for _, c := range cs {
			t.Errorf("named a coding agent as releasable: %s", c.Group)
		}
	}
}

// Lifetime evidence must not be reported as a quiet period the sampler never
// saw. The old branch printed "done no work for 15s" beside "idle by its own
// record for 70 hours" — two different clocks in one sentence.
func TestLifetimeEvidenceDoesNotMentionTheWindowClock(t *testing.T) {
	f := GroupFacts{ObservedFor: 10 * time.Minute, Members: []Member{
		{Pid: 1, Exe: "rust-analyzer", Footprint: 914 << 20, QuietFor: 15 * time.Second,
			CpuTotalS: 7.9, AgeS: 70 * 3600, ParentAlive: true},
	}}
	cs := AssessMembers(f)
	if len(cs) != 1 {
		t.Fatalf("%d candidates, want 1", len(cs))
	}
	joined := strings.Join(cs[0].Evidence, " ")
	if strings.Contains(joined, "done no work for 15") {
		t.Errorf("evidence quotes a 15 s window beside a 70 h lifetime claim:\n%s", joined)
	}
	if !strings.Contains(joined, "whole life") {
		t.Errorf("evidence does not state the lifetime basis:\n%s", joined)
	}
}

// Executable is a full path on macOS. A protection list matched against the
// whole string never fires — measured live, three `claude` processes at
// /Users/.../.local/bin/claude were named as releasable.
func TestProtectionMatchesFullPaths(t *testing.T) {
	cases := []string{
		"/Users/tranquangdang21/.local/bin/claude",
		"/Applications/Visual Studio Code.app/Contents/MacOS/Code Helper (Renderer)",
		"/System/Library/CoreServices/Finder.app/Contents/MacOS/Finder",
		"/Library/Apple/System/Library/CoreServices/WindowServer",
	}
	for _, exe := range cases {
		if !isProtected(exe) && !isAgent(exe) {
			t.Errorf("%q would be named as releasable", exe)
		}
	}
	// A genuine orphan path stays eligible.
	if isProtected("/tmp/orphanprobe") || isAgent("/tmp/orphanprobe") {
		t.Error("a plain probe binary was treated as protected")
	}
}

// Claude Code keeps its builds at ~/.local/share/claude/versions/<version>, so
// the running image's base name is "2.1.310". A base-name check reports the
// agent as an anonymous process; measured live, three were named releasable.
func TestAgentMatchedByPathSegment(t *testing.T) {
	for _, exe := range []string{
		"/Users/tranquangdang21/.local/share/claude/versions/2.1.310",
		"/opt/homebrew/lib/node_modules/@openai/codex/bin/codex.js",
		"/Applications/ZCode.app/Contents/Frameworks/ZCode Helper.app/Contents/MacOS/ZCode Helper",
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"/Applications/Visual Studio Code.app/Contents/MacOS/Electron",
	} {
		if !isAgent(exe) && !isProtected(exe) {
			t.Errorf("%q would be named as releasable", exe)
		}
	}
	// The genuine orphan must survive every list.
	if isAgent("/private/tmp/orphanprobe") || isProtected("/private/tmp/orphanprobe") {
		t.Error("the probe binary was excluded by a protection rule")
	}
}

// The evidence that separates an orphan from a launchd service. macOS reparents
// both to pid 1, so ppid is not enough on either side: measured, the first live
// run called every service abandoned, and a real orphan sat at
// parentGone=false for its whole life.
func TestOrphanAndServiceAreDistinguished(t *testing.T) {
	f := GroupFacts{ObservedFor: 10 * time.Minute, Members: []Member{
		// ppid 1 and launchd does not manage it: abandoned.
		{Pid: 47396, Exe: "/private/tmp/orphanprobe", Footprint: 905 << 20,
			QuietFor: 16 * time.Minute, CpuTotalS: 0.01, AgeS: 980,
			ParentGone: true},
		// ppid 1 and launchd manages it: a service, never a candidate.
		{Pid: 952, Exe: "/System/Library/CoreServices/Finder.app/Contents/MacOS/Finder",
			Footprint: 300 << 20, QuietFor: time.Hour, CpuTotalS: 1, AgeS: 90000},
	}}
	cs := AssessMembers(f)
	if len(cs) != 1 {
		t.Fatalf("%d candidates, want only the orphan", len(cs))
	}
	if !strings.Contains(cs[0].Group, "orphanprobe") {
		t.Errorf("wrong process survived: %s", cs[0].Group)
	}
}

// The abandoned line must carry the number it rests on. "Quiet for a while"
// reads as a finding without being checkable — measured, five pnpm processes
// had been silent for 22 hours and the line never said so.
func TestAbandonedLineStatesTheQuietPeriod(t *testing.T) {
	f := GroupFacts{ObservedFor: 24 * time.Hour, Members: []Member{
		{Pid: 64747, Exe: "/opt/node/bin/node", Footprint: 120 << 20,
			QuietFor: 22 * time.Hour, ParentGone: true},
	}}
	cs := AssessMembers(f)
	if len(cs) != 1 {
		t.Fatalf("%d candidates, want 1", len(cs))
	}
	r := &Recommendation{Severity: "Warn", Shortfall: 1 << 30, Candidates: cs}
	for _, c := range cs {
		r.TotalRelief += c.ExpectedReliefBytes
	}
	txt := r.Text()
	if !strings.Contains(txt, "no CPU for 22h0m0s") {
		t.Errorf("the quiet period is not stated:\n%s", txt)
	}
	if strings.Contains(txt, "quiet for a while") {
		t.Errorf("vague phrasing survived:\n%s", txt)
	}
}

// When the claim rests on lifetime CPU rather than the quiet clock, say that
// instead of inventing a duration.
func TestLifetimeClaimDoesNotInventAQuietPeriod(t *testing.T) {
	f := GroupFacts{ObservedFor: 5 * time.Minute, Members: []Member{
		{Pid: 47396, Exe: "/private/tmp/orphanprobe", Footprint: 905 << 20,
			QuietFor: 30 * time.Second, CpuTotalS: 0.01, AgeS: 1800,
			ParentGone: true},
	}}
	cs := AssessMembers(f)
	if len(cs) != 1 {
		t.Fatalf("%d candidates, want 1", len(cs))
	}
	r := &Recommendation{Severity: "Warn", Shortfall: 1 << 30, Candidates: cs}
	for _, c := range cs {
		r.TotalRelief += c.ExpectedReliefBytes
	}
	want := "idle by its own CPU record"
	if !strings.Contains(r.Text(), want) {
		t.Errorf("no %q in:\n%s", want, r.Text())
	}
	if strings.Contains(r.Text(), "no CPU for 30s") {
		t.Errorf("quoted a 30 s window as the basis:\n%s", r.Text())
	}
}
