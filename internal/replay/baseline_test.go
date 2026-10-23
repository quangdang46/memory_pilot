package replay

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"

	"memory_pilot/internal/forecast"
	"memory_pilot/internal/pressure"
)

// G-P2: on the trace corpus, the forecast must beat the naive baseline
// (instantaneous-rate linear extrapolation) on median absolute TTE error,
// and the pressure estimator must never emit Normal while all evidence is
// missing (MP-REQ-020..026 spot checks).
func loadTraces(t *testing.T) []Trace {
	entries, err := os.ReadDir("../../traces")
	if err != nil {
		t.Skip("corpus not generated yet")
	}
	var out []Trace
	for _, e := range entries {
		if e.Name() == "manifest.json" {
			continue
		}
		b, err := os.ReadFile(filepath.Join("../../traces", e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		var tr Trace
		json.Unmarshal(b, &tr)
		out = append(out, tr)
	}
	if len(out) != 12 {
		t.Fatalf("expected 12 traces, got %d", len(out))
	}
	return out
}

func TestForecastBeatsNaiveBaseline(t *testing.T) {
	traces := loadTraces(t)
	oursTotal, naiveTotal, points := 0.0, 0.0, 0
	for _, tr := range traces {
		fc := forecast.New(tr.Threshold)
		var crossingS float64 = -1
		for _, s := range tr.Samples {
			if crossingS < 0 && s.FreeBytes <= tr.Threshold && s.FreeBytes > 0 {
				crossingS = float64(s.MonotonicMs) / 1000
			}
		}
		for i, s := range tr.Samples {
			now := float64(s.MonotonicMs) / 1000
			if crossingS <= now || s.FreeBytes <= tr.Threshold {
				break
			}
			actualRemaining := crossingS - now
			if actualRemaining > 7200 { // beyond horizon cap: not enrolled
				continue
			}
			o := fc.Update(forecast.Sample{
				MonotonicMs: s.MonotonicMs, HeadroomBytes: s.FreeBytes,
				SwapUsedBytes: s.SwapUsed, CompressorPages: s.CompressorPg,
			})
			if o.FallbackActive || o.TteS < 0 {
				continue
			}
			// naive: instantaneous rate over this tick
			naive := -1.0
			if i > 0 {
				dt := float64(s.MonotonicMs-tr.Samples[i-1].MonotonicMs) / 1000
				dr := (tr.Samples[i-1].FreeBytes - s.FreeBytes) / dt
				if dr > 0 {
					naive = (s.FreeBytes - tr.Threshold) / dr
				}
			}
			if naive < 0 || naive > 7200 {
				continue
			}
			oursTotal += math.Abs(o.TteS - actualRemaining)
			naiveTotal += math.Abs(naive - actualRemaining)
			points++
		}
	}
	if points < 50 {
		t.Fatalf("too few comparison points: %d", points)
	}
	oursMAE, naiveMAE := oursTotal/float64(points), naiveTotal/float64(points)
	t.Logf("points=%d ours MAE=%.0fs naive MAE=%.0fs", points, oursMAE, naiveMAE)
	if oursMAE >= naiveMAE {
		t.Errorf("forecast (MAE %.0fs) did not beat naive baseline (MAE %.0fs)", oursMAE, naiveMAE)
	}
}

func TestUnknownNeverNormalOnAllMissing(t *testing.T) {
	traces := loadTraces(t)
	for _, tr := range traces {
		if tr.Name != "all-missing" {
			continue
		}
		est := pressure.New()
		for _, s := range tr.Samples {
			out := est.Update(toEstimatorInput(s))
			if out.State == pressure.Normal {
				t.Fatal("all-missing trace produced Normal (SI-4 violation)")
			}
			if out.State != pressure.Unknown {
				t.Fatalf("want Unknown, got %s", out.State)
			}
		}
	}
}

func TestFlapSuppressed(t *testing.T) {
	traces := loadTraces(t)
	for _, tr := range traces {
		if tr.Name != "flap-synthetic" {
			continue
		}
		est := pressure.New()
		transitions := 0
		var last pressure.State
		for _, s := range tr.Samples {
			out := est.Update(toEstimatorInput(s))
			if last != "" && out.State != last {
				transitions++
			}
			last = out.State
		}
		// 80 samples over 400s of 3-sample oscillation: dwell must keep
		// transitions far below the naive count (13)
		if transitions > 3 {
			t.Errorf("flapping not suppressed: %d transitions", transitions)
		}
	}
}
