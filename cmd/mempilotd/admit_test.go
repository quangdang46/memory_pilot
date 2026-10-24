package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"memory_pilot/internal/executor"
	"memory_pilot/internal/ipc"
	"memory_pilot/internal/ledger"
)

func newTestHandler(t *testing.T) (*daemonHandler, *ledger.Ledger, *executor.Registry) {
	t.Helper()
	dir := t.TempDir()
	lg, err := ledger.Open(dir, "ledger")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lg.Close() })
	reg := executor.NewRegistry()
	return &daemonHandler{lg: lg, dataRoot: dir, mode: "canary",
		registry: reg, startedAt: time.Now()}, lg, reg
}

func admit(t *testing.T, h *daemonHandler, peer int32, pid int32, allowStop bool) ipc.Response {
	t.Helper()
	arg, _ := json.Marshal(map[string]any{"pid": pid, "allow_stop": allowStop, "decision": "ok"})
	ctx := ipc.WithPeerPID(context.Background(), peer)
	resp, err := h.handleAdmit(ctx, ipc.Request{V: ipc.ProtocolVersion, Op: "admit", Arg: arg})
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// SI-1: a caller that is not the parent of the pid it names must be refused.
// Before this check, any same-uid process could register PID 1 as an owned,
// stop-authorized workload over the Unix socket.
func TestAdmitRefusesNonParent(t *testing.T) {
	h, lg, reg := newTestHandler(t)
	h.plat = nil // parent lookup unavailable ⇒ must fail closed, not allow

	resp := admit(t, h, 999, 1, true)
	if resp.OK {
		t.Fatal("SI-1: admit accepted a pid whose parent is not the caller")
	}
	if _, ok := reg.Lookup(1); ok {
		t.Fatal("SI-1: refused admission still registered the pid")
	}
	recs, _ := lg.ReadAll()
	found := false
	for _, r := range recs {
		var m map[string]any
		json.Unmarshal(r, &m)
		if m["kind"] == "admission_refused" {
			found = true
		}
	}
	if !found {
		t.Error("refused attempt was not recorded")
	}
}

// A caller without a resolvable peer pid cannot prove parentage ⇒ refuse.
func TestAdmitRefusesWithoutPeerPID(t *testing.T) {
	h, _, reg := newTestHandler(t)
	resp, err := h.handleAdmit(context.Background(), ipc.Request{V: ipc.ProtocolVersion,
		Op: "admit", Arg: json.RawMessage(`{"pid":42,"allow_stop":true}`)})
	if err != nil {
		t.Fatal(err)
	}
	if resp.OK {
		t.Fatal("admit accepted a request with no peer credential")
	}
	if _, ok := reg.Lookup(42); ok {
		t.Fatal("registered without peer proof")
	}
}

// Stop authorization must be refused in observe mode even when requested
// (ADR-014 §2 item 7): observe plans, it never arms. Exercised at the
// authorization decision itself, since the parentage gate cannot be faked
// without a real child.
func TestObserveModeNeverArmsStop(t *testing.T) {
	for _, mode := range []string{"observe", "canary", "enforce"} {
		armed := mode == "canary" || mode == "enforce"
		if got := stopArmedForMode(mode, true); got != armed {
			t.Errorf("mode %s: stop armed=%v, want %v", mode, got, armed)
		}
		if got := stopArmedForMode(mode, false); got {
			t.Errorf("mode %s: armed without --allow-stop", mode)
		}
	}
}

func TestDoctorReportsMissingLedgerWithoutPanicking(t *testing.T) {
	h, lg, _ := newTestHandler(t)
	lg.Close()
	os.Remove(filepath.Join(h.dataRoot, "ledger.0001.jsonl"))
	resp, err := h.Handle(context.Background(), ipc.Request{V: ipc.ProtocolVersion, Op: "doctor"})
	if err != nil {
		t.Fatalf("doctor returned an error instead of a report: %v", err)
	}
	if !resp.OK {
		t.Fatalf("doctor failed: %s", resp.Error)
	}
}
