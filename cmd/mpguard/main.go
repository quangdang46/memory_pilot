// mpguard terminates processes that the memory-pressure daemon has identified
// as growing without bound, so the owner's terminal sessions survive.
//
// This is a SEPARATE BINARY on purpose. ADR-012 fixed "no process-control
// capability in the product" and named the only legal escape hatch: "If the
// owner later wants a manual 'terminate this process' utility, it is a separate
// product with its own spec, its own safety analysis, and its own binary. It
// will not be a flag on mempilot." mpguard is that binary. mempilotd remains
// incapable of signalling anything it did not spawn (SI-1), and the ST-1 gate
// still passes on it.
//
// The distinction is not cosmetic. The daemon runs unattended for weeks and
// acts on its own judgement; a mistake there is unbounded in time. mpguard runs
// when a human invokes it, prints what it will do, and can be stopped.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"syscall"
	"time"

	"memory_pilot/internal/model"
	"memory_pilot/platform"
)

const (
	// minFootprintBytes is the floor for being a candidate at all. Below this
	// the kill costs a process its state to recover a rounding error.
	minFootprintBytes = 256 << 20 // 256 MB
	// minIdleS is how long a process must have gone without CPU before it can
	// be called idle. A process using CPU is working, and killing work is the
	// exact failure this tool exists to avoid.
	//
	// Ten minutes, not two. Measured in a dry run: a rust-analyzer at "49m of
	// 50m life" and a Chrome helper at "10m of 12m life" both qualified under
	// a 120 s bar. Neither is a leak — they are young processes that have not
	// done much yet, and the ratio alone cannot tell them apart from an
	// abandoned one.
	minIdleS = 600.0
	// minAgeS keeps young processes out. A process that started a minute ago
	// has not had time to demonstrate what it is for.
	//
	// An hour, not five minutes: a leak is judged on how a process behaves
	// over a working session, and twenty minutes of life is still startup.
	minAgeS = 3600.0
)

func main() {
	dryRun := flag.Bool("dry-run", false, "show what would be killed without killing")
	yes := flag.Bool("yes", false, "actually terminate (required; without it nothing is killed)")
	minMB := flag.Int("min-mb", 256, "smallest footprint worth acting on, in MB")
	maxN := flag.Int("max", 10, "stop after this many processes")
	keep := flag.String("keep", "", "comma-separated base names to never touch (added to the built-in list)")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	plat, err := platform.NewPlatform()
	if err != nil {
		fatal("cannot read processes on this platform: %v", err)
	}
	snap, err := plat.Processes(ctx)
	if err != nil {
		fatal("process scan failed: %v", err)
	}

	extraKeep := map[string]bool{}
	for _, k := range strings.Split(*keep, ",") {
		if k = strings.TrimSpace(k); k != "" {
			extraKeep[strings.ToLower(k)] = true
		}
	}

	cands := selectVictims(snap, int64(*minMB)<<20, extraKeep)
	if len(cands) > *maxN {
		cands = cands[:*maxN]
	}
	if len(cands) == 0 {
		fmt.Println("nothing qualifies: no process is large, idle, old enough, and unprotected")
		return
	}

	var total int64
	for _, c := range cands {
		total += c.footprint
	}
	fmt.Printf("candidates: %d process(es), %d MB total\n\n", len(cands), total>>20)
	for _, c := range cands {
		// Both durations round to the same unit. Rounding age to the minute
		// while showing idle to the second printed "idle 20m3s of 20m0s life",
		// which reads as an arithmetic bug and undermines every number beside
		// it.
		fmt.Printf("  pid %-7d %-38s %5d MB   idle %s of %s life, %.1fs CPU total\n",
			c.pid, truncate(c.name, 38), c.footprint>>20,
			roundMinutes(c.idle), roundMinutes(c.age), c.cpuTotal)
		fmt.Printf("      %s\n", c.exe)
	}

	if *dryRun || !*yes {
		fmt.Printf("\nnothing was killed.")
		if !*yes {
			fmt.Printf(" re-run with --yes to terminate these processes.")
		}
		fmt.Println()
		return
	}

	fmt.Println()
	var killed, failed int
	for _, c := range cands {
		if err := syscall.Kill(int(c.pid), syscall.SIGTERM); err != nil {
			fmt.Printf("  pid %-7d failed: %v\n", c.pid, err)
			failed++
			continue
		}
		fmt.Printf("  pid %-7d SIGTERM sent\n", c.pid)
		killed++
	}
	fmt.Printf("\n%d terminated, %d failed\n", killed, failed)
	if failed > 0 {
		os.Exit(1)
	}
}

// victim is one process mpguard is willing to terminate.
type victim struct {
	pid       int32
	name      string
	exe       string
	footprint int64
	idle      time.Duration
	age       time.Duration
	cpuTotal  float64
}

// selectVictims applies every rule in one place, so the policy can be read
// without following call chains.
//
// The rules are ordered by how much damage a mistake would do:
//
//  1. never the kernel, our own processes, or the terminal the owner is using;
//  2. never a coding agent — that is the work being protected;
//  3. never something using CPU — it is working, and a leak does not look like
//     this (a leaking process allocates and does nothing);
//  4. never something young — it has not demonstrated what it is for;
//  5. only then rank by size, biggest first.
func selectVictims(snap model.ProcessSnapshot, minFootprint int64, extraKeep map[string]bool) []victim {
	// A live parent means the process may still be doing someone's work.
	// Measured in a dry run: the two largest candidates (a 1.9 GB tsx server
	// 73 h old, a 1.3 GB Docker backend 73 h old) had live parents that were
	// themselves idle node processes — an abandoned chain, but the tree is
	// still standing, and "the parent is also idle" is a conclusion drawn
	// from the same weak signal twice, not a second piece of evidence.
	//
	// Candidates whose parent is alive are collected separately and only
	// offered when nothing orphaned qualifies, so the default action is the
	// unambiguous one.
	var orphans, parented []victim
	live := make(map[int32]bool, len(snap.Processes))
	for _, p := range snap.Processes {
		live[p.Identity.Pid] = true
	}
	now := snap.At
	for _, p := range snap.Processes {
		name := p.Identity.Name
		if name == "" {
			name = baseName(p.Identity.Executable)
		}
		exe := p.Identity.Executable

		// An unreadable image path means every protection list is blind for
		// this process. Measured: Claude Code's current build (pid 58684,
		// `claude --resume …`) refuses proc_pidpath — it returns 0 for that
		// pid while returning a normal path for its neighbours — so its
		// identity is only the version directory name, "2.1.310". The agent
		// list matches names and path segments and does not contain "2.1.310",
		// so the running session appeared as an anonymous 337 MB process and
		// was selected as releasable. The same pattern produced three
		// releasable "claude" processes in the L1 engine's first live run.
		//
		// There is no name to check in that case, and killing what cannot be
		// identified is the one mistake this tool must never make.
		if !strings.Contains(exe, "/") {
			continue
		}
		if isProtectedProcess(name, exe, extraKeep) {
			continue
		}
		fp := int64(p.FootprintBytes)
		if fp < minFootprint {
			continue
		}
		// A process whose CPU could not be read is not evidence of idleness.
		// Missing evidence never improves a verdict (SI-4), and here the
		// verdict authorises a kill.
		if p.CpuUnavailable {
			continue
		}
		age := now.Sub(p.Identity.StartTime).Seconds()
		if age < minAgeS {
			continue
		}
		cpu := p.CpuUserS + p.CpuSystemS
		idle := idleFrom(cpu, age)
		if idle < time.Duration(minIdleS)*time.Second {
			continue
		}
		v := victim{
			pid: p.Identity.Pid, name: name, exe: exe, footprint: fp,
			idle: idle, age: time.Duration(age) * time.Second, cpuTotal: cpu,
		}
		if ppid := p.Identity.Ppid; ppid > 1 && live[ppid] {
			parented = append(parented, v)
			continue
		}
		orphans = append(orphans, v)
	}
	sort.Slice(orphans, func(i, j int) bool { return orphans[i].footprint > orphans[j].footprint })
	sort.Slice(parented, func(i, j int) bool { return parented[i].footprint > parented[j].footprint })
	return append(orphans, parented...)
}

// idleFrom approximates how long the process has been idle from its lifetime
// CPU accounting. There is no per-process "last ran" clock available without
// sampling, so the conservative reading is used: a process that has burned
// less than one second of CPU per minute of life is holding, not working.
func idleFrom(cpuTotalS, ageS float64) time.Duration {
	if ageS <= 0 {
		return 0
	}
	if cpuTotalS <= 0 {
		return time.Duration(ageS) * time.Second
	}
	// Work out the fraction of its life spent on CPU; the remaining time is
	// the idle estimate. This under-reports idleness for anything that ran
	// hard early and stopped, which is the safe direction.
	busyS := cpuTotalS * 100 // CPU seconds count far more than wall seconds
	if busyS >= ageS {
		return 0
	}
	return time.Duration(ageS-busyS) * time.Second
}

// protectedNames are never terminated, whatever their footprint.
//
// These are the things whose loss ends the session or the desktop. The list is
// deliberately about consequences, not about "system vs user": Finder is a user
// process and killing it breaks the desktop just as surely as killing the
// window server does.
var protectedNames = map[string]bool{
	// the desktop
	"finder": true, "dock": true, "windowmanager": true, "systemuiserver": true,
	"windowserver": true, "loginwindow": true, "coreaudiod": true,
	// virtualisation: a running VM dies with it
	"virtualmachine": true, "com.apple.virtualization.virtualmachine": true,
	// the terminal the owner's work lives in
	"wezterm-gui": true, "terminal": true, "iterm2": true, "alacritty": true,
	"kitty": true, "tmux": true, "sshd": true,
	// this tool and the daemon it works with
	"mpguard": true, "mempilot": true, "mempilotd": true, "launchd": true,
}

// protectedAgentNames are the coding agents. Killing one destroys in-flight
// work with no resume path — the single worst outcome this tool can produce.
var protectedAgentNames = map[string]bool{
	"claude": true, "codex": true, "cursor-agent": true, "opencode": true,
	"aider": true, "goose": true, "amp": true, "gemini": true, "copilot": true,
	"continue": true, "zcode-cli": true, "codeium": true, "windsurf": true,
	"plandex": true, "crush": true,
}

// protectedSystemPrefixes are directories macOS installs into. Anything running
// from them is the operating system.
var protectedSystemPrefixes = []string{
	"/System/", "/usr/libexec/", "/usr/sbin/", "/Library/Apple/",
}

func isProtectedProcess(name, exe string, extra map[string]bool) bool {
	if protectedKey(name, extra) {
		return true
	}
	// Match every path segment: an agent may run as .../versions/2.1.312, and
	// the npm-installed agents carry a .exe suffix even on macOS
	// (opencode.exe), so the bare name alone does not match.
	for _, seg := range strings.Split(exe, "/") {
		if protectedKey(seg, extra) {
			return true
		}
	}
	for _, p := range protectedSystemPrefixes {
		if strings.HasPrefix(exe, p) {
			return true
		}
	}
	return false
}

// protectedKey reports whether one name belongs to a protected process.
//
// The .exe suffix is stripped before matching. Agents distributed through npm
// keep the Windows-style suffix in the file name even when running on macOS —
// measured here, the running OpenCode binary is
// "…/bin/opencode.exe", and an exact-name list let it through as releasable.
func protectedKey(s string, extra map[string]bool) bool {
	s = strings.ToLower(s)
	if s == "" {
		return false
	}
	candidates := []string{s}
	if trimmed := strings.TrimSuffix(s, ".exe"); trimmed != s {
		candidates = append(candidates, trimmed)
	}
	for _, c := range candidates {
		if protectedNames[c] || protectedAgentNames[c] || extra[c] {
			return true
		}
	}
	return false
}

func baseName(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

// roundMinutes renders a duration at minute resolution. Used for both sides of
// the "idle X of Y life" pair so the two numbers are comparable at a glance.
func roundMinutes(d time.Duration) string {
	if d < time.Minute {
		return d.Round(time.Second).String()
	}
	return d.Round(time.Minute).String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "mpguard: "+format+"\n", args...)
	os.Exit(2)
}
