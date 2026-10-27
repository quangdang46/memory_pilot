package ledger

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The live daemon stopped writing on 2026-10-26 13:17:38 and stayed silent for
// 31 hours while remaining alive and answering IPC. Two faults combined:
//
//  1. seqOf parsed "ledger.0006.jsonl" with Sscanf("%s.%04d.jsonl"). %s is
//     greedy, so the pattern never matched and every restart began at seq 0.
//     The first rotation then targeted ledger.0001.jsonl, which already existed.
//  2. rotateFile closed the live handle before opening the replacement. When
//     O_EXCL failed, it returned the error leaving l.f pointing at a closed
//     file — so Append no longer hit its "ledger closed" guard, it failed on
//     every write with EBADF, and all 17 call sites discard that error.
func TestSeqOfReadsTheSequenceNumber(t *testing.T) {
	for _, c := range []struct {
		path string
		want int
	}{
		{"/x/ledger.0001.jsonl", 1},
		{"/x/ledger.0006.jsonl", 6},
		{"/x/ledger.0042.jsonl", 42},
		{"/x/ledger.1234.jsonl", 1234},
	} {
		if got := seqOf(c.path); got != c.want {
			t.Errorf("seqOf(%q) = %d, want %d", c.path, got, c.want)
		}
	}
}

// A restart must continue the numbering, not collide with the files already on
// disk. This is the exact shape that killed the daemon: six ledgers present,
// reopen, rotate, collide, die.
func TestRestartContinuesNumbering(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"0001", "0002", "0003", "0004", "0005", "0006"} {
		p := filepath.Join(dir, "led.0"+n+".jsonl")
		p = filepath.Join(dir, "led."+n+".jsonl")
		if err := os.WriteFile(p, []byte(versionHeader+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	l, err := openWith(dir, "led", 1, 8) // rotate on the next append
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if l.seq != 6 {
		t.Fatalf("reopened at seq %d, want 6", l.seq)
	}
	if err := l.Append(map[string]int{"i": 1}); err != nil {
		t.Fatalf("append after restart: %v", err)
	}
	if l.seq != 7 {
		t.Fatalf("rotated to seq %d, want 7", l.seq)
	}
	if _, err := os.Stat(filepath.Join(dir, "led.0007.jsonl")); err != nil {
		t.Fatalf("ledger.0007.jsonl not created: %v", err)
	}
}

// A failed rotation must not destroy the live handle. If the replacement cannot
// be opened, appends into the existing file must keep working — losing the
// rotation is survivable, losing every subsequent record is not.
func TestFailedRotationKeepsWriting(t *testing.T) {
	dir := t.TempDir()
	l, err := openWith(dir, "keep", 1<<20, 8)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if err := l.Append(map[string]int{"i": 0}); err != nil {
		t.Fatal(err)
	}
	// Force the next rotation target to already exist.
	if err := os.WriteFile(filepath.Join(dir, "keep.0002.jsonl"), []byte(versionHeader+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// This is the path the live daemon took: the executor's rotate_own_ledger
	// action calls Rotate directly, not through the size cap.
	if err := l.Rotate(); err == nil {
		t.Fatal("expected Rotate to report the collision")
	}
	if err := l.Append(map[string]int{"i": 1}); err != nil {
		t.Fatalf("ledger unusable after a failed rotation: %v", err)
	}
	// And it must recover on its own once the collision is gone.
	if err := os.Remove(filepath.Join(dir, "keep.0002.jsonl")); err != nil {
		t.Fatal(err)
	}
	if err := l.Append(map[string]int{"i": 2}); err != nil {
		t.Fatalf("ledger did not recover: %v", err)
	}
	recs, err := l.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	// Records 0 and 1 land in keep.0001.jsonl; record 2 needs a rotation and
	// the cap is 1 MB so it stays in the same file. All three must be readable.
	found := map[int]bool{}
	for _, r := range recs {
		var v map[string]int
		if err := json.Unmarshal(r, &v); err != nil {
			t.Fatal(err)
		}
		found[v["i"]] = true
	}
	for i := 0; i < 3; i++ {
		if !found[i] {
			t.Fatalf("record %d lost across the rotation incident (found %v)", i, found)
		}
	}
}

// Silence and breakage must be distinguishable. Every daemon Append call site
// discards the returned error, so the ledger itself must report the transition.
func TestBrokenLedgerIsReportedNotSilent(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir, "tell")
	if err != nil {
		t.Fatal(err)
	}
	var events []string
	l.OnError(func(e error) { events = append(events, e.Error()) })

	if err := l.Append(map[string]int{"i": 0}); err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("reported a failure while healthy: %v", events)
	}

	l.Close()
	if err := l.Append(map[string]int{"i": 1}); err == nil {
		t.Fatal("append on a closed ledger returned nil")
	}
	if len(events) != 1 {
		t.Fatalf("closed ledger not reported: %v", events)
	}
	// A second failure in the same episode must not repeat the report.
	_ = l.Append(map[string]int{"i": 2})
	if len(events) != 1 {
		t.Fatalf("failure reported more than once per episode: %v", events)
	}
}

// The unit tests above call openWith directly. The daemon goes through Open,
// so the whole lifecycle is exercised here the way the daemon actually drives
// it: reopen, append, rotate on demand, restart, append again.
func TestOpenLifecycleAcrossRotations(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"0001", "0002", "0003"} {
		if err := os.WriteFile(filepath.Join(dir, "life."+n+".jsonl"),
			[]byte(versionHeader+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	l, err := Open(dir, "life")
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Append(map[string]int{"i": 0}); err != nil {
		t.Fatal(err)
	}
	if err := l.Rotate(); err != nil {
		t.Fatalf("rotate at seq 3: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "life.0004.jsonl")); err != nil {
		t.Fatalf("rotate did not create seq 4: %v", err)
	}
	if err := l.Append(map[string]int{"i": 1}); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	l2, err := Open(dir, "life")
	if err != nil {
		t.Fatal(err)
	}
	defer l2.Close()
	if l2.seq != 4 {
		t.Fatalf("reopened at seq %d, want 4", l2.seq)
	}
	if err := l2.Append(map[string]int{"i": 2}); err != nil {
		t.Fatal(err)
	}
	recs, err := l2.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	var n int
	for _, r := range recs {
		var v map[string]int
		if err := json.Unmarshal(r, &v); err != nil {
			t.Fatal(err)
		}
		n += v["i"]
	}
	if n != 3 {
		t.Fatalf("lost records across rotations: sum %d, want 3", n)
	}
}

// `status` used to report only the size of the ledger on disk, which stays
// healthy-looking after every write has started failing. Health must report
// the write path, not the files.
func TestHealthReportsTheWritePathNotTheFileSize(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(dir, "h")
	if err != nil {
		t.Fatal(err)
	}
	if ok, msg, seq := l.Health(); !ok || msg != "" || seq != 1 {
		t.Fatalf("healthy ledger reported ok=%v msg=%q seq=%d", ok, msg, seq)
	}
	if err := l.Append(map[string]int{"i": 0}); err != nil {
		t.Fatal(err)
	}
	if ok, _, _ := l.Health(); !ok {
		t.Fatal("healthy after a successful append")
	}
	// The exact production state: handle closed, records still on disk.
	l.Close()
	if l.SizeBytes() <= 0 {
		t.Fatal("size should still be non-zero; that is the trap")
	}
	_ = l.Append(map[string]int{"i": 1})
	ok, msg, _ := l.Health()
	if ok {
		t.Fatal("reported healthy while every write fails")
	}
	if msg == "" {
		t.Fatal("unhealthy but gave no reason")
	}
}
