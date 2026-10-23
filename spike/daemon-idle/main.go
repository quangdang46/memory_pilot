// Spike S-4: idle RSS/CPU/FD/goroutines of an empty daemon: ticker + JSONL writer.
// Measures itself via getrusage + proc_pidinfo(PROC_PIDTASKINFO), logs a JSONL line
// every 5s for a configurable duration (default 300s), then prints a summary.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
)

var addrProcPidinfo, addrHostStats, addrMachHostSelf uintptr

type VmStats64 struct {
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

type line struct {
	T          time.Time `json:"t"`
	Goroutines int       `json:"goroutines"`
	FDs        int       `json:"fds"`
	RssMB      float64   `json:"rss_mb"`
	HeapMB     float64   `json:"heap_mb"`
	UserS      float64   `json:"user_s"`
	SysS       float64   `json:"sys_s"`
}

func fdCount() int {
	// proc_pidinfo(PROC_PIDLISTFDS)
	var buf [4096]byte
	r, _, _ := purego.SyscallN(addrProcPidinfo, uintptr(os.Getpid()), 1 /*PROC_PIDLISTFDS*/, 0,
		uintptr(unsafe.Pointer(&buf[0])), unsafe.Sizeof(buf), 0)
	return int(r / 4)
}

func selfRss() (rss, vsz uint64) {
	var buf [192]byte
	r, _, _ := purego.SyscallN(addrProcPidinfo, uintptr(os.Getpid()), 4, 0,
		uintptr(unsafe.Pointer(&buf[0])), unsafe.Sizeof(buf), 0)
	if r >= 16 {
		vsz = *(*uint64)(unsafe.Pointer(&buf[0]))
		rss = *(*uint64)(unsafe.Pointer(&buf[8]))
	}
	return
}

func main() {
	libc, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
	must(err)
	for _, r := range []struct {
		sym string
		p   *uintptr
	}{
		{"proc_pidinfo", &addrProcPidinfo},
		{"host_statistics64", &addrHostStats},
		{"mach_host_self", &addrMachHostSelf},
	} {
		a, err := purego.Dlsym(libc, r.sym)
		must(err)
		*r.p = a
	}

	dur := 300 * time.Second
	if len(os.Args) > 1 {
		d, err := time.ParseDuration(os.Args[1])
		if err == nil {
			dur = d
		}
	}
	f, err := os.Create("idle.jsonl")
	must(err)
	defer f.Close()
	enc := json.NewEncoder(f)
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	deadline := time.Now().Add(dur)
	n := 0
	for time.Now().Before(deadline) {
		<-tick.C
		rss, _ := selfRss()
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		l := line{T: time.Now().UTC(), Goroutines: runtime.NumGoroutine(), FDs: fdCount(),
			RssMB: float64(rss) / 1e6, HeapMB: float64(ms.HeapInuse) / 1e6}
		must(enc.Encode(l))
		n++
	}
	must(f.Sync())
	rss, _ := selfRss()
	fmt.Printf("samples=%d final_rss_mb=%.1f goroutines=%d fds=%d heap_inuse_mb=%.1f duration=%v\n",
		n, float64(rss)/1e6, runtime.NumGoroutine(), fdCount(), func() float64 {
			var ms runtime.MemStats
			runtime.ReadMemStats(&ms)
			return float64(ms.HeapInuse) / 1e6
		}(), dur)
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
