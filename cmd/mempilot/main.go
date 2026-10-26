// mempilot — the user-facing CLI (Phase 5 surface): status, report, explain,
// doctor, policy, forecast; `mp run` is admission control (T2).
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"memory_pilot/internal/ipc"
	"memory_pilot/launchd"
)

const (
	exitOK          = 0
	exitUsage       = 2
	exitGeneral     = 1
	exitConfig      = 3
	exitNoDaemon    = 4
	exitUnsupported = 5
)

func socketPath() string {
	if p := os.Getenv("MEMPILOT_SOCK"); p != "" {
		return p
	}
	home, _ := os.UserHomeDir()
	return home + "/.local/share/mempilot/mempilot.sock"
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(exitUsage)
	}
	cmd := os.Args[1]
	args := os.Args[2:]
	switch cmd {
	case "status":
		status(args)
	case "report":
		report(args)
	case "forecast":
		forecastLine(args)
	case "relief":
		reliefLine(args)
	case "explain", "doctor", "policy":
		callAndPrint(cmd, args)
	case "service":
		service(args)
	case "run":
		run(args) // T2 admission control
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n", cmd)
		usage()
		os.Exit(exitUsage)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `usage: mempilot <command>
  status     daemon health + pressure state
  forecast   current forecast (honest language, no ETA promises when warming)
  report     what grew, by workload group, with coverage
  relief     what could be released now, how much it returns, and whether that
             is enough — the L1 answer, on demand
  explain    reconstruct a decision from the ledger
  policy     current mode + allowlist
  doctor     installation + platform probe
  run CMD    admission control around a real launch (--est-mb, --wait, --force, --allow-stop)
`)
}

// status prints the one-line health summary plus per-group growth. The JSON
// form is still available with --json for scripts.
func status(args []string) {
	raw := fetch("status")
	if hasFlag(args, "--json") {
		printRaw(raw)
		return
	}
	var d struct {
		Line       string  `json:"line"`
		Uptime     int64   `json:"uptime_s"`
		Groups     []group `json:"groups"`
		Actionable bool    `json:"actionable"`
		TteS       float64 `json:"tte_s"`
	}
	json.Unmarshal(raw, &d)
	fmt.Println(d.Line)
	if len(d.Groups) == 0 {
		fmt.Println("\n  no attribution window closed yet (first one lands after 5 min)")
		return
	}
	fmt.Println("\ntop movers (last window)")
	printGroups(d.Groups, hasFlag(args, "--all"))
}

// report answers the question the product exists to answer: what grew, by
// workload, and how much of the system movement that explains. It never
// claims a cause and never says "leak" — the classifier's own wording is
// used verbatim so the two cannot drift apart.
type group struct {
	ID        string  `json:"id"`
	GrowthMB  int64   `json:"growth_mb"`
	Processes int     `json:"processes"`
	TopExe    string  `json:"top_exe"`
	PeakBps   float64 `json:"peak_bps"`
}

func report(args []string) {
	raw := fetch("report")
	if hasFlag(args, "--json") {
		printRaw(raw)
		return
	}
	var recs []map[string]any
	json.Unmarshal(raw, &recs)
	// Records arrive newest first. "Latest" therefore means the FIRST match,
	// not the last: overwriting kept the oldest attribution window in the
	// response, so `report` showed a window from before the last restart while
	// the ledger had newer ones. Observed: the CLI reported an 11:45 window
	// with 914 MB in wezterm-gui while the daemon was writing 12:16 windows.
	var latestAttr, latestVerdict map[string]any
	var advisories []map[string]any
	for _, r := range recs {
		switch r["kind"] {
		case "attribution":
			if latestAttr == nil {
				latestAttr = r
			}
		case "classification":
			// Newest window wins, then the most notable bucket in it. Taking
			// the first record made the summary line arbitrary: the ledger
			// happened to order a group that grew 0 MB first, so `report`
			// ended with "group intelligenceflo grew by 0 MB — insufficient
			// evidence", which is true, useless, and reads like a fault.
			if latestVerdict == nil || notable(r) > notable(latestVerdict) {
				latestVerdict = r
			}
		case "advisory":
			if len(advisories) < 6 {
				advisories = append(advisories, r)
			}
		}
	}
	if latestAttr == nil {
		fmt.Println("no attribution window has closed yet — nothing to report")
		fmt.Println("(the daemon reports each 5-minute window; check `mempilot status`)")
		return
	}
	// State how old the window is. After a daemon restart the ledger's newest
	// attribution can be many minutes old, and a report that presents it as
	// current is the same class of lie as a verdict without its basis: the
	// numbers are right, the implied "now" is not.
	age := ""
	if at := strOf(latestAttr["at"]); at != "" {
		if ts, err := time.Parse(time.RFC3339Nano, at); err == nil {
			if d := time.Since(ts).Round(time.Minute); d >= 2*time.Minute {
				age = fmt.Sprintf(" | %s ago", d)
			}
		}
	}
	fmt.Printf("window %ds%s | accounted %v MB | system headroom fell %v MB | compressor %v MB\n\n",
		int64Of(latestAttr["window_s"]), age, latestAttr["accounted_mb"],
		latestAttr["observed_mb"], latestAttr["compressor_mb"])
	if gs, ok := latestAttr["groups"].([]any); ok && len(gs) > 0 {
		var out []group
		for _, g := range gs {
			m, _ := g.(map[string]any)
			out = append(out, group{ID: strOf(m["id"]), GrowthMB: int64Of(m["growth_mb"]),
				Processes: int(int64Of(m["processes"])), TopExe: strOf(m["top_exe"]),
				PeakBps: floatOf(m["peak_bps"])})
		}
		printGroups(out, hasFlag(args, "--all"))
	} else {
		fmt.Println("  no group grew beyond the reporting floor in this window")
	}
	if v, ok := latestAttr["coverage"].(map[string]any); ok {
		fmt.Printf("\ncoverage: %v seen, %v outside this user (unreadable by design), %v denied\n",
			v["Succeeded"], v["Foreign"], v["Denied"])
	}
	if note := strOf(latestAttr["note"]); note != "" {
		fmt.Println(note)
	}
	if latestVerdict != nil && notable(latestVerdict) > 0 {
		fmt.Printf("\nclassification: %s\n", strOf(latestVerdict["text"]))
	}
	if len(advisories) > 0 {
		fmt.Println("\nlatest advisories")
		for _, a := range advisories {
			fmt.Printf("  [%s] %s\n", strOf(a["strength"]), strOf(a["text"]))
		}
	}
}

// notable ranks a verdict for the one-line summary. "Insufficient evidence"
// is the honest answer for most groups and the least worth printing.
func notable(r map[string]any) int {
	switch strOf(r["bucket"]) {
	case "leak_suspicion":
		return 4
	case "subprocess_accumulation":
		return 3
	case "workload_growth":
		return 2
	case "cache_or_retrievable":
		return 1
	}
	return 0
}

// reportFloorMB is how much a group must have grown to get its own line.
// Measured on the owner's machine at 4 minutes into a normal working day: 186
// groups "grew" in one window, 193 lines of output, most of them 0-3 MB of
// page noise. That list is the warning board — nothing in it is a decision,
// and a reader who scrolls past it learns to scroll past all of it. The floor
// hides nothing: the remainder is counted and summed in the last line, and
// --all prints every group.
const reportFloorMB = 64

func printGroups(gs []group, all bool) {
	shown, hidden, hiddenMB := 0, 0, int64(0)
	for _, g := range gs {
		if !all && g.GrowthMB < reportFloorMB {
			hidden++
			hiddenMB += g.GrowthMB
			continue
		}
		name := g.ID
		if name == "" {
			name = g.TopExe
		}
		top := ""
		if g.TopExe != "" && g.TopExe != name {
			top = "  top: " + g.TopExe
		}
		fmt.Printf("  +%6d MB  %-22s %2d procs%s\n", g.GrowthMB, name, g.Processes, top)
		shown++
	}
	if hidden > 0 {
		fmt.Printf("  %d other group(s) grew less than %d MB each, %d MB in total "+
			"(`report --all` lists them)\n", hidden, reportFloorMB, hiddenMB)
	}
	if shown == 0 && hidden == 0 {
		fmt.Println("  no group grew beyond the reporting floor in this window")
	}
}

// reliefLine asks the daemon the L1 question directly. Unlike an advisory it
// is not gated on pressure or rate-limited: the owner asked, so they get the
// current answer with its evidence.
func reliefLine(args []string) {
	raw := fetch("relief")
	if hasFlag(args, "--json") {
		printRaw(raw)
		return
	}
	var d struct {
		Severity    string `json:"severity"`
		ShortfallMB int64  `json:"shortfall_mb"`
		TotalMB     int64  `json:"total_relief_mb"`
		Sufficient  bool   `json:"sufficient"`
		Candidates  int    `json:"candidates"`
		Text        string `json:"text"`
	}
	json.Unmarshal(raw, &d)
	fmt.Println(d.Text)
	if d.Candidates > 0 {
		fmt.Printf("\n(%d candidate(s), %d MB releasable, shortfall %d MB, sufficient %v, at %s pressure)\n",
			d.Candidates, d.TotalMB, d.ShortfallMB, d.Sufficient, d.Severity)
	}
}

func forecastLine(args []string) {
	raw := fetch("forecast")
	if hasFlag(args, "--json") {
		printRaw(raw)
		return
	}
	var d struct {
		Line string `json:"line"`
	}
	json.Unmarshal(raw, &d)
	fmt.Println(d.Line)
}

func fetch(op string) json.RawMessage {
	resp, err := ipc.Call(socketPath(), ipc.Request{V: ipc.ProtocolVersion, Op: op}, 5*time.Second)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mempilot: cannot reach daemon:", err)
		os.Exit(exitNoDaemon)
	}
	if !resp.OK {
		fmt.Fprintln(os.Stderr, "mempilot:", resp.Error)
		os.Exit(exitGeneral)
	}
	return resp.Data
}

func printRaw(raw json.RawMessage) { fmt.Println(string(raw)) }

func hasFlag(args []string, f string) bool {
	for _, a := range args {
		if a == f {
			return true
		}
	}
	return false
}

func strOf(v any) string {
	s, _ := v.(string)
	return s
}

func floatOf(v any) float64 {
	f, _ := v.(float64)
	return f
}

func callAndPrint(op string, _ []string) {
	resp, err := ipc.Call(socketPath(), ipc.Request{V: ipc.ProtocolVersion, Op: op}, 5*time.Second)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mempilot: cannot reach daemon:", err)
		os.Exit(exitNoDaemon)
	}
	if !resp.OK {
		fmt.Fprintln(os.Stderr, "mempilot:", resp.Error)
		os.Exit(exitGeneral)
	}
	fmt.Println(string(resp.Data))
	os.Exit(exitOK)
}

// run: T2 admission control around a real launch. The wrapper asks the daemon
// for a decision, then execs the command — it must never swallow it. (Before
// this, `mp run echo hi` printed "[ok] headroom sufficient" and exited 0
// without running anything, so the managed-launch path did not exist and the
// L2 stop path had no legitimate target.)
func run(args []string) {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	estMB := fs.Int64("est-mb", 0, "estimated footprint MB")
	force := fs.Bool("force", false, "launch despite pressure (recorded)")
	wait := fs.Bool("wait", false, "wait until pressure clears")
	allowStop := fs.Bool("allow-stop", false,
		"authorize managed stop of this workload under Critical pressure (ADR-014; canary-gated)")
	fs.Parse(args)
	cmd := fs.Args()
	if len(cmd) == 0 {
		fmt.Fprintln(os.Stderr, "run: CMD required")
		os.Exit(exitUsage)
	}

	decision := "ok"
	var headroomMB float64
	resp, err := ipc.Call(socketPath(), ipc.Request{V: ipc.ProtocolVersion, Op: "status"}, 5*time.Second)
	switch {
	case err != nil:
		// Fail open: the wrapper's job is to launch work, and a dead daemon
		// must not block the user. Say so, because silence would hide a
		// broken install.
		fmt.Fprintln(os.Stderr, "mempilot: daemon unreachable — launching without admission control")
		decision = "unmanaged"
	default:
		var st struct {
			KernelLevel int     `json:"kernel_level"`
			FreeMB      float64 `json:"free_mb"`
		}
		json.Unmarshal(resp.Data, &st)
		headroomMB = st.FreeMB
		need := float64(*estMB)
		switch {
		case *force:
			decision = "forced"
		case need > 0 && st.FreeMB > 0 && need > st.FreeMB*0.8 && *wait:
			fmt.Printf("[defer] need %.0fMB, headroom %.0fMB — waiting for pressure to clear (--wait)\n", need, st.FreeMB)
			os.Exit(exitOK)
		case need > 0 && st.FreeMB > 0 && need > st.FreeMB*0.8:
			decision = "warn"
			fmt.Fprintf(os.Stderr, "[warn] need %.0fMB vs %.0fMB headroom — launching anyway (--wait to defer)\n", need, st.FreeMB)
		}
	}
	if decision == "forced" {
		fmt.Fprintln(os.Stderr, "[forced] launching under pressure; decision recorded")
	}
	launch(cmd, *allowStop, decision, headroomMB)
}

// launch starts the workload. The daemon is told before and after the spawn
// so it can hold a registrable (pid, start time) reference — ADR-014 requires
// the daemon to be the recorded parent, and pid alone is never identity.
func launch(cmd []string, allowStop bool, decision string, headroomMB float64) {
	bin, err := exec.LookPath(cmd[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "mempilot: %s: %v\n", cmd[0], err)
		os.Exit(exitGeneral)
	}
	attr := &os.ProcAttr{Env: os.Environ(), Files: []*os.File{os.Stdin, os.Stdout, os.Stderr}}
	proc, err := os.StartProcess(bin, cmd, attr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mempilot: cannot start %s: %v\n", bin, err)
		os.Exit(exitGeneral)
	}
	// The wrapper is the parent until it exits; the daemon records ownership
	// now and revalidates identity at any future stop (precondition 3).
	_, _ = ipc.Call(socketPath(), ipc.Request{V: ipc.ProtocolVersion, Op: "admit",
		Arg: mustJSON(map[string]any{"pid": proc.Pid, "allow_stop": allowStop,
			"decision": decision, "headroom_mb": headroomMB})}, 5*time.Second)
	state, err := proc.Wait()
	code := 0
	if err != nil {
		fmt.Fprintf(os.Stderr, "mempilot: wait: %v\n", err)
		code = exitGeneral
	} else if ws, ok := state.Sys().(syscall.WaitStatus); ok {
		code = ws.ExitStatus()
		if ws.Signaled() {
			code = 128 + int(ws.Signal())
		}
	}
	_, _ = ipc.Call(socketPath(), ipc.Request{V: ipc.ProtocolVersion, Op: "admit_exit",
		Arg: mustJSON(map[string]any{"pid": proc.Pid})}, 5*time.Second)
	os.Exit(code)
}

func mustJSON(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func int64Of(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	}
	return 0
}

// service installs or removes the LaunchAgent. Without it the daemon dies with
// the terminal that started it, which is the difference between "runs when I
// remember" and "always running".
func service(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: mempilot service install|uninstall|status")
		os.Exit(exitUsage)
	}
	dataRoot := os.Getenv("MEMPILOT_DATA")
	if dataRoot == "" {
		home, _ := os.UserHomeDir()
		dataRoot = home + "/.local/share/mempilot"
	}
	switch args[0] {
	case "install":
		bin, err := os.Executable()
		if err != nil {
			fmt.Fprintln(os.Stderr, "mempilot: cannot locate own binary:", err)
			os.Exit(exitGeneral)
		}
		// Install the daemon sibling, not the CLI.
		bin = filepath.Join(filepath.Dir(bin), "mempilotd")
		if _, err := os.Stat(bin); err != nil {
			fmt.Fprintf(os.Stderr, "mempilot: %s not found — build it first (make build)\n", bin)
			os.Exit(exitGeneral)
		}
		// Carry the config through, or the service silently runs in the
		// built-in observe mode and never acts.
		cfgPath := os.Getenv("MEMPILOT_CONFIG")
		if cfgPath == "" {
			cfgPath = launchd.DefaultConfigPath()
		}
		if _, err := os.Stat(cfgPath); err != nil {
			if werr := os.MkdirAll(filepath.Dir(cfgPath), 0o755); werr == nil {
				_ = os.WriteFile(cfgPath, []byte(defaultConfig(dataRoot)), 0o644)
			}
			fmt.Fprintf(os.Stderr, "mempilot: wrote default config %s (mode canary)\n", cfgPath)
		}
		p, err := launchd.Install(bin, dataRoot, cfgPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "mempilot: install failed:", err)
			os.Exit(exitGeneral)
		}
		fmt.Printf("installed %s\n  runs %s\n  data %s\n", p, bin, dataRoot)
		fmt.Println("the daemon starts now and at every login; `mempilot service uninstall` removes it")
	case "uninstall":
		if err := launchd.Uninstall(); err != nil {
			fmt.Fprintln(os.Stderr, "mempilot: uninstall failed:", err)
			os.Exit(exitGeneral)
		}
		fmt.Println("removed the LaunchAgent")
	case "status":
		probs := launchd.Doctor(dataRoot)
		if len(probs) == 0 {
			fmt.Println("service installed and loaded")
			return
		}
		for _, p := range probs {
			fmt.Println(" -", p)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown service command %q\n", args[0])
		os.Exit(exitUsage)
	}
}

// defaultConfig is what a fresh install starts with: canary, so the daemon can
// actually act on a pre-authorized owned workload. Enforce remains a manual
// promotion after canary evidence (ADR-014 §2 item 7).
func defaultConfig(dataRoot string) string {
	return fmt.Sprintf(`{
  "data_root": %q,
  "mode": "canary",
  "overrides": {}
}
`, dataRoot)
}
