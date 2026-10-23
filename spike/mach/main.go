// Spike S-1: Can Go call host_statistics64 + proc_listpids + proc_pidinfo without cgo?
// Method: purego (CGO_ENABLED=0) — assembly trampolines into /usr/lib/libSystem.B.dylib.
// Build: CGO_ENABLED=0 go build -o mach main.go
package main

import (
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
)

const (
	libSystem      = "/usr/lib/libSystem.B.dylib"
	HOST_VM_INFO64 = 4 // flavor — per <mach/host_info.h>; NOT 10
)

// vm_statistics64 rev1 layout: all fields through total_uncompressed_pages_in_compressor
// (the fields Memory Pilot reads). 38 integer_t slots. The kernel validates the count
// against known ABI revisions and rejects unknown ones with KERN_INVALID_ARGUMENT.
type VmStatistics64Rev1 struct {
	FreeCount, ActiveCount, InactiveCount, WireCount uint32
	ZeroFillCount, Reactivations, Pageins, Pageouts  uint64
	Faults, CowFaults, Lookups, Hits                 uint64
	Purges                                           uint64
	PurgeableCount, SpeculativeCount                 uint32
	Decompressions, Compressions, Swapins, Swapouts  uint64
	CompressorPageCount, ThrottledCount              uint32
	ExternalPageCount, InternalPageCount             uint32
	TotalUncompressedPagesInCompressor               uint64
}

var (
	addrMachHostSelf, addrHostStatistics64, addrMachPortDeallocate,
	addrProcListPids, addrProcPidinfo uintptr
)

var rev1Count = binary.Size(VmStatistics64Rev1{}) / 4

type vmStats struct {
	free, active, inactive, wire, speculative, compressor, throttled uint32
}

func hostStats(count int) (VmStatistics64Rev1, int) {
	var st VmStatistics64Rev1
	cnt := uint32(count)
	host, _, _ := purego.SyscallN(addrMachHostSelf)
	ret, _, _ := purego.SyscallN(addrHostStatistics64, host, HOST_VM_INFO64,
		uintptr(unsafe.Pointer(&st)), uintptr(unsafe.Pointer(&cnt)))
	purego.SyscallN(addrMachPortDeallocate, host)
	if ret != 0 {
		return VmStatistics64Rev1{}, int(ret)
	}
	return st, 0
}

func main() {
	libc, err := purego.Dlopen(libSystem, purego.RTLD_LAZY|purego.RTLD_GLOBAL)
	must(err)
	for _, r := range []struct {
		sym string
		p   *uintptr
	}{
		{"mach_host_self", &addrMachHostSelf},
		{"host_statistics64", &addrHostStatistics64},
		{"mach_port_deallocate", &addrMachPortDeallocate},
		{"proc_listpids", &addrProcListPids},
		{"proc_pidinfo", &addrProcPidinfo},
	} {
		a, err := purego.Dlsym(libc, r.sym)
		must(err)
		*r.p = a
	}

	fmt.Printf("struct slot count (size/4): %d (kernel revisions: REV0=24, REV1=38)\n", rev1Count)

	st, ret := hostStats(38)
	fmt.Printf("host_statistics64(HOST_VM_INFO64=4) count=38: kern_return=%d\n", ret)
	fmt.Printf("  free=%d active=%d inactive=%d wire=%d speculative=%d compressor=%d throttled=%d\n",
		st.FreeCount, st.ActiveCount, st.InactiveCount, st.WireCount,
		st.SpeculativeCount, st.CompressorPageCount, st.ThrottledCount)
	if ret != 0 {
		fmt.Println("FAIL: count=38 rejected")
		os.Exit(1)
	}

	fmt.Println("count boundary scan (kern_return; 0 = accepted, 4 = KERN_INVALID_ARGUMENT):")
	for _, c := range []int{20, 22, 23, 24, 25, 37, 38, 39, 48, 56, 57, 90} {
		_, ret := hostStats(c)
		fmt.Printf("  count=%d -> %d\n", c, ret)
	}

	if out, err := exec.Command("/usr/bin/vm_stat").Output(); err == nil {
		reFree := regexp.MustCompile(`Pages free:\s+([0-9]+)`)
		reComp := regexp.MustCompile(`Pages occupied by compressor:\s+([0-9]+)`)
		if m := reFree.FindSubmatch(out); m != nil {
			v, _ := strconv.ParseUint(string(m[1]), 10, 64)
			fmt.Printf("vm_stat Pages free=%d (spike free=%d; vm_stat free includes speculative) \n", v, st.FreeCount+st.SpeculativeCount)
		}
		if m := reComp.FindSubmatch(out); m != nil {
			v, _ := strconv.ParseUint(string(m[1]), 10, 64)
			fmt.Printf("vm_stat compressor=%d (spike compressor=%d)\n", v, st.CompressorPageCount)
		}
	}

	pids := listAllPids()
	few := pids
	if len(few) > 5 {
		few = few[:5]
	}
	fmt.Printf("proc_listpids: %d pids, first few: %v\n", len(pids), few)

	rss, vsz, err := pidTaskInfo(os.Getpid())
	fmt.Printf("proc_pidinfo(PROC_PIDTASKINFO) pid=%d err=%v rss=%.1fMB vsz=%.1fMB\n",
		os.Getpid(), err, float64(rss)/1e6, float64(vsz)/1e6)

	if len(pids) > 0 {
		const iter = 2000
		start := time.Now()
		for i := 0; i < iter; i++ {
			_, _, _ = pidTaskInfo(int(pids[i%len(pids)]))
		}
		per := time.Since(start) / iter
		fmt.Printf("proc_pidinfo cost: %v per call (N=%d pids -> %v/sample cycle)\n",
			per, len(pids), per*time.Duration(len(pids)))
	}
}

func listAllPids() []int32 {
	const PROC_ALL_PIDS = 1
	buf := make([]int32, 4096)
	n, _, _ := purego.SyscallN(addrProcListPids, PROC_ALL_PIDS, 0,
		uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)*4), 0, 0)
	if n == 0 || n > uintptr(len(buf)*4) {
		fmt.Println("proc_listpids failed")
		os.Exit(1)
	}
	cnt := int(n / 4)
	out := make([]int32, cnt)
	for i := 0; i < cnt; i++ {
		out[i] = buf[i]
	}
	return out
}

func pidTaskInfo(pid int) (rss, vsz uint64, err error) {
	// full proc_taskinfo buffer (starts with pti_virtual_size, pti_resident_size)
	var buf [192]byte
	want := unsafe.Sizeof(buf)
	r, _, _ := purego.SyscallN(addrProcPidinfo, uintptr(pid), 4 /*PROC_PIDTASKINFO*/, 0,
		uintptr(unsafe.Pointer(&buf[0])), want, 0)
	if r < 16 {
		return 0, 0, fmt.Errorf("proc_pidinfo returned %d", r)
	}
	vsz = *(*uint64)(unsafe.Pointer(&buf[0]))
	rss = *(*uint64)(unsafe.Pointer(&buf[8]))
	return rss, vsz, nil
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
