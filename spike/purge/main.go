// Spike S-6 (ADR-006 experiment): purge effectiveness.
// 1. Baseline VM stats (external/file-backed, internal, compressor, free).
// 2. Warm-read a large existing file set; time it.
// 3. Induce pressure with a controlled allocator (no purge request issued).
// 4. Re-read the same files; time the re-warm. Re-sample VM stats at each step.
// Demonstrates: the kernel already evicts file cache under pressure
// (external_page_count falls with no purge call), and re-warm pays a real cost.
package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"
)

var addrHostStats, addrMachHostSelf uintptr

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

func vm() VmStats64 {
	var st VmStats64
	cnt := uint32(38)
	host, _, _ := purego.SyscallN(addrMachHostSelf)
	purego.SyscallN(addrHostStats, host, 4, uintptr(unsafe.Pointer(&st)),
		uintptr(unsafe.Pointer(&cnt)))
	return st
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func collectFiles(root string, targetBytes uint64) []string {
	var files []string
	var total uint64
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, e := d.Info(); e == nil && info.Mode().IsRegular() && info.Size() > 1<<20 {
			if total < targetBytes {
				files = append(files, p)
				total += uint64(info.Size())
			}
		}
		return nil
	})
	return files
}

func readAll(files []string) time.Duration {
	start := time.Now()
	buf := make([]byte, 1<<20)
	for _, f := range files {
		fp, err := os.Open(f)
		if err != nil {
			continue
		}
		for {
			_, e := fp.Read(buf)
			if e != nil {
				break
			}
		}
		fp.Close()
	}
	return time.Since(start)
}

func row(label string, before, after VmStats64) {
	fmt.Printf("%-28s external(filecache)=%5dMB (Δ%+d) internal=%5dMB (Δ%+d) compressor=%5dMB (Δ%+d) free+spec=%4dMB\n",
		label,
		after.External/256, int64(after.External)/256-int64(before.External)/256,
		after.Internal/256, int64(after.Internal)/256-int64(before.Internal)/256,
		after.CompressorPage/256, int64(after.CompressorPage)/256-int64(before.CompressorPage)/256,
		(after.Free+after.Speculative)/256)
}

func main() {
	libc, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
	must(err)
	for _, r := range []struct {
		sym string
		p   *uintptr
	}{
		{"host_statistics64", &addrHostStats},
		{"mach_host_self", &addrMachHostSelf},
	} {
		a, err := purego.Dlsym(libc, r.sym)
		must(err)
		*r.p = a
	}

	base := vm()
	fmt.Println("baseline:", base.External/256, "MB filecache,", base.Internal/256, "MB anon")

	files := collectFiles("/usr/local", 3<<30)
	if len(files) < 5 {
		files = collectFiles("/Applications", 3<<30)
	}
	fmt.Printf("warm set: %d files\n", len(files))
	if len(files) == 0 {
		os.Exit(1)
	}

	tWarm := readAll(files)
	warm := vm()
	row("after warm read", base, warm)
	fmt.Printf("warm read time: %v\n", tWarm.Round(time.Millisecond))

	// controlled allocator: +6GB touched, no purge call anywhere
	keep := make([][]byte, 0, 12)
	for i := 0; i < 12; i++ {
		b := make([]byte, 512<<20)
		for j := 0; j < len(b); j += 4096 {
			b[j] = 1
		}
		keep = append(keep, b)
	}
	under := vm()
	row("after +6GB allocator (no purge issued)", warm, under)

	tRewarm := readAll(files)
	after := vm()
	row("after re-read (allocator released)", under, after)
	fmt.Printf("re-warm read time: %v (slowdown vs warm: %.1fx)\n",
		tRewarm.Round(time.Millisecond), float64(tRewarm)/float64(tWarm))
	keep = nil
}
