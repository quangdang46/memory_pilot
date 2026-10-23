// Package supervisor: single-instance lock (SI-8), shutdown order
// (intake → collector → ledger flush → IPC close), and self-protective exit.
package supervisor

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// SingleInstance takes an exclusive flock on <dataRoot>/mempilot.lock.
// Returns the lock file (hold it for process lifetime) or an error (SI-8).
func SingleInstance(dataRoot string) (*os.File, error) {
	if err := os.MkdirAll(dataRoot, 0o755); err != nil {
		return nil, err
	}
	p := filepath.Join(dataRoot, "mempilot.lock")
	f, err := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("another mempilotd instance holds %s (SI-8): %w", p, err)
	}
	f.Truncate(0)
	fmt.Fprintf(f, "%d\n", os.Getpid())
	return f, nil
}
