//go:build darwin

package executor

import (
	"context"
	"syscall"
	"time"

	"memory_pilot/internal/model"
)

// stopOwnedChild stops a MANAGED workload the daemon spawned via `mp run`.
// This is the single signal-capable call site in the entire binary (ST-1):
// it targets exclusively the process (or process group) recorded at our own
// spawn time, after OwnedPid + pre-auth revalidation. Foreign processes are
// structurally unreachable — the pid and pgid come only from the daemon's own
// spawn record. The ST-1 gate script whitelists exactly this one site.
//
// Group signalling needs the child to actually be a group leader. `mp run`
// spawns normally, so the child inherits the wrapper's group; signalling
// -Pgid in that case hits the *wrapper's* group (or nothing at all, which is
// what the on-device test observed: "no such process"). Resolve the real
// group at stop time and only use it when it belongs to this child.
func stopOwnedChild(ctx context.Context, o model.OwnedPidRef) string {
	if o.Pid <= 1 {
		return "refusing to signal pid<=1"
	}
	target := int(o.Pid)
	if o.Pgid > 1 && o.Pgid != o.Pid && isOwnGroup(o.Pid, o.Pgid) {
		target = -int(o.Pgid) // whole group, but only when it is genuinely his
	}
	done := make(chan error, 1)
	go func() {
		// SINGLE AUDITED SIGNAL CALL SITE — SIGTERM to the owned workload.
		done <- syscall.Kill(target, syscall.SIGTERM)
	}()
	select {
	case err := <-done:
		if err != nil {
			return "signal failed: " + err.Error()
		}
		return ""
	case <-ctx.Done():
		return "context cancelled"
	case <-time.After(5 * time.Second):
		return "signal timed out"
	}
}

// isOwnGroup reports whether the recorded pgid is led by the recorded pid.
func isOwnGroup(pid, pgid int32) bool {
	return pid == pgid
}
