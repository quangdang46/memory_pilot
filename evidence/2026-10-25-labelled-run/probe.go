// probe: a labelled memory workload for measuring Memory Pilot's verdicts.
// Three shapes with known ground truth:
//   leak     - steady growth, no CPU, no plateau          -> expect leak_suspicion
//   plateau  - grows then stops and holds                  -> expect cache_or_retrievable
//   workload - growth driven by real CPU work              -> expect workload_growth
package main

import (
	"flag"
	"os"
	"time"
)

func main() {
	mode := flag.String("mode", "leak", "leak|plateau|workload")
	mbPerTick := flag.Int("mb", 8, "MB added per 10s tick")
	minutes := flag.Int("min", 40, "run duration")
	flag.Parse()

	var held [][]byte
	tick := 0
	end := time.Now().Add(time.Duration(*minutes) * time.Minute)
	for time.Now().Before(end) {
		tick++
		switch *mode {
		case "leak":
			held = append(held, alloc(*mbPerTick))
		case "plateau":
			if tick <= 8 { // grow for 80s, then hold flat
				held = append(held, alloc(*mbPerTick))
			}
		case "workload":
			held = append(held, alloc(*mbPerTick))
			burn(2 * time.Second) // CPU work: the "is it working?" signal
		}
		time.Sleep(10 * time.Second)
	}
	os.Exit(0)
}

func alloc(mb int) []byte {
	b := make([]byte, mb<<20)
	for i := 0; i < len(b); i += 4096 {
		b[i] = 1 // touch: force real residency, not lazy mapping
	}
	return b
}

func burn(d time.Duration) {
	end := time.Now().Add(d)
	var x uint64
	for time.Now().Before(end) {
		for i := 0; i < 100000; i++ {
			x = x*6364136223846793005 + 1442695040888963407
		}
	}
	if x == 42 {
		os.Stdout.WriteString("")
	}
}
