//go:build darwin

// Pure-Go bindings into libSystem via purego (S-1: no cgo).
package macos

import (
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

const (
	HOST_VM_INFO64 = 4  // <mach/host_info.h> — NOT 10
	vmStatsRev1Cnt = 38 // kernel revision selector; see spike/mach/NOTES.md

	PROC_ALL_PIDS       = 1
	PROC_PIDTASKINFO    = 4
	PROC_PIDLISTFDS     = 1
	PROC_PIDTBSDINFO    = 3
	RUSAGE_INFO_V4      = 4
	kernInvalidArgument = 4

	maxComLen = 16 // MAXCOMLEN
)

// procBSDidInfo mirrors <sys/proc_info.h> struct proc_bsdinfo. Field order is
// ABI: the kernel copies sizeof(struct proc_bsdinfo) bytes; reading a shorter
// buffer yields 0 (same failure mode documented in spike/mach/NOTES.md).
type procBSDidInfo struct {
	Flags, Status, Xstatus     uint32
	Pid, Ppid                  uint32
	Uid, Gid, Ruid, Rgid       uint32
	Svuid, Svgid               uint32
	Rfu1                       uint32
	Comm                       [maxComLen]byte
	Name                       [2 * maxComLen]byte
	Nfiles, Pgid, Pjobc, ETdev uint32
	ETpgid                     uint32
	Nice                       int32
	StartTvsec                 uint64
	StartTvusec                uint64
}

// vmStatistics64Rev1 mirrors XNU rev1 layout (38 integer_t slots) —
// every field we read lives in rev1. The count is an ABI revision
// selector, not a buffer size (spike/mach/NOTES.md).
type vmStatistics64Rev1 struct {
	Free, Active, Inactive, Wire                    uint32
	ZeroFill, Reactivations, Pageins, Pageouts      uint64
	Faults, CowFaults, Lookups, Hits                uint64
	Purges                                          uint64
	Purgeable, Speculative                          uint32
	Decompressions, Compressions, Swapins, Swapouts uint64
	CompressorPage, Throttled                       uint32
	External, Internal                              uint32
	TotalUncompressedInCompressor                   uint64
}

type procTaskInfo struct {
	VirtualSize, ResidentSize, ResidentMax, UserTime, SystemTime uint64
	Policy                                                       int32
	Faults, Pageins, CowFaults                                   int32
	MessagesSent, MessagesReceived                               uint64
	SyscallsMach, SyscallsBSD, Csw                               uint64
	Threadnum, Numrunning, Priority                              int32
}

type rusageInfoV4 struct {
	UUID                        [16]byte
	UserTime, SystemTime        uint64
	PkgIdleWkups, IntWkups      uint64
	Pageins, WiredSize          uint64
	ResidentSize, PhysFootprint uint64
	ProcStart, ProcExit         uint64
	_                           [34]uint64
}

var (
	addrMachHostSelf     uintptr
	addrHostStatistics64 uintptr
	addrMachPortDealloc  uintptr
	addrProcListPids     uintptr
	addrProcPidinfo      uintptr
	addrProcPidRusage    uintptr
	addrSysctlbyname     uintptr
	addrMachContinuous   uintptr
	addrMachTimebase     uintptr
	addrProcPidPath      uintptr

	timebaseOnce sync.Once
	timebaseNum  uint64 = 1
	timebaseDen  uint64 = 1
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
		{"proc_pidpath", &addrProcPidPath},
		{"mach_continuous_time", &addrMachContinuous},
		{"mach_timebase_info", &addrMachTimebase},
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

// bsdInfo supplies the identity tuple (start time) and the ancestry
// (ppid) that attribution groups by. Coverage counts denied reads rather
// than substituting zeros (SI-4).
func bsdInfo(pid int32) (procBSDidInfo, bool) {
	var bi procBSDidInfo
	r, _, _ := purego.SyscallN(addrProcPidinfo, uintptr(pid), PROC_PIDTBSDINFO, 0,
		uintptr(unsafe.Pointer(&bi)), unsafe.Sizeof(bi), 0)
	return bi, r >= unsafe.Sizeof(bi)
}

// bsdUID returns the real uid of a process and whether it was readable. Used
// to separate "we were never allowed to read this" (foreign user) from "we
// should have been able to read this and could not" (real visibility loss).
// Root and other users' processes report ok=false here, which is exactly the
// case where a permission denial is not a visibility failure.
func bsdUID(pid int32) (int, bool) {
	bi, ok := bsdInfo(pid)
	if !ok {
		return -1, false
	}
	return int(bi.Uid), true
}

// executablePath returns the full path of the process image via
// proc_pidpath(2). comm is not usable for identification: measured live, three
// `claude` processes reported comm "2.1.310" — the bundle name, not the process
// name — so any protection list keyed on comm missed them entirely.
func executablePath(pid int32) (string, bool) {
	if addrProcPidPath == 0 {
		return "", false
	}
	buf := make([]byte, 4096) // PROC_PIDPATHINFO_MAXSIZE
	r, _, _ := purego.SyscallN(addrProcPidPath, uintptr(pid),
		uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if r <= 0 {
		return "", false
	}
	return string(buf[:int(r)]), true
}

// commName returns pbi_comm (MAXCOMLEN, NUL-padded). The kernel truncates
// long paths to the process name; that is the only name readable for
// processes we do not own.
func (b procBSDidInfo) commName() string {
	buf := b.Comm[:]
	for i, c := range buf {
		if c == 0 {
			return string(buf[:i])
		}
	}
	return string(buf)
}

// machContinuousMs is the monotonic clock every rate computation depends on.
// mach_continuous_time keeps counting across sleep, so a laptop that suspends
// for an hour does not produce a 3600 s "gap" that reads as a rate spike.
// Previously this field was never populated (always 0), which made the
// pressure dwell gate unfalsifiable and latched Critical forever.
func machContinuousMs() int64 {
	timebaseOnce.Do(func() {
		var tb struct{ Numer, Denom uint32 }
		if addrMachTimebase != 0 {
			purego.SyscallN(addrMachTimebase, uintptr(unsafe.Pointer(&tb)))
			if tb.Numer > 0 && tb.Denom > 0 {
				timebaseNum, timebaseDen = uint64(tb.Numer), uint64(tb.Denom)
			}
		}
	})
	if addrMachContinuous == 0 {
		return 0
	}
	t, _, _ := purego.SyscallN(addrMachContinuous)
	// ns = ticks * numer / denom; keep the multiplication in 128-bit-safe
	// order by dividing first (numer/denom is 1/1 on arm64, 1/1 or similar
	// elsewhere — the split keeps us exact for the integer part).
	ticks := uint64(t)
	ns := (ticks / timebaseDen) * timebaseNum
	rem := (ticks % timebaseDen) * timebaseNum / timebaseDen
	return int64(ns+rem) / 1e6
}
