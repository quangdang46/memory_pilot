package verify

import (
	"fmt"
	"testing"
	"time"
)

// ST-10: unverified by default; verdict only after the window elapses.
func TestST10UnverifiedByDefault(t *testing.T) {
	v := New()
	now := time.Unix(0, 0)
	v.Schedule("a1", 0.8, 2*time.Minute, now)
	if _, ok := v.Result("a1"); ok {
		t.Fatal("verdict before window is a violation")
	}
	v.Resolve("a1", 0.3, now.Add(time.Minute)) // inside window: ignored
	if _, ok := v.Result("a1"); ok {
		t.Fatal("verdict inside window")
	}
	v.Resolve("a1", 0.3, now.Add(3*time.Minute))
	if r, _ := v.Result("a1"); r != "confirmed" {
		t.Errorf("want confirmed, got %s", r)
	}
	// no improvement ⇒ refuted
	v.Schedule("a2", 0.8, time.Minute, now)
	v.Resolve("a2", 0.79, now.Add(2*time.Minute))
	if r, _ := v.Result("a2"); r != "refuted" {
		t.Errorf("want refuted, got %s", r)
	}
}

// Scheduled actions must eventually be resolved by the tick loop; otherwise
// pending grows forever and no action ever receives a verdict.
func TestResolveDue(t *testing.T) {
	v := New()
	now := time.Unix(0, 0)
	v.Schedule("a1", 0.8, 10*time.Second, now)
	v.Schedule("a2", 0.5, 10*time.Second, now)
	if n := v.ResolveDue(0.9, now.Add(5*time.Second)); n != 0 {
		t.Errorf("resolved %d inside the window", n)
	}
	if v.PendingUnverified() != 2 {
		t.Errorf("pending %d, want 2", v.PendingUnverified())
	}
	if n := v.ResolveDue(0.1, now.Add(11*time.Second)); n != 2 {
		t.Errorf("resolved %d, want 2", n)
	}
	if v.PendingUnverified() != 0 {
		t.Errorf("pending %d after resolve, want 0", v.PendingUnverified())
	}
	if r, _ := v.Result("a1"); r != "confirmed" {
		t.Errorf("a1 verdict %q", r)
	}
}

// Retained verdicts are bounded.
func TestResultsBounded(t *testing.T) {
	v := New()
	now := time.Unix(0, 0)
	for i := 0; i < maxResults+500; i++ {
		id := fmt.Sprintf("a%d", i)
		v.Schedule(id, 0.9, time.Second, now)
		v.ResolveDue(0.0, now.Add(2*time.Second))
	}
	if n := len(v.results); n > maxResults {
		t.Errorf("results map holds %d verdicts, cap is %d", n, maxResults)
	}
}
