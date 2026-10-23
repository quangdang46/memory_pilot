// mempilotd — the daemon. Constructs the graph per module-contracts §7:
// no package self-registers; construction validates config; failure ⇒ exit 3
// before any goroutine starts.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"memory_pilot/internal/clock"
	"memory_pilot/internal/config"
	"memory_pilot/internal/ledger"
	"memory_pilot/internal/selfmonitor"
	"memory_pilot/internal/store"
	"memory_pilot/internal/supervisor"
	"memory_pilot/internal/ipc"
	"memory_pilot/platform"
)

func main() {
	dataRoot := flag.String("data-root", defaultRoot(), "daemon data directory (SI-3 boundary)")
	cfgPath := flag.String("config", "", "optional config file")
	flag.Parse()

	if err := run(*dataRoot, *cfgPath); err != nil {
		fmt.Fprintln(os.Stderr, "mempilotd:", err)
		os.Exit(exitFor(err))
	}
}

func defaultRoot() string {
	home, _ := os.UserHomeDir()
	return home + "/.local/share/mempilot"
}

func exitFor(err error) int {
	if _, ok := err.(configError); ok {
		return 3
	}
	return 1
}

type configError struct{ err error }

func (c configError) Error() string { return c.err.Error() }

func run(dataRoot, cfgPath string) error {
	var cfg config.Config
	if cfgPath != "" {
		c, err := config.Load(cfgPath)
		if err != nil {
			return configError{err}
		}
		cfg = c
	} else {
		cfg = config.DefaultConfig(dataRoot)
	}
	cfg.DataRoot = dataRoot
	if err := cfg.Validate(); err != nil {
		return configError{err}
	}

	lock, err := supervisor.SingleInstance(cfg.DataRoot)
	if err != nil {
		return err
	}
	defer lock.Close()

	st, err := store.New(cfg.DataRoot)
	if err != nil {
		return err
	}
	_ = st
	lg, err := ledger.Open(cfg.DataRoot, "ledger")
	if err != nil {
		return err
	}
	plat, err := platform.NewPlatform()
	if err != nil {
		return err
	}

	clk := clock.Real()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mon := selfmonitor.New(clk, cfg.Overrides)

	// supervisor main loop: two-cadence collection, quality-stamped, into ledger
	go mon.Run(ctx, lg.SizeBytes)
	sysDone := make(chan struct{})
	go func() {
		defer close(sysDone)
		t := clk.NewTicker(cfg.Overrides.SystemSampleEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C():
				s, err := plat.SampleSystem(ctx)
				if err != nil {
					// record the failure as an event — missing evidence is visible (SI-4)
					lg.Append(map[string]any{"kind": "system_sample_error", "at": time.Now().UTC(), "error": err.Error()})
					continue
				}
				lg.Append(map[string]any{"kind": "system", "sample": s})
			}
		}
	}()
	procDone := make(chan struct{})
	go func() {
		defer close(procDone)
		t := clk.NewTicker(cfg.Overrides.ProcessSampleEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C():
				snap, err := plat.Processes(ctx)
				if err != nil {
					lg.Append(map[string]any{"kind": "process_sample_error", "at": time.Now().UTC(), "error": err.Error()})
					continue
				}
				lg.Append(map[string]any{"kind": "processes", "coverage": snap.Coverage, "n": len(snap.Processes), "at": snap.At})
			}
		}
	}()

	// IPC server with the handler backed by live state
	socket := cfg.DataRoot + "/mempilot.sock"
	handler := &daemonHandler{lg: lg, mon: mon}
	ipcSrv, err := ipc.Serve(ctx, socket, handler)
	if err != nil {
		return err
	}

	// signal handling: shutdown order = intake → collector → ledger flush → IPC
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGTERM, syscall.SIGINT)
	<-sig
	cancel()
	<-sysDone
	<-procDone
	lg.Close()
	ipcSrv.Path()
	os.Remove(socket)
	return nil
}

type daemonHandler struct {
	lg  *ledger.Ledger
	mon *selfmonitor.Monitor
}

func (h *daemonHandler) Handle(_ context.Context, req ipc.Request) (ipc.Response, error) {
	switch req.Op {
	case "status":
		s := h.mon.Snapshot()
		b, _ := json.Marshal(map[string]any{
			"healthy": h.mon.Healthy(), "rss_mb": s.RSSMB, "cpu_pct": s.CPUPct,
			"goroutines": s.Goroutines, "ledger_mb": s.LedgerMB, "detail": s.Budget.Detail,
		})
		return ipc.Response{V: ipc.ProtocolVersion, OK: true, Data: b}, nil
	case "doctor":
		probs := []string{}
		b, _ := json.Marshal(probs)
		return ipc.Response{V: ipc.ProtocolVersion, OK: true, Data: b}, nil
	default:
		return ipc.Response{V: ipc.ProtocolVersion, Error: "op not available in phase 1"}, nil
	}
}
