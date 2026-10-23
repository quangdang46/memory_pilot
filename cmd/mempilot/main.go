// mempilot — the CLI (harness surface in phase 1; full surface is Phase 5).
package main

import (
	"fmt"
	"os"
	"time"

	"memory_pilot/internal/ipc"
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
		fmt.Fprintln(os.Stderr, "usage: mempilot <status|doctor|...>")
		os.Exit(2)
	}
	op := os.Args[1]
	switch op {
	case "status", "doctor":
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q (phase 1 surface: status, doctor)\n", op)
		os.Exit(2)
	}
	resp, err := ipc.Call(socketPath(), ipc.Request{V: ipc.ProtocolVersion, Op: op}, 5*time.Second)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mempilot:", err)
		os.Exit(4) // cannot reach daemon
	}
	if !resp.OK {
		fmt.Fprintln(os.Stderr, "mempilot:", resp.Error)
		os.Exit(1)
	}
	fmt.Println(string(resp.Data))
}
