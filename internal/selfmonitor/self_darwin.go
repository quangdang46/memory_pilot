//go:build darwin

package selfmonitor

import (
	"unsafe"

	"github.com/ebitengine/purego"
)

var (
	addrProcPidRusage uintptr
	loaded            = false
)

func ensureLoaded() {
	if loaded {
		return
	}
	libc, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
	if err != nil {
		return
	}
	if a, err := purego.Dlsym(libc, "proc_pid_rusage"); err == nil {
		addrProcPidRusage = a
	}
	loaded = true
}

type rusageV4 struct {
	UUID       [16]byte
	UserTime, SystemTime uint64
	PkgIdleWkups, IntWkups uint64
	Pageins, WiredSize uint64
	ResidentSize, PhysFootprint uint64
	_          [21]uint64
}

func selfRSSBytes() uint64 {
	ensureLoaded()
	if addrProcPidRusage == 0 {
		return 0
	}
	var ru rusageV4
	r, _, _ := purego.SyscallN(addrProcPidRusage, uintptr(ownPid()), 4, uintptr(unsafe.Pointer(&ru)), 0, 0, 0)
	if r != 0 {
		return 0
	}
	return ru.PhysFootprint
}

func selfCPURusage() (user, sys float64) {
	ensureLoaded()
	if addrProcPidRusage == 0 {
		return 0, 0
	}
	var ru rusageV4
	r, _, _ := purego.SyscallN(addrProcPidRusage, uintptr(ownPid()), 4, uintptr(unsafe.Pointer(&ru)), 0, 0, 0)
	if r != 0 {
		return 0, 0
	}
	return float64(ru.UserTime) / 1e9, float64(ru.SystemTime) / 1e9
}

func ownPid() int32 { return int32(pidSelf()) }
