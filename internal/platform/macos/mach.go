//go:build darwin

// Pure-Go bindings into libSystem via purego (S-1: no cgo).
package macos

import (
	"unsafe"

	"github.com/ebitengine/purego"
)

const (
	HOST_VM_INFO64  = 4  // <mach/host_info.h> — NOT 10
	vmStatsRev1Cnt  = 38 // kernel revision selector; see spike/mach/NOTES.md

	PROC_ALL_PIDS      = 1
	PROC_PIDTASKINFO   = 4
	PROC_PIDLISTFDS    = 1
	RUSAGE_INFO_V4     = 4
	kernInvalidArgument = 4
)

// vmStatistics64Rev1 mirrors XNU rev1 layout (38 integer_t slots) —
// every field we read lives in rev1. The count is an ABI revision
// selector, not a buffer size (spike/mach/NOTES.md).
type vmStatistics64Rev1 struct {
	Free, Active, Inactive, Wire          uint32
	ZeroFill, Reactivations, Pageins, Pageouts uint64
	Faults, CowFaults, Lookups, Hits      uint64
	Purges                                uint64
	Purgeable, Speculative                uint32
	Decompressions, Compressions, Swapins, Swapouts uint64
	CompressorPage, Throttled             uint32
	External, Internal                    uint32
	TotalUncompressedInCompressor         uint64
}

type procTaskInfo struct {
	VirtualSize, ResidentSize, ResidentMax, UserTime, SystemTime uint64
	Policy                                   int32
	Faults, Pageins, CowFaults               int32
	MessagesSent, MessagesReceived           uint64
	SyscallsMach, SyscallsBSD, Csw           uint64
	Threadnum, Numrunning, Priority          int32
}

type rusageInfoV4 struct {
	UUID                       [16]byte
	UserTime, SystemTime       uint64
	PkgIdleWkups, IntWkups     uint64
	Pageins, WiredSize         uint64
	ResidentSize, PhysFootprint uint64
	ProcStart, ProcExit        uint64
	_                          [34]uint64
}

var (
	addrMachHostSelf     uintptr
	addrHostStatistics64 uintptr
	addrMachPortDealloc  uintptr
	addrProcListPids     uintptr
	addrProcPidinfo      uintptr
	addrProcPidRusage    uintptr
	addrSysctlbyname     uintptr
)

func loadLibSystem() error {
	libc, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
	if err != nil {
		return err
	}
	for _, e := range []struct {
		sym string
		p   *uintptr
	}{
		{"mach_host_self", &addrMachHostSelf},
		{"host_statistics64", &addrHostStatistics64},
		{"mach_port_deallocate", &addrMachPortDealloc},
		{"proc_listpids", &addrProcListPids},
		{"proc_pidinfo", &addrProcPidinfo},
		{"proc_pid_rusage", &addrProcPidRusage},
		{"sysctlbyname", &addrSysctlbyname},
	} {
		a, err := purego.Dlsym(libc, e.sym)
		if err != nil {
			return err
		}
		*e.p = a
	}
	return nil
}

func hostStats() (vmStatistics64Rev1, int) {
	var st vmStatistics64Rev1
	cnt := uint32(vmStatsRev1Cnt)
	host, _, _ := purego.SyscallN(addrMachHostSelf)
	ret, _, _ := purego.SyscallN(addrHostStatistics64, host, HOST_VM_INFO64,
		uintptr(unsafe.Pointer(&st)), uintptr(unsafe.Pointer(&cnt)))
	purego.SyscallN(addrMachPortDealloc, host)
	return st, int(ret)
}

// sysctlInt32 reads an integer sysctl; ok=false when absent (S-3: two
// memorystatus sysctls do not exist on all builds — absent is not zero).
func sysctlInt32(name string) (int32, bool) {
	c := append([]byte(name), 0)
	var val int32
	var size uint64 = 4
	r, _, _ := purego.SyscallN(addrSysctlbyname,
		uintptr(unsafe.Pointer(&c[0])), uintptr(unsafe.Pointer(&val)),
		uintptr(unsafe.Pointer(&size)), 0, 0, 0)
	if r != 0 || size != 4 {
		return 0, false
	}
	return val, true
}

func sysctlUint64(name string) (uint64, bool) {
	c := append([]byte(name), 0)
	var val uint64
	var size uint64 = 8
	r, _, _ := purego.SyscallN(addrSysctlbyname,
		uintptr(unsafe.Pointer(&c[0])), uintptr(unsafe.Pointer(&val)),
		uintptr(unsafe.Pointer(&size)), 0, 0, 0)
	if r != 0 || size != 8 {
		return 0, false
	}
	return val, true
}

func listPids() []int32 {
	buf := make([]int32, 8192)
	n, _, _ := purego.SyscallN(addrProcListPids, PROC_ALL_PIDS, 0,
		uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)*4), 0, 0)
	if n == 0 || n > uintptr(len(buf)*4) {
		return nil
	}
	return buf[:n/4]
}

func taskInfo(pid int32) (procTaskInfo, bool) {
	var ti procTaskInfo
	r, _, _ := purego.SyscallN(addrProcPidinfo, uintptr(pid), PROC_PIDTASKINFO, 0,
		uintptr(unsafe.Pointer(&ti)), unsafe.Sizeof(ti), 0)
	return ti, r >= 96 // kernel copies sizeof(proc_taskinfo)=96
}

func rusageV4(pid int32) (rusageInfoV4, bool) {
	var ru rusageInfoV4
	r, _, _ := purego.SyscallN(addrProcPidRusage, uintptr(pid), RUSAGE_INFO_V4,
		uintptr(unsafe.Pointer(&ru)), 0, 0, 0)
	return ru, r == 0
}
