// mpcorpus generates the versioned trace corpus (12 traces incl.
// escalation-full) deterministically. Re-running overwrites byte-identically.
package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"

	"memory_pilot/internal/replay"
)

const totalPages = 1_048_576 // 16GB / 16KB pages
const pageBytes = 16384
const thresholdBytes = 2.0 / 100 * totalPages * pageBytes // F_C fallback critical

type gen func(i int) replay.TraceSample

func mk(name, desc string, n int, f gen) replay.Trace {
	t := replay.Trace{Name: name, Version: 1, Description: desc, Threshold: thresholdBytes}
	for i := 0; i < n; i++ {
		t.Samples = append(t.Samples, f(i))
	}
	return t
}

func clampMin(v, lo float64) float64 { return math.Max(lo, v) }

func main() {
	out := "."
	if len(os.Args) > 1 {
		out = os.Args[1]
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		panic(err)
	}
	traces := []replay.Trace{
		mk("steady-normal", "stable machine, kernel normal throughout", 60, func(i int) replay.TraceSample {
			return s(i, 0.25*totalPages*pageBytes, 100000, 0, 1)
		}),
		mk("slow-leak", "linear free-memory decline, kernel normal->warn", 120, func(i int) replay.TraceSample {
			free := clampMin(0.3*totalPages*pageBytes-float64(i)*5e6, 0.5e8)
			lvl := int64(1)
			if free < 0.06*totalPages*pageBytes {
				lvl = 2
			}
			return s(i, free, 150000, 4e9, lvl)
		}),
		mk("escalation-full", "normal->watch->prevent->critical with recovery", 160, func(i int) replay.TraceSample {
			free := 0.3 * totalPages * pageBytes
			lvl := int64(1)
			swap := 1e9
			switch {
			case i < 40:
			case i < 80:
				free = 0.1*totalPages*pageBytes - 8e6*float64(i)
				lvl, swap = 2, 5e9
			case i < 110:
				free = 0.02*totalPages*pageBytes - 3e6*float64(i)
				lvl, swap = 4, 7.5e9
			default:
				free = 0.25 * totalPages * pageBytes
				lvl, swap = 1, 2e9
			}
			return s(i, clampMin(free, 0.2e8), 200000, swap, lvl)
		}),
		mk("burst-alloc", "single 4GB allocation spike then flat", 80, func(i int) replay.TraceSample {
			free := 0.3 * totalPages * pageBytes
			if i == 40 {
				free -= 4e9
			}
			return s(i, free, 120000, 1e9, 1)
		}),
		mk("sawtooth-cache", "file cache churn: free oscillates", 120, func(i int) replay.TraceSample {
			osc := float64(i%20) * 1.5e7
			return s(i, clampMin(0.2*totalPages*pageBytes-osc, 1e8), 100000, 1e9, 1)
		}),
		mk("kernel-unavailable", "kernel level unreadable, fallback drives state", 60, func(i int) replay.TraceSample {
			return s(i, clampMin(0.05*totalPages*pageBytes-float64(i)*1e6, 0.5e8), 250000, 6e9, 0)
		}),
		mk("all-missing", "no readable sources - Unknown, never Normal", 40, func(i int) replay.TraceSample {
			return s(i, 0, 0, 0, 0)
		}),
		mk("compressor-growth", "compressor grows while free stable", 80, func(i int) replay.TraceSample {
			return s(i, 0.2*totalPages*pageBytes, float64(100000+i*2000), 3e9, 1)
		}),
		mk("swap-runaway", "swap grows superlinearly, free collapsing", 100, func(i int) replay.TraceSample {
			free := clampMin(0.15*totalPages*pageBytes-float64(i*i)*2e5, 0.3e8)
			return s(i, free, 300000, float64(i)*2e8, 2)
		}),
		mk("recovery-after-critical", "critical then full recovery", 100, func(i int) replay.TraceSample {
			free, lvl := 0.01*totalPages*pageBytes, int64(4)
			if i > 50 {
				free, lvl = 0.28*totalPages*pageBytes, 1
			}
			return s(i, free, 250000, 8e9, lvl)
		}),
		mk("flap-synthetic", "rapid normal/warn oscillation (dwell must suppress)", 80, func(i int) replay.TraceSample {
			lvl := int64(1)
			if (i/3)%2 == 0 {
				lvl = 2
			}
			return s(i, 0.08*totalPages*pageBytes, 180000, 5e9, lvl)
		}),
		mk("quiet-idle", "nothing happens for 10 minutes", 120, func(i int) replay.TraceSample {
			return s(i, 0.35*totalPages*pageBytes, 90000, 5e8, 1)
		}),
	}
	if len(traces) != 12 {
		panic("corpus must be 12 traces")
	}
	hashes := map[string]string{}
	for _, tr := range traces {
		b, _ := json.MarshalIndent(tr, "", " ")
		p := filepath.Join(out, tr.Name+".json")
		if err := os.WriteFile(p, b, 0o644); err != nil {
			panic(err)
		}
		_, hash := replay.Run(tr)
		hashes[tr.Name] = hash
		fmt.Printf("%-28s %s\n", tr.Name, hash[:23])
	}
	man, _ := json.MarshalIndent(map[string]any{"version": 1, "hashes": hashes}, "", " ")
	os.WriteFile(filepath.Join(out, "manifest.json"), man, 0o644)
	fmt.Println("corpus written to", out)
}

func s(i int, free, comp, swap float64, lvl int64) replay.TraceSample {
	return replay.TraceSample{
		MonotonicMs: int64(i) * 5000, FreeBytes: free, CompressorPg: comp,
		SwapUsed: swap, KernelLevel: int(lvl), TotalPages: totalPages, PageBytes: pageBytes,
	}
}
