package main

import (
	"io"
	"os"
	"strings"
	"testing"
)

func capture(t *testing.T, f func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	f()
	w.Close()
	os.Stdout = old
	b, _ := io.ReadAll(r)
	return string(b)
}

// Measured on the owner's machine: 186 groups "grew" in one four-minute
// window, 193 lines of output, most of them 0-3 MB. That list is the warning
// board. The floor hides nothing — the remainder is counted and summed.
func TestReportFloorHidesNoiseButNotTotals(t *testing.T) {
	gs := []group{
		{ID: "node", GrowthMB: 821, Processes: 60},
		{ID: "tiny-a", GrowthMB: 1, Processes: 1},
		{ID: "tiny-b", GrowthMB: 2, Processes: 1},
		{ID: "tiny-c", GrowthMB: 3, Processes: 1},
	}
	out := capture(t, func() { printGroups(gs, false) })
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("%d lines, want 2 (one group, one summary):\n%s", len(lines), out)
	}
	if !strings.Contains(out, "node") {
		t.Errorf("the real mover is missing:\n%s", out)
	}
	// 1+2+3 MB survived the floor as a count and a sum.
	if !strings.Contains(out, "3 other group(s)") || !strings.Contains(out, "6 MB in total") {
		t.Errorf("hidden groups are not accounted for:\n%s", out)
	}
}

func TestReportAllListsEverything(t *testing.T) {
	gs := []group{{ID: "node", GrowthMB: 821}, {ID: "tiny", GrowthMB: 1}}
	out := capture(t, func() { printGroups(gs, true) })
	if strings.Contains(out, "other group(s)") {
		t.Errorf("--all still collapsed groups:\n%s", out)
	}
	if !strings.Contains(out, "tiny") {
		t.Errorf("--all did not list the small group:\n%s", out)
	}
}

// The summary line was arbitrary: "group intelligenceflo grew by 0 MB —
// insufficient evidence" is true and useless.
func TestSummaryPrefersTheNotableVerdict(t *testing.T) {
	quiet := map[string]any{"bucket": "unknown"}
	leak := map[string]any{"bucket": "leak_suspicion"}
	work := map[string]any{"bucket": "workload_growth"}
	if notable(leak) <= notable(work) || notable(work) <= notable(quiet) {
		t.Errorf("ranking is not ordered: leak=%d work=%d quiet=%d",
			notable(leak), notable(work), notable(quiet))
	}
	if notable(map[string]any{}) != 0 {
		t.Error("a record with no bucket ranked above silence")
	}
}
