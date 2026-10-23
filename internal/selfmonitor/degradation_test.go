package selfmonitor

import (
	"testing"
	"time"

	"memory_pilot/internal/clock"
	"memory_pilot/internal/config"
)

// ST-19: degradation is monotone — budgets only shrink.
func TestST19MonotoneDegradation(t *testing.T) {
	m := New(clock.NewFake(time.Unix(0, 0)), config.DefaultsForTests())
	m.Degrade("reduced")
	if m.Budget().Detail != "reduced" || m.Budget().SampleEvery != 10*time.Second {
		t.Fatalf("reduced not applied: %+v", m.Budget())
	}
	m.Degrade("full") // attempt to grow — must be ignored
	if m.Budget().Detail != "reduced" {
		t.Errorf("budget grew (SI-6 violation): %+v", m.Budget())
	}
	m.Degrade("minimal")
	if m.Budget().Detail != "minimal" || m.Budget().SampleEvery != 20*time.Second {
		t.Errorf("minimal not applied: %+v", m.Budget())
	}
	m.Degrade("reduced") // cannot go back up
	if m.Budget().Detail != "minimal" {
		t.Errorf("de-escalation of budget allowed")
	}
}

// ST-20: hard-limit exit signals unhealthy at RSS > 400MB.
func TestST20HardLimitExit(t *testing.T) {
	m := New(clock.NewFake(time.Unix(0, 0)), config.DefaultsForTests())
	m.SetHealthy(true)
	m.snap.rssBytes.Store(401 << 20)
	s := m.ObserveAndMaybeDegrade()
	if m.Healthy() {
		t.Errorf("RSS>400MB must mark unhealthy (self-protective exit)")
	}
	_ = s
	if m.ErrOverBudget() == "" {
		t.Errorf("over-budget reason missing")
	}
}
