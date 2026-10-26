// Package relief: the L1 recommendation engine from
// docs/06-problem-first/process-relief-engine.md.
//
// The product exists to keep long-running coding agents alive, not to report
// on memory. The distinction matters: an advisory that says "Code grew
// 3146 MB" tells the owner about their own work and gives them nothing to do.
// What the engine owes them is the sentence in the design document —
//
//	closing these three verified orphans frees ~400 MB; the gap is ~2 GB;
//	this will not suffice
//
// — which is honest about the gap as well as the relief.
//
// This package computes that sentence. It never terminates anything: v1 ships
// detect + recommend only, and ST-1/ST-2 fail the build if a termination
// primitive appears outside tests.
package relief

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"memory_pilot/internal/classify"
)

// Candidate is one process the engine believes could be released, with the
// three numbers the design separates and never merges into one score.
type Candidate struct {
	Group string

	// AbandonmentConfidence [0,1]: how sure we are no live work depends on it.
	AbandonmentConfidence float64
	// InterruptionRisk [0,1]: what breaks if we are wrong. Drives the default
	// (high risk ⇒ recommend only, never act).
	InterruptionRisk float64
	// ExpectedReliefBytes: what closing it would return. Drives priority, and
	// never permission.
	ExpectedReliefBytes int64

	// Evidence is the recorded reason for each number. A candidate without
	// evidence is not a candidate.
	Evidence []string

	// State is useful / suspicious / abandoned. Default is useful: the burden
	// of proof is on moving away from it.
	State string
}

const (
	StateUseful     = "useful"
	StateSuspicious = "suspicious"
	StateAbandoned  = "abandoned"
)

// Evidence bar for `abandoned`, from protection-actions.md §3.3. Every one must
// hold; each is recorded so the owner can check the reasoning.
const (
	// MinQuietForAbandoned is the CPU-quiet period. A process using CPU is
	// working, whatever else is true of it.
	MinQuietForAbandoned = 600 * time.Second
	// MinQuietForSuspicious is the shorter bar for "worth telling the owner
	// about" — still long enough that a process waiting on a network call is
	// not in it.
	MinQuietForSuspicious = 300 * time.Second
	// MinReliefBytes: below this, closing something is a cleanup nicety, not a
	// response to a shortfall.
	MinReliefBytes = 100 << 20
	// MaxQuietClaimS is the longest quiet period a recommendation may rest on.
	// A process's CPU silence before the daemon started is not something it
	// observed, and 25% of this machine is outside sampling coverage at any
	// moment. The daemon passes the longest gap-free run it actually recorded;
	// see GroupFacts.ObservedFor.
	// LifetimeIdleCPUS is the CPU a process may have consumed over its whole
	// life and still count as idle.
	LifetimeIdleCPUS = 60.0
	// LifetimeIdleRate is CPU-seconds per second of life. 2% of one core:
	// nothing is running there. The rate matters more than the total — a
	// process that started five minutes ago has not had the chance to
	// accumulate anything, and an absolute bar would call it idle.
	LifetimeIdleRate = 0.02
	// MinLifetimeIdleS is how long a process must have existed before lifetime
	// CPU says anything at all. Same bar as abandonment: ten minutes.
	MinLifetimeIdleS = 600.0
	// MaxNamed caps how many processes the sentence lists. The count and the
	// total stay exact; the detail does not need to be a wall. Measured in the
	// first live run: 39 candidates, each with two lines of evidence — a
	// "recommendation" that is longer than the warning board it replaced.
	MaxNamed = 8
)

// GroupFacts is what the engine is allowed to know about a group. Everything
// here is observable; nothing is inferred from a name.
type GroupFacts struct {
	Group      string
	Footprint  int64         // bytes this group holds now
	QuietFor   time.Duration // since its members last burned CPU
	ActiveFor  time.Duration // total active time observed
	ParentGone bool          // ancestry terminates at launchd
	IsTerminal bool          // shells, terminals: never a candidate
	IsAgent    bool          // coding agents: never a candidate without opt-in
	CacheRole  bool          // known cache (browser, indexer): retrievable

	// ObservedFor is how long the sampler has watched without a coverage gap.
	// A quiet period longer than this cannot be claimed as observed. Zero means
	// unbounded (callers that already know better).
	ObservedFor time.Duration

	// Members are the group's individual processes. A group's footprint hides
	// the case that matters most: one abandoned member holding gigabytes while
	// the rest work. Measured on the owner's machine — three rust-analyzer
	// processes held 2.7 GB with zero CPU for two days, inside a `claude` group
	// that was excluded wholesale because the agent itself is protected.
	Members []Member
}

// Member is one process inside a group.
type Member struct {
	Pid       int32
	Exe       string
	Footprint int64
	// QuietFor is how long since this process last used CPU.
	QuietFor time.Duration
	// ParentAlive reports whether the process that started it still runs.
	ParentAlive bool
	// CpuTotalS is the CPU this process has consumed in its entire life, and
	// AgeS is how long it has existed. Together they are evidence independent
	// of how long the sampler has been running: a process that has burned
	// 0.4 s of CPU in two days while holding 900 MB is idle by its own
	// accounting, whether or not we were watching when it started.
	CpuTotalS float64
	AgeS      float64

	// ParentGone is true only when this process was observed under a parent
	// that has since exited. A process launchd started directly has no parent
	// in the ordinary sense and is not evidence of anything.
	ParentGone bool
}

// Assess turns facts into a candidate with its three numbers.
//
// The classification is deliberately conservative. Abandoned requires quiet
// for ten minutes, a dead parent, and no terminal/agent involvement; anything
// less is useful or suspicious. A process using CPU is working, and its
// footprint is the cost of that work, not a fault.
func Assess(f GroupFacts) Candidate {
	c := Candidate{
		Group:                 f.Group,
		InterruptionRisk:      1.0,
		AbandonmentConfidence: 0,
		State:                 StateUseful,
	}
	if f.Footprint < MinReliefBytes {
		c.Evidence = append(c.Evidence,
			fmt.Sprintf("holds %d MB, below the %d MB floor — nothing to gain",
				f.Footprint>>20, MinReliefBytes>>20))
		return c
	}

	if f.IsTerminal {
		c.Evidence = append(c.Evidence,
			"terminal: its children are work the owner started by hand")
		return c
	}
	if f.IsAgent {
		c.Evidence = append(c.Evidence,
			"coding agent: closing it destroys in-flight work with no resume path")
		return c
	}
	if f.QuietFor < MinQuietForSuspicious {
		c.Evidence = append(c.Evidence,
			fmt.Sprintf("active %s ago — this is work, not a fault", f.QuietFor.Round(time.Second)))
		return c
	}

	// Risk falls with how retrievable the memory is. A cache can be re-read;
	// a working set cannot.
	c.InterruptionRisk = 0.6
	if f.CacheRole {
		c.InterruptionRisk = 0.3
		c.Evidence = append(c.Evidence, "known cache role: memory is retrievable")
	}

	if f.QuietFor < MinQuietForAbandoned {
		c.State = StateSuspicious
		c.AbandonmentConfidence = 0.4
		c.Evidence = append(c.Evidence,
			fmt.Sprintf("quiet %s, short of the %s bar for abandoned",
				f.QuietFor.Round(time.Second), MinQuietForAbandoned))
		c.ExpectedReliefBytes = f.Footprint
		return c
	}

	c.AbandonmentConfidence = 0.6
	c.Evidence = append(c.Evidence,
		fmt.Sprintf("quiet %s (>= %s)",
			f.QuietFor.Round(time.Second), MinQuietForAbandoned))
	if f.ParentGone {
		c.AbandonmentConfidence = 0.85
		c.Evidence = append(c.Evidence, "parent gone: ancestry terminates at launchd")
		c.InterruptionRisk = 0.15
		if f.CacheRole {
			c.InterruptionRisk = 0.1
		}
	} else {
		c.Evidence = append(c.Evidence,
			"parent alive: a live session may still own this")
		c.InterruptionRisk = 0.7
	}

	if c.AbandonmentConfidence >= 0.8 && c.InterruptionRisk <= 0.3 {
		c.State = StateAbandoned
	} else {
		c.State = StateSuspicious
	}
	c.ExpectedReliefBytes = f.Footprint
	return c
}

// protectedExe lists processes that must never be named as releasable, whatever
// their footprint or idle time. These are not heuristics: closing any of them
// breaks the session, the desktop, or the system.
//
// Measured in the first live L1 run (23:51): 39 candidates including Finder,
// com.apple.Virtualization.VirtualMachine, Google Chrome Helper and Code Helper.
// A recommendation that tells the owner to close Finder is not a
// recommendation, it is a hazard.
var protectedExe = map[string]bool{
	// the desktop and window server
	"Finder": true, "Dock": true, "WindowManager": true, "SystemUIServer": true,
	// virtualisation: closing it destroys a running VM
	"com.apple.Virtualization.VirtualMachine": true,
	"VirtualMachine": true,
	// system services that appear as ordinary processes
	"launchd": true, "cfprefsd": true, "mds": true, "mds_stores": true,
	"WindowServer": true, "loginwindow": true, "coreaudiod": true,
	// the daemon itself
	"mempilotd": true,
}

// protectedPrefix covers families whose exact names vary by build.
// agentExe is the set of coding agents. A group-level IsAgent flag is not
// enough: measured live, three `claude` processes were named "2.1.310 (pid
// 47742)" because the group anchor was a session id, not the executable. That
// is the worst false positive the engine can produce — it offers to close the
// agent the owner is talking to.
var agentExe = map[string]bool{
	"claude": true, "codex": true, "cursor-agent": true, "opencode": true,
	"opencode.exe": true, "aider": true, "goose": true, "amp": true,
	"gemini": true, "copilot": true, "continue": true, "zcode-cli": true,
	"codeium": true, "windsurf": true, "plandex": true, "crush": true,
}

func isAgent(exe string) bool {
	base := exeBase(exe)
	return agentExe[exe] || agentExe[base]
}

var protectedPrefix = []string{
	"com.apple.",      // Apple system services and frameworks
	"Google Chrome H", // helpers belong to their browser, not to the user
	"Code Helper",     // same for the IDE
	"Electron Helper",
	"VTDecoder",
}

// exeBase reduces an image path to its file name. Executable is a full path on
// macOS ("/Users/me/.local/bin/claude"), so every list here is matched against
// the base name; matching the whole path would never hit.
func exeBase(exe string) string {
	if i := strings.LastIndexByte(exe, '/'); i >= 0 {
		return exe[i+1:]
	}
	return exe
}

func isProtected(exe string) bool {
	base := exeBase(exe)
	if protectedExe[exe] || protectedExe[base] {
		return true
	}
	for _, p := range protectedPrefix {
		if strings.HasPrefix(exe, p) || strings.HasPrefix(base, p) {
			return true
		}
	}
	return false
}

// AssessMembers returns candidates for individual processes inside a group,
// which is where the actionable cases live.
//
// A group-level verdict cannot express "one of these eleven processes is
// abandoned and holds 2.7 GB". The group is either a terminal (excluded), an
// agent (excluded), or a workload whose total is the cost of doing its job.
// Individual members are judged on their own evidence.
func AssessMembers(f GroupFacts) []Candidate {
	var out []Candidate
	for _, m := range f.Members {
		if m.Footprint < MinReliefBytes {
			continue
		}
		// Two independent kinds of evidence, either of which can show the
		// process is not working: the quiet clock (what this sampler saw) and
		// the process's own lifetime accounting (true regardless of when the
		// sampler started).
		lifetimeIdle := m.CpuTotalS <= LifetimeIdleCPUS &&
			m.AgeS >= MinLifetimeIdleS &&
			m.CpuTotalS/m.AgeS <= LifetimeIdleRate
		if m.QuietFor < MinQuietForSuspicious && !lifetimeIdle {
			continue // it is working
		}
		// Measured: seven minutes after a restart, 43 processes were reported
		// "idle for 6m45s" — the harness's own runtime. A quiet period longer
		// than the observation window is a statement about the observer, not
		// the process.
		// A quiet period longer than the observation window is not a claim we
		// can make from the quiet clock. It may still be a claim we can make
		// from the process's own CPU accounting, which is why this only
		// suppresses the first kind of evidence.
		observedQuiet := m.QuietFor
		if f.ObservedFor > 0 && m.QuietFor > f.ObservedFor && !lifetimeIdle {
			continue
		}
		if isProtected(m.Exe) || isAgent(m.Exe) {
			continue // never name a process whose loss breaks the session
		}
		c := Candidate{
			Group:            fmt.Sprintf("%s (pid %d)", m.Exe, m.Pid),
			InterruptionRisk: 0.8,
			State:            StateSuspicious,
		}
		if observedQuiet > f.ObservedFor && f.ObservedFor > 0 && lifetimeIdle {
			c.Evidence = append(c.Evidence,
				fmt.Sprintf("holds %d MB, %.1f s of CPU in %s of life — idle by its own record",
					m.Footprint>>20, m.CpuTotalS,
					time.Duration(m.AgeS)*time.Second))
		} else {
			c.Evidence = append(c.Evidence,
				fmt.Sprintf("holds %d MB, no CPU for %s",
					m.Footprint>>20, m.QuietFor.Round(time.Second)))
		}

		// Three parent states, and only one of them is evidence:
		//   ParentGone  — was under a parent, parent exited, macOS reparented
		//                 it to launchd. Abandoned.
		//   ParentAlive — the parent still runs, so a session may own it.
		//   neither     — the parent is outside coverage (75% on this machine).
		//                 Unknown, and stated as unknown.
		quiet := m.QuietFor
		if lifetimeIdle && quiet < MinQuietForAbandoned {
			quiet = MinQuietForAbandoned // lifetime evidence clears the bar
		}
		if lifetimeIdle && quiet < MinQuietForSuspicious {
			quiet = MinQuietForSuspicious
		}
		switch {
		case m.ParentGone && quiet >= MinQuietForAbandoned:
			c.State = StateAbandoned
			c.AbandonmentConfidence = 0.85
			c.InterruptionRisk = 0.15
			c.Evidence = append(c.Evidence, "parent gone: nothing is waiting on it")
		case m.ParentGone:
			c.State = StateSuspicious
			c.AbandonmentConfidence = 0.5
			c.InterruptionRisk = 0.3
			c.Evidence = append(c.Evidence,
				fmt.Sprintf("parent gone, quiet %s — short of the %s bar",
					m.QuietFor.Round(time.Second), MinQuietForAbandoned))
		case quiet >= MinQuietForAbandoned && m.ParentAlive && !lifetimeIdle:
			// A live parent that is not using it. This is the rust-analyzer
			// case: the session that spawned it is still open but idle, and the
			// child has held gigabytes without doing anything for hours.
			c.State = StateSuspicious
			c.AbandonmentConfidence = 0.55
			c.InterruptionRisk = 0.45
			c.Evidence = append(c.Evidence,
				"parent still running, but this process has done no work for "+
					m.QuietFor.Round(time.Second).String()+" — closing it loses nothing "+
					"it is currently doing, though the parent may restart it")
		case quiet >= MinQuietForAbandoned && !lifetimeIdle:
			// The parent is not in coverage, so whether anything owns this
			// process is unknown. Saying "parent gone" here would be a guess,
			// and the first live run made exactly that guess about Finder.
			c.State = StateSuspicious
			c.AbandonmentConfidence = 0.45
			c.InterruptionRisk = 0.6
			c.Evidence = append(c.Evidence,
				"parent not observed — it may have exited or may be outside "+
					"sampling coverage; this is not evidence either way")
		case lifetimeIdle && m.ParentAlive:
			// A live parent that is not using it, proven by the process's own
			// accounting rather than by a window this sampler may have missed.
			c.State = StateSuspicious
			c.AbandonmentConfidence = 0.55
			c.InterruptionRisk = 0.45
			c.Evidence = append(c.Evidence,
				"the parent is running but has not used this process in its "+
					"whole life; closing it loses nothing it is currently doing, "+
					"though the parent may restart it")
		default:
			c.AbandonmentConfidence = 0.3
		}
		c.ExpectedReliefBytes = m.Footprint
		out = append(out, c)
	}
	return out
}

// Recommendation is the sentence the owner reads. It carries the relief, the
// gap, and — when the relief is insufficient — says so, because a kill list
// that "solves" an unfixable gap is the most dangerous output the module can
// produce (process-relief-engine.md §3).
type Recommendation struct {
	At         time.Time
	Severity   string // the pressure level this was computed at
	Shortfall  int64  // bytes the machine is short, 0 when unknown
	Candidates []Candidate

	TotalRelief int64
}

// Sufficient reports whether the candidates would close the shortfall.
func (r *Recommendation) Sufficient() bool {
	if r.Shortfall <= 0 {
		return true
	}
	return r.TotalRelief >= r.Shortfall
}

// Text renders the recommendation. It is the whole output of L1.
func (r *Recommendation) Text() string {
	if len(r.Candidates) == 0 {
		if r.Shortfall > 0 {
			return fmt.Sprintf(
				"short %d MB and nothing can be released safely — no process meets "+
					"the bar for being closed (quiet, orphaned, not a terminal or agent)",
				r.Shortfall>>20)
		}
		return "nothing to release"
	}
	relief := int64(0)
	var abandoned []Candidate
	for i, c := range r.Candidates {
		if c.State == StateAbandoned {
			relief += c.ExpectedReliefBytes
			if i < MaxNamed { // already ranked; the rest are counted, not listed
				abandoned = append(abandoned, c)
			}
		}
	}
	named := 0
	for _, c := range r.Candidates {
		if c.State == StateAbandoned {
			named++
		}
	}
	if len(abandoned) == 0 {
		// Nothing is abandoned outright, but idle processes holding gigabytes
		// are still the only actionable thing on the machine. Say what they
		// are and what closing them would return — "below the bar" without a
		// number and a next step is the same non-answer this tier replaced.
		total := int64(0)
		s := fmt.Sprintf("%d process(es) are idle but not verified abandoned:\n",
			len(r.Candidates))
		for i, c := range r.Candidates {
			total += c.ExpectedReliefBytes
			if i >= MaxNamed {
				continue
			}
			s += fmt.Sprintf("  %s (%d MB)\n", c.Group, c.FootprintMB())
			for _, e := range c.Evidence {
				s += fmt.Sprintf("      %s\n", e)
			}
		}
		if len(r.Candidates) > MaxNamed {
			s += fmt.Sprintf("  ... and %d more\n", len(r.Candidates)-MaxNamed)
		}
		// "Covers it" is a claim about relief, and relief from a process we
		// have not verified as abandoned is a guess. The design says never to
		// present one as the other, so this branch states the arithmetic and
		// leaves the decision where it belongs.
		s += fmt.Sprintf("closing all of them would return ~%d MB at most", total>>20)
		if r.Shortfall > 0 {
			if total >= r.Shortfall {
				s += fmt.Sprintf("; the shortfall is %d MB, so that would cover it "+
					"if they are truly abandoned", r.Shortfall>>20)
			} else {
				s += fmt.Sprintf("; the shortfall is %d MB — even closing all of "+
					"them would NOT cover it", r.Shortfall>>20)
			}
		}
		return s
	}
	s := fmt.Sprintf("closing %d abandoned workload(s) would return ~%d MB",
		named, relief>>20)
	for _, c := range abandoned {
		s += fmt.Sprintf("\n  %s: %d MB, quiet for a while, parent gone", c.Group, c.FootprintMB())
	}
	if named > len(abandoned) {
		s += fmt.Sprintf("\n  ... and %d more", named-len(abandoned))
	}
	if r.Shortfall > 0 {
		if r.Sufficient() {
			s += fmt.Sprintf("\nshortfall is %d MB — this covers it", r.Shortfall>>20)
		} else {
			s += fmt.Sprintf("\nshortfall is %d MB — this will NOT cover it, "+
				"and nothing else can be released safely", r.Shortfall>>20)
		}
	}
	return s
}

// FootprintMB is the candidate's relief in megabytes.
func (c Candidate) FootprintMB() int64 { return c.ExpectedReliefBytes >> 20 }

// Rank orders candidates by relief per unit of risk, which is the design's
// relief_priority. Risk of zero is recommend-first, never infinite priority.
func Rank(cs []Candidate) {
	sort.SliceStable(cs, func(i, j int) bool {
		pi, pj := priority(cs[i]), priority(cs[j])
		if pi != pj {
			return pi > pj
		}
		return cs[i].ExpectedReliefBytes > cs[j].ExpectedReliefBytes
	})
}

func priority(c Candidate) float64 {
	risk := c.InterruptionRisk
	if risk < 0.05 {
		risk = 0.05
	}
	return float64(c.ExpectedReliefBytes) / risk * c.AbandonmentConfidence
}

// ShouldRecommend reports whether a group's classification is worth telling the
// owner about at all. This is the gate that stops the tool from narrating work.
//
// The old behaviour announced every leak_suspicion and subprocess_accumulation
// every window: measured on the owner's machine, 98 advisories in 45 minutes,
// all at Normal pressure, most for groups that were working (Code at 62% CPU,
// Chrome loading pages). The design's L1 says "record the finding, prepare a
// plan, without acting" — which is not the same as interrupting.
// climbing reports that the machine is under pressure now. Without it the
// answer is no: at Normal, a group growing is the cost of the work the owner
// asked for, and announcing it is the warning board they asked never to see
// again.
func ShouldRecommend(bucket classify.Bucket, climbing bool, hasCandidate bool) bool {
	if hasCandidate {
		return true // an abandoned process is worth one line
	}
	if !climbing {
		return false
	}
	// Under pressure, the shapes that are not explained by work are worth
	// naming. Workload growth under pressure is the machine doing its job
	// slowly, not a decision the owner can make.
	switch bucket {
	case classify.LeakSuspicion, classify.SubprocessAccumulation:
		return true
	}
	return false
}
