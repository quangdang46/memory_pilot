package relief

import (
	"testing"
	"time"
)

// End to end on the real state: Prevent pressure at 23:26, three idle
// rust-analyzer processes, 12 GB swap used of 12 GB total.
func TestEndToEndPreventState(t *testing.T) {
	members := []Member{
		{Pid: 11265, Exe: "rust-analyzer", Footprint: 1017 << 20, QuietFor: 48 * time.Hour, ParentAlive: true},
		{Pid: 21432, Exe: "rust-analyzer", Footprint: 859 << 20, QuietFor: 48 * time.Hour, ParentAlive: true},
		{Pid: 37060, Exe: "rust-analyzer", Footprint: 821 << 20, QuietFor: 13 * time.Hour, ParentAlive: true},
		{Pid: 96329, Exe: "node", Footprint: 1803 << 20, QuietFor: time.Second, ParentAlive: true},
		{Pid: 50972, Exe: "claude", Footprint: 950 << 20, QuietFor: 3 * time.Second, ParentAlive: true},
		{Pid: 85066, Exe: "VirtualMachine", Footprint: 1797 << 20, QuietFor: 4 * time.Second, ParentAlive: false},
	}
	cs := AssessMembers(GroupFacts{Members: members})
	Rank(cs)

	// 12 GB swap used, 16 GB RAM: a genuine shortfall.
	shortfall := int64(2 << 30)
	r := &Recommendation{Severity: "Prevent", Shortfall: shortfall, Candidates: cs}
	for _, c := range cs {
		if c.State == StateAbandoned {
			r.TotalRelief += c.ExpectedReliefBytes
		}
	}
	if r.TotalRelief == 0 {
		// Nothing abandoned: the suspicious total is what the text reports.
		for _, c := range cs {
			r.TotalRelief += c.ExpectedReliefBytes
		}
	}
	txt := r.Text()
	t.Logf("\n%s", txt)

	if len(cs) != 3 {
		t.Errorf("%d candidates, want exactly the 3 idle rust-analyzer processes; "+
			"busy node, claude and the VM must be excluded", len(cs))
	}
	if !r.Sufficient() && r.TotalRelief >= shortfall {
		t.Error("Sufficient() disagrees with the totals")
	}
	if r.TotalRelief < int64(2<<30) {
		t.Errorf("total relief %d MB, want >= 2048", r.TotalRelief>>20)
	}
}
