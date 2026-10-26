//go:build darwin

package macos

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// LaunchdJobPids returns the pids launchd is currently managing as jobs.
//
// This is the evidence that separates a real orphan from a launchd service,
// and it is the only one available: macOS reparents both to pid 1, so ppid
// alone cannot tell them apart. Measured on this machine — three `claude`
// processes, Finder, and wezterm all appear in `launchctl list`; a child whose
// parent exited does not. Without this, either every launchd service looks
// abandoned or no orphan ever does; the first live run chose the former and
// named Finder, the VM and every Chrome helper.
func LaunchdJobPids(ctx context.Context) (map[int32]bool, bool) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/bin/launchctl", "list").Output()
	if err != nil {
		return nil, false // not evidence either way; the caller must not guess
	}
	pids := make(map[int32]bool, 256)
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		// Columns: PID  Status  Label. A "-" pid means the job is not running.
		if pid, err := strconv.Atoi(fields[0]); err == nil && pid > 0 {
			pids[int32(pid)] = true
		}
	}
	return pids, true
}
