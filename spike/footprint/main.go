// Spike S-5 (per-PID sampling cost at N=600) + S-7 (footprint source choice).
// Compares proc_pidinfo(PROC_PIDTASKINFO) resident_size vs proc_pid_rusage
// RUSAGE_INFO_V4 phys_footprint/resident_size, times both at scale, and
// cross-checks totals against /usr/bin/memory_pressure and vm_stat.
package main

import (
	"fmt"
	"os"
	"sort"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
)

var addrProcListPids, addrProcPidinfo, addrProcPidRusage uintptr

const (
	PROC_ALL_PIDS    = 1
	PROC_PIDTASKINFO = 4
	RUSAGE_INFO_V4   = 4
)

type taskInfo struct {
	VirtualSize, ResidentSize, ResidentMax, UserTime, SystemTime uint64
	Policy                                                       int32
	Faults, Pageins, CowFaults                                   int32
	MessagesSent, MessagesReceived, SyscallsMach, SyscallsBSD    uint64
	Csw                                                          uint64
	Threadnum, Numrunning, Priority                              int32
}

// rusage_info_v4 per <sys/resource.h>
type rusageV4 struct {
	UUID                        [16]byte
	UserTime, SystemTime        uint64
	PkgIdleWkups, IntWkups      uint64
	Pageins, WiredSize          uint64
	ResidentSize, PhysFootprint uint64
	ProcStart, ProcExit         uint64
	// ... remaining fields unused; buffer is 60 uint64s
	_ [34]uint64
}

func listAllPids() []int32 {
	buf := make([]int32, 8192)
	n, _, _ := purego.SyscallN(addrProcListPids, PROC_ALL_PIDS, 0,
		uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)*4), 0, 0)
	return buf[:n/4]
}

func readTask(pid int32) (taskInfo, bool) {
	var ti taskInfo
	r, _, _ := purego.SyscallN(addrProcPidinfo, uintptr(pid), PROC_PIDTASKINFO, 0,
		uintptr(unsafe.Pointer(&ti)), unsafe.Sizeof(ti), 0)
	return ti, r >= 96 /* kernel copies sizeof(proc_taskinfo)=96 */
}

func readRusage(pid int32) (rusageV4, bool) {
	var ru rusageV4
	r, _, _ := purego.SyscallN(addrProcPidRusage, uintptr(pid), RUSAGE_INFO_V4,
		uintptr(unsafe.Pointer(&ru)), 0, 0, 0)
	return ru, r == 0
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func main() {
	libc, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
	must(err)
	for _, r := range []struct {
		sym string
		p   *uintptr
	}{
		{"proc_listpids", &addrProcListPids},
		{"proc_pidinfo", &addrProcPidinfo},
		{"proc_pid_rusage", &addrProcPidRusage},
	} {
		a, err := purego.Dlsym(libc, r.sym)
		must(err)
		*r.p = a
	}

	pids := listAllPids()
	fmt.Printf("live pids: %d\n", len(pids))

	// Build an N=600 workload: real pids padded by repeats to 600.
	n := 600
	work := make([]int32, 0, n)
	for i := 0; i < n; i++ {
		work = append(work, pids[i%len(pids)])
	}

	// S-5: per-pid cost, both APIs
	const passes = 3
	for _, api := range []struct {
		name string
		fn   func(int32) bool
	}{
		{"proc_pidinfo(TASKINFO)", func(p int32) bool { _, ok := readTask(p); return ok }},
		{"proc_pid_rusage(V4)", func(p int32) bool { _, ok := readRusage(p); return ok }},
	} {
		var best time.Duration
		for pass := 0; pass < passes; pass++ {
			start := time.Now()
			okc := 0
			for _, p := range work {
				if api.fn(p) {
					okc++
				}
			}
			el := time.Since(start)
			if pass == 0 || el < best {
				best = el
			}
			if pass == passes-1 {
				per := el / time.Duration(n)
				fmt.Printf("%s: N=600 cycle=%v best=%v, per-pid=%v, ok=%d/600\n",
					api.name, el.Round(time.Microsecond), best.Round(time.Microsecond), per, okc)
				fmt.Printf("  -> cycles/s at 5s cadence budget: 5s/%v = %.1f%% of one core\n",
					best.Round(time.Microsecond), float64(best)/5e9*100)
			}
		}
	}

	// S-7: footprint sources on live processes
	type row struct {
		pid        int32
		resident   uint64 // taskinfo
		ruResident uint64
		footprint  uint64
	}
	var rows []row
	for _, p := range pids {
		ti, ok1 := readTask(p)
		ru, ok2 := readRusage(p)
		if ok1 && ok2 && ru.PhysFootprint > 0 {
			rows = append(rows, row{p, ti.ResidentSize, ru.ResidentSize, ru.PhysFootprint})
		}
	}
	// agreement between the two APIs on resident size
	var agree, total int
	var sumDiff uint64
	for _, r := range rows {
		d := r.resident
		if d > r.ruResident {
			d = d - r.ruResident
		} else {
			d = r.ruResident - d
		}
		total++
		if d < 1<<20 { // <1MB
			agree++
		}
		sumDiff += d
	}
	fmt.Printf("resident-size agreement taskinfo vs rusageV4: %d/%d within 1MB (avg |diff|=%.0fKB)\n",
		agree, total, float64(sumDiff)/float64(max(total, 1))/1024)

	// footprint vs resident distribution: top 10 by footprint
	sort.Slice(rows, func(i, j int) bool { return rows[i].footprint > rows[j].footprint })
	fmt.Println("top 10 by phys_footprint (MB): pid | footprint | resident(taskinfo) | resident(rusage)")
	for i := 0; i < 10 && i < len(rows); i++ {
		r := rows[i]
		fmt.Printf("  %6d | %8.1f | %8.1f | %8.1f\n", r.pid,
			float64(r.footprint)/1e6, float64(r.resident)/1e6, float64(r.ruResident)/1e6)
	}
	var sumF, sumR uint64
	for _, r := range rows {
		sumF += r.footprint
		sumR += r.resident
	}
	fmt.Printf("sum footprint=%.2fGB sum resident=%.2fGB over %d readable pids\n",
		float64(sumF)/1e9, float64(sumR)/1e9, len(rows))
	os.Exit(0)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
