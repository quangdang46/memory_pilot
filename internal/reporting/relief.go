// Relief candidate report (bead 6lc): THREE numbers, never one score.
// Orphan detect+recommend (bead fhn): recommendation with the full evidence
// bar. Session lifecycle (bead z6g): idle detection + resume awareness.
package reporting

import (
	"fmt"
	"time"
)

// ReliefCandidate: three numbers — expected effect range, confidence, cost —
// plus risk. Never collapsed into one score (ADR-006 §reporting).
type ReliefCandidate struct {
	Kind           string
	EffectLowBytes int64
	EffectHighBytes int64
	Confidence     float64
	CostCPUms      int64
	RiskNote       string
	Verified       bool // measurement-backed (ST-10)
}

func RenderRelief(c ReliefCandidate) string {
	verdict := "unverified"
	if c.Verified {
		verdict = "verified"
	}
	return fmt.Sprintf("relief candidate %s: expected effect %d–%d MB | confidence %.2f | cost ~%dms CPU | risk: %s | %s",
		c.Kind, c.EffectLowBytes>>20, c.EffectHighBytes>>20, c.Confidence, c.CostCPUms, c.RiskNote, verdict)
}

// OrphanCandidate: a process whose parent is gone, holding memory, with no
// activity for the idle window. Recommend, never act (T3).
type OrphanCandidate struct {
	Pid           int32
	Exe           string
	ParentGone    bool
	IdleS         int64
	FootprintMB   int64
	CoverageOK    bool
}

func (o OrphanCandidate) Recommend(idleThresholdS int64) (string, bool) {
	if !o.CoverageOK {
		return "", false // honest denial: visibility too low
	}
	if o.ParentGone && o.IdleS >= idleThresholdS && o.FootprintMB >= 256 {
		return fmt.Sprintf("orphan candidate pid %d (%s): parent exited, idle %s, footprint %dMB — consider closing it yourself (we will not act on it)",
			o.Pid, o.Exe, time.Duration(o.IdleS*int64(time.Second)).Round(time.Minute), o.FootprintMB), true
	}
	return "", false
}

// SessionState: idle detection + resume awareness for coding-agent sessions.
type SessionState struct {
	GroupKey     string
	CPUDeltaS    float64
	LastActive   time.Time
	Resumed      bool // saw activity after an idle period
	FollowUpDue  bool // prior advisory unanswered
}

func (s SessionState) IdleFor(now time.Time) time.Duration { return now.Sub(s.LastActive) }

func (s SessionState) Describe(now time.Time) string {
	idle := s.IdleFor(now).Round(time.Minute)
	switch {
	case s.Resumed:
		return fmt.Sprintf("session %s: resumed after idle (tracking follow-up)", s.GroupKey)
	case idle > 30*time.Minute:
		return fmt.Sprintf("session %s: idle %s", s.GroupKey, idle)
	default:
		return fmt.Sprintf("session %s: active (cpu +%.1fs)", s.GroupKey, s.CPUDeltaS)
	}
}
