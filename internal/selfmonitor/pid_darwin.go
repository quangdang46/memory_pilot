//go:build darwin

package selfmonitor

import "os"

func pidSelf() int { return os.Getpid() }
