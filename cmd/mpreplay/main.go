// mpreplay replays a trace (or all traces with -all) through the estimator +
// forecaster and verifies manifest hashes. Deterministic; no wall clock.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"memory_pilot/internal/replay"
)

func main() {
	dir := flag.String("traces", "traces", "trace directory")
	all := flag.Bool("all", false, "replay every trace and verify manifest hashes")
	name := flag.String("trace", "", "single trace name")
	flag.Parse()
	if *all {
		man, err := os.ReadFile(filepath.Join(*dir, "manifest.json"))
		if err != nil {
			panic(err)
		}
		var m struct {
			Hashes map[string]string `json:"hashes"`
		}
		json.Unmarshal(man, &m)
		fail := 0
		for n, want := range m.Hashes {
			_, got := replayTrace(filepath.Join(*dir, n+".json"))
			if got != want {
				fmt.Printf("MISMATCH %s: got %s want %s\n", n, got, want)
				fail++
			}
		}
		if fail > 0 {
			os.Exit(1)
		}
		fmt.Printf("ALL %d TRACES BIT-IDENTICAL\n", len(m.Hashes))
		return
	}
	p := *name
	if p != "" && filepath.Ext(p) != ".json" {
		p += ".json"
	}
	rows, hash := replayTrace(filepath.Join(*dir, p))
	for _, r := range rows {
		b, _ := json.Marshal(r)
		fmt.Println(string(b))
	}
	fmt.Println(hash)
}

func replayTrace(path string) ([]replay.ReplayRow, string) {
	b, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	var t replay.Trace
	if err := json.Unmarshal(b, &t); err != nil {
		panic(err)
	}
	return replay.Run(t)
}
