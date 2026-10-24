//go:build darwin

package pipeline

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"memory_pilot/internal/classify"
	"memory_pilot/internal/executor"
	"memory_pilot/internal/model"
	"memory_pilot/internal/policy"
	"memory_pilot/internal/verify"
)

// End-to-end L2 on the real machine: a process this test spawns, registers as
// an owned workload with prior authorization, and then asks the pipeline to
// stop under Critical pressure with leak evidence. The assertion is that the
// process is actually gone — not that a record says so.
//
// This exercises the single audited signal call site (ST-1) for real, which
// no unit test with a fake pid can do.
func TestL2StopsRealOwnedProcessOnDevice(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns and signals a real process")
	}
	cmd := exec.Command("/bin/sh", "-c", "sleep 120")
	if err := cmd.Start(); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	pid := int32(cmd.Process.Pid)
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	lg := &recordingLedger{}
	reg := executor.NewRegistry()
	// The daemon spawned it (this test stands in for `mp run`) and the user
	// gave prior authorization for managed stop.
	reg.Register(executor.OwnedPid{Pid: pid, Pgid: pid, Auth: "allow-stop"})
	guard := &executor.ActionGuard{
		Mode:     func() policy.Mode { return policy.ModeCanary },
		Registry: reg, MaxAge: 30 * time.Second,
	}
	ex := executor.New(guard)
	ex.Actors = executor.Actors{
		TrimOwnBuffers:  func() (executor.Effect, error) { return executor.Effect{Changed: false}, nil },
		RotateOwnLedger: func() (executor.Effect, error) { return executor.Effect{Changed: true}, nil },
	}
	p := New(Config{WindowS: 60}, lg, policy.NewEngine(policy.ModeCanary), ex, verify.New())
	p.Owned = reg.List()

	vs := []classify.Verdict{{GroupKey: "claude", Bucket: classify.LeakSuspicion,
		Confidence: 0.8, GrowthMB: 900, Basis: []string{"sustained 1500s, activity flat"}}}
	results := p.Decide(context.Background(), sysSample(time.Now(), 4000),
		PressureView{Level: model.LevelCritical, Score: 0.97, Quality: model.QualGood},
		model.Forecast{Actionable: true, Confidence: 0.9, Calibrated: true, Bound: true}, vs)

	var l2 *model.ActionResult
	for i := range results {
		if results[i].Kind == model.ActionStopOwned {
			l2 = &results[i]
		}
	}
	if l2 == nil {
		t.Fatal("L2 candidate never reached the executor for an owned, authorized workload")
	}
	if l2.State != "executed" {
		t.Fatalf("L2 state %q: %v", l2.State, l2.Evidence)
	}

	// SIGTERM to the group: wait for the process to actually exit.
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("owned process still running 10 s after L2 executed")
	}
	// The process must be gone from the table, not merely signaled.
	if err := syscall.Kill(int(pid), 0); err == nil {
		t.Fatal("pid still exists after L2")
	}
	// Independently confirm from the process table.
	out, _ := exec.Command("ps", "-o", "pid=,stat=", "-p", itoa(pid)).Output()
	if len(out) > 0 && !strings.Contains(string(out), "Z") {
		t.Fatalf("process table still lists %s: %s", itoa(pid), out)
	}
	if _, ok := reg.Lookup(pid); !ok {
		// Registration is cleared by the wrapper's admit_exit, not by the
		// daemon; either way the stop itself must have been recorded.
		t.Log("registry already cleared")
	}
}

// The same shape must refuse when the target has no authorization token —
// the positive test above is only meaningful if the negative one holds.
func TestL2RefusesRealProcessWithoutAuthorization(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "sleep 60")
	if err := cmd.Start(); err != nil {
		t.Fatalf("spawn: %v", err)
	}
	pid := int32(cmd.Process.Pid)
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

	reg := executor.NewRegistry()
	reg.Register(executor.OwnedPid{Pid: pid, Pgid: pid, Auth: ""}) // no --allow-stop
	ex := executor.New(&executor.ActionGuard{
		Mode:     func() policy.Mode { return policy.ModeCanary },
		Registry: reg, MaxAge: 30 * time.Second,
	})
	ar := ex.Execute(context.Background(), executor.Decision{
		ID: "neg", IssuedAt: time.Now(),
		Candidate: model.Candidate{Kind: model.ActionStopOwned,
			TargetID: "owned:" + itoa(pid)},
	})
	if ar.State != "refused" {
		t.Fatalf("unauthorized stop was not refused: %+v", ar)
	}
	if err := syscall.Kill(int(pid), 0); err != nil {
		t.Fatal("refused action still killed the process")
	}
	_ = os.Getpid
}

func itoa(pid int32) string {
	var b [12]byte
	i := len(b)
	for pid > 0 {
		i--
		b[i] = byte('0' + pid%10)
		pid /= 10
	}
	return string(b[i:])
}
