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
// it targets exclusively the process group recorded at our own spawn time,
// after OwnedPid + pre-auth revalidation. Foreign processes are structurally
// unreachable — the pgid comes only from the daemon's own spawn record.
// The ST-1 gate script whitelists exactly this one site.
func stopOwnedChild(ctx context.Context, o model.OwnedPidRef) string {
	if o.Pgid <= 1 {
		return "refusing to signal group<=1"
	}
	done := make(chan error, 1)
	go func() {
		// SINGLE AUDITED SIGNAL CALL SITE — SIGTERM to our own child's group
		done <- syscall.Kill(int(-o.Pgid), syscall.SIGTERM)
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
