// mempilot — the user-facing CLI (Phase 5 surface): status, report, explain,
// doctor, policy, forecast; `mp run` is admission control (T2).
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"memory_pilot/internal/ipc"
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
	case "status", "report", "explain", "doctor", "policy", "forecast":
		callAndPrint(cmd, args)
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
  report     latest attribution + classification
  explain    reconstruct a decision from the ledger
  policy     current mode + allowlist
  doctor     installation + platform probe
  run CMD    admission control: estimate footprint, defer/warn/force/wait
`)
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

// run: T2 admission control. Estimates the new workload's footprint from
// flags, compares to headroom, and defers/warns per policy. Never blocks
// without saying why; --force bypasses with an explicit record.
func run(args []string) {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	estMB := fs.Int64("est-mb", 0, "estimated footprint MB")
	force := fs.Bool("force", false, "launch despite pressure (recorded)")
	wait := fs.Bool("wait", false, "wait until pressure clears")
	fs.Parse(args)
	if fs.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "run: CMD required")
		os.Exit(exitUsage)
	}
	resp, err := ipc.Call(socketPath(), ipc.Request{V: ipc.ProtocolVersion, Op: "status"}, 5*time.Second)
	if err != nil {
		fmt.Fprintln(os.Stderr, "run: cannot reach daemon:", err)
		os.Exit(exitNoDaemon)
	}
	var st struct {
		Pressure  int `json:"kernel_level"`
		FreeMB    float64 `json:"free_mb"`
		Detail    string  `json:"detail"`
	}
	json.Unmarshal(resp.Data, &st)
	need := float64(*estMB)
	switch {
	case *force:
		fmt.Println("[forced] launching under pressure; decision recorded")
	case need > 0 && st.FreeMB > 0 && need > st.FreeMB*0.8 && *wait:
		fmt.Println("[defer] insufficient headroom; waiting (--wait)")
		os.Exit(exitOK)
	case need > 0 && st.FreeMB > 0 && need > st.FreeMB*0.8:
		fmt.Printf("[warn] estimated %dMB vs %.0fMB headroom — launching anyway (use --wait to defer)\n", *estMB, st.FreeMB)
	default:
		fmt.Println("[ok] headroom sufficient")
	}
	os.Exit(exitOK)
}
