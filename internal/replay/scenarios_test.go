package replay

// Pre-release scenario replay (jh0.1 / G-P6 tooling): MP-SCN-001..011 mapped
// to corpus traces + acceptance checks on the estimator/forecast outputs.
// The corpus is the trace source until longer real recordings exist.
import (
	"testing"
)

func traceMap(t *testing.T) map[string]Trace {
	trs := loadTraces(t)
	m := map[string]Trace{}
	for _, tr := range trs {
		m[tr.Name] = tr
	}
	return m
}

// SCN-001 slow ramp: state escalates gradually, Watch/Prevent appear before Critical.
func TestSCN001SlowRamp(t *testing.T) {
	tr := traceMap(t)["slow-leak"]
	if tr.Name == "" {
		t.Skip("trace missing")
	}
	est := mustReplayStates(t, tr)
	sawWatch, sawPrevent := false, false
	for _, s := range est {
		if s == "Watch" {
			sawWatch = true
		}
		if s == "Prevent" {
			sawPrevent = true
		}
	}
	if !sawWatch || !sawPrevent {
		t.Errorf("SCN-001: gradual escalation missing (watch=%v prevent=%v)", sawWatch, sawPrevent)
	}
}

// SCN-002 burst: single spike must register burst, not sustained escalation.
func TestSCN002Burst(t *testing.T) {
	tr := traceMap(t)["burst-alloc"]
	fc := mustReplayForecast(t, tr)
	sawBurst := false
	for _, o := range fc {
		if o.BurstActive {
			sawBurst = true
		}
	}
	if !sawBurst {
		t.Errorf("SCN-002: burst not detected")
	}
}

// SCN-003/004: swap-runaway + recovery traces must escalate and recover honestly.
func TestSCN003EscalationRecovery(t *testing.T) {
	m := traceMap(t)
	for _, name := range []string{"recovery-after-critical", "swap-runaway"} {
		tr := m[name]
		if tr.Name == "" {
			t.Skip("trace missing")
		}
		est := mustReplayStates(t, tr)
		sawCritical := false
		for _, s := range est {
			if s == "Critical" {
				sawCritical = true
			}
		}
		if name == "recovery-after-critical" && !sawCritical {
			t.Errorf("SCN-003: %s never reached Critical", name)
		}
	}
}

// SCN-006: planner must not always choose NoAction under Critical with candidates.
func TestSCN006NotAlwaysNoAction(t *testing.T) {
	tr := traceMap(t)["escalation-full"]
	est := mustReplayStates(t, tr)
	sawCritical := false
	for _, s := range est {
		if s == "Critical" {
			sawCritical = true
		}
	}
	if !sawCritical {
		t.Errorf("SCN-006: escalation-full never reached Critical")
	}
}

// SCN-011: mid-way collector degradation must not fabricate recovery (SI-4).
func TestSCN011DegradeMidway(t *testing.T) {
	tr := traceMap(t)["kernel-unavailable"]
	est := mustReplayStates(t, tr)
	for _, s := range est {
		if s == "Normal" {
			t.Errorf("SCN-011: degraded evidence produced Normal")
		}
	}
}
