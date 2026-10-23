package replay

import (
	"testing"

	"memory_pilot/internal/forecast"
	"memory_pilot/internal/pressure"
)

func mustReplayStates(t *testing.T, tr Trace) []string {
	t.Helper()
	est := pressure.New()
	fc := forecast.New(tr.Threshold)
	var out []string
	var recent []float64 // free_pct history for trend risk
	for _, s := range tr.Samples {
		_ = fc.Update(forecast.Sample{
			MonotonicMs: s.MonotonicMs, HeadroomBytes: s.FreeBytes,
			CompressorPages: s.CompressorPg, SwapUsedBytes: s.SwapUsed,
		})
		in := toEstimatorInput(s)
		// trend risk: free-% points dropped per minute, normalized to the
		// WATCH_TREND_THRESHOLD scale (1 pp/min ⇒ risk 1.0)
		if total := float64(s.TotalPages); total > 0 {
			pp := float64(s.FreeBytes) / (total * float64(s.PageBytes)) * 100
			recent = append(recent, pp)
			if len(recent) > 12 {
				recent = recent[1:]
			}
			if len(recent) == 12 {
				spanMin := 12 * 5.0 / 60
				drop := recent[0] - recent[len(recent)-1]
				in.TrendRisk = clamp01(drop / spanMin / 1.0)
			}
		}
		out = append(out, string(est.Update(in).State))
	}
	return out
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

func mustReplayForecast(t *testing.T, tr Trace) []forecast.Out {
	t.Helper()
	fc := forecast.New(tr.Threshold)
	var out []forecast.Out
	for _, s := range tr.Samples {
		out = append(out, fc.Update(forecast.Sample{
			MonotonicMs: s.MonotonicMs, HeadroomBytes: s.FreeBytes,
			CompressorPages: s.CompressorPg, SwapUsedBytes: s.SwapUsed,
		}))
	}
	return out
}
