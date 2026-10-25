// Spike S-3: kern.memorystatus_vm_pressure_level values under induced pressure.
// Controlled allocator: ramps 512MB chunks, samples sysctls every 100ms,
// stops at CRITICAL or hard caps (time / bytes). Prints an observed value table.
package main

import (
	"fmt"
	"os"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
)

var start = time.Now()

var addrSysctlbyname, addrHostStats, addrMachHostSelf uintptr

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

func sysctlInt(name string) (int32, bool) {
	var val int32
	var size uint64 = 4
	ret, _, _ := purego.SyscallN(addrSysctlbyname,
		uintptr(unsafe.Pointer(unsafe.StringData(name))), // not null-terminated-safe; see below
		uintptr(unsafe.Pointer(&val)), uintptr(unsafe.Pointer(&size)), 0, 0, 0)
	_ = ret
	// fallback: use C string
	return val, ret == 0 && size == 4
}

func sysctlInt2(name string) int32 {
	c := append([]byte(name), 0)
	var val int32
	var size uint64 = 4
	r, _, _ := purego.SyscallN(addrSysctlbyname,
		uintptr(unsafe.Pointer(&c[0])), uintptr(unsafe.Pointer(&val)),
		uintptr(unsafe.Pointer(&size)), 0, 0, 0)
	if r != 0 || size != 4 {
		return -1
	}
	return val
}

func freePages() uint32 {
	var st VmStats64
	cnt := uint32(38)
	host, _, _ := purego.SyscallN(addrMachHostSelf)
	purego.SyscallN(addrHostStats, host, 4, uintptr(unsafe.Pointer(&st)),
		uintptr(unsafe.Pointer(&cnt)))
	purego.SyscallN(addrMachHostSelf) // no-op; port leak fine in spike
	return st.Free + st.Speculative
}

func main() {
	libc, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
	must(err)
	for _, r := range []struct {
		sym string
		p   *uintptr
	}{
		{"sysctlbyname", &addrSysctlbyname},
		{"host_statistics64", &addrHostStats},
		{"mach_host_self", &addrMachHostSelf},
	} {
		a, err := purego.Dlsym(libc, r.sym)
		must(err)
		*r.p = a
	}

	const chunk = 512 << 20
	const maxChunks = 24 // 8 GB hard cap
	type row struct {
		t      time.Duration
		gb     float64
		plevel int32
		mslvl  int32
		freeMB uint32
	}
	var rows []row
	record := func(gb float64) {
		rows = append(rows, row{time.Since(start).Round(time.Millisecond), gb,
			sysctlInt2("kern.memorystatus_vm_pressure_level"),
			sysctlInt2("kern.memorystatus_level"), freePages() / 256})
	}

	start := time.Now()
	stop := make(chan struct{})

	// sampler goroutine
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				close(done)
				return
			default:
			}
			record(-1)
			time.Sleep(100 * time.Millisecond)
		}
	}()

	keep := [][]byte{}
	for i := 0; i < maxChunks; i++ {
		b := make([]byte, chunk)
		for j := 0; j < len(b); j += 4096 {
			b[j] = 1
		}
		keep = append(keep, b)
		record(float64(i+1) / 2)
		pl := sysctlInt2("kern.memorystatus_vm_pressure_level")
		if pl == 4 {
			fmt.Println("hit CRITICAL; releasing")
			break
		}
		if time.Since(start) > 180*time.Second {
			fmt.Println("time cap; releasing")
			break
		}
	}
	close(stop)
	<-done
	keep = nil

	fmt.Println("t        allocatedGB  pressure_level  memorystatus_level  freeMB")
	for _, r := range rows {
		gb := r.gb
		if gb < 0 {
			gb = -1 // between chunks
		}
		fmt.Printf("%-8v %-11.1f %-15d %-19d %d\n", r.t, gb, r.plevel, r.mslvl, r.freeMB)
	}
	// distinct observed values
	seen := map[int32]bool{}
	for _, r := range rows {
		seen[r.plevel] = true
	}
	fmt.Println("distinct pressure_level values observed:", keys(seen))
}

func keys(m map[int32]bool) []int32 {
	var out []int32
	for k := range m {
		out = append(out, k)
	}
	if len(out) == 0 {
		os.Exit(1)
	}
	return out
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
