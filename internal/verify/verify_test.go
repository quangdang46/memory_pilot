package verify

import (
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
