// Package reporting: honest-language renderers (G-P5). Renderers cannot be
// constructed from values lacking confidence/evidence (SI-5).
package reporting

import (
	"fmt"
	"strings"
	"time"

	"memory_pilot/internal/classify"
)

type StatusLine struct {
	PID        int32
	UptimeS    int64
	Mode       string
	Pressure   string
	Score      float64
	Provenance string
	Reliable   bool
	SelfRSSMB  float64
	SelfCPUPct float64
	LedgerMB   float64
}

func RenderStatus(s StatusLine) string {
	if !s.Reliable {
		return fmt.Sprintf("mempilotd pid %d | mode %s | pressure UNKNOWN (unreliable evidence — not calm) | self rss %.1fMB cpu %.2f%% | ledger %.1fMB",
			s.PID, s.Mode, s.SelfRSSMB, s.SelfCPUPct, s.LedgerMB)
	}
	return fmt.Sprintf("mempilotd pid %d | uptime %s | mode %s | pressure %s (%.2f, %s) | self rss %.1fMB cpu %.2f%% | ledger %.1fMB",
		s.PID, time.Duration(s.UptimeS)*time.Second, s.Mode, s.Pressure, s.Score, s.Provenance,
		s.SelfRSSMB, s.SelfCPUPct, s.LedgerMB)
}

type ForecastLine struct {
	TteS       float64
	LowerS     float64
	Confidence float64
	Calibrated bool
	Actionable bool
}

func RenderForecast(f ForecastLine) string {
	var tte string
	switch {
	case f.TteS < 0:
		tte = "no crossing within horizon"
	default:
		tte = fmt.Sprintf("ETA %s", time.Duration(f.TteS*float64(time.Second)).Round(time.Second))
	}
	trust := "warming up (not actionable)"
	if f.Calibrated && f.Actionable {
		trust = fmt.Sprintf("calibrated, lower bound %s", time.Duration(f.LowerS*float64(time.Second)).Round(time.Second))
	} else if f.Calibrated {
		trust = "calibrated but not actionable"
	}
	return fmt.Sprintf("forecast: %s | confidence %.2f | %s", tte, f.Confidence, trust)
}

func RenderClassification(v classify.Verdict, growthMB int64) string {
	return classify.RenderVerdict(v, growthMB) // honest by construction (phrase test)
}

// Exit codes (cli-surface.md namespace).
const (
	ExitOK          = 0
	ExitUsage       = 2
	ExitGeneral     = 1
	ExitConfig      = 3
	ExitNoDaemon    = 4
	ExitUnsupported = 5
)

// Golden outputs are asserted in reporting_test.go (G-P5).
var _ = strings.TrimSpace
