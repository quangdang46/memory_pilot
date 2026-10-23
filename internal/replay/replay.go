// Package replay: deterministic, bit-identical replay of traces through the
// estimator + forecaster. No wall clock: time comes from the trace itself.
package replay

import (
	"crypto/sha256"
	"math"
	"encoding/json"
	"fmt"

	"memory_pilot/internal/forecast"
	"memory_pilot/internal/pressure"
	"memory_pilot/internal/model"
)

type TraceSample struct {
	MonotonicMs   int64   `json:"mono_ms"`
	FreeBytes     float64 `json:"free_bytes"`
	CompressorPg  float64 `json:"compressor_pages"`
	SwapUsed      float64 `json:"swap_used"`
	KernelLevel   int     `json:"kernel_level"` // 0=unavailable
	TotalPages    uint64  `json:"total_pages"`
	PageBytes     uint64  `json:"page_bytes"`
}

type Trace struct {
	Name        string        `json:"name"`
	Version     int           `json:"version"`
	Description string        `json:"description"`
	Threshold   float64       `json:"threshold_bytes"` // critical free-bytes level
	Samples     []TraceSample `json:"samples"`
}

type ReplayRow struct {
	MonoMS    int64           `json:"mono_ms"`
	State     string          `json:"state"`
	Provenance string         `json:"provenance"`
	Score     float64         `json:"score"`
	Rate      float64         `json:"rate"`
	TteS      float64         `json:"tte_s"`
	LowerS    float64         `json:"lower_s"`
	Conf      float64         `json:"conf"`
	Burst     bool            `json:"burst"`
	Actionable bool           `json:"actionable"`
}

// Run replays a trace and returns rows plus a content hash. Determinism: the
// hash must be identical across runs on any machine (no maps, no wall clock).
func Run(t Trace) (rows []ReplayRow, hash string) {
	est := pressure.New()
	fc := forecast.New(t.Threshold)
	for _, s := range t.Samples {
		st := est.Update(toEstimatorInput(s))
		fo := fc.Update(forecast.Sample{
			MonotonicMs: s.MonotonicMs, HeadroomBytes: s.FreeBytes,
			CompressorPages: s.CompressorPg, SwapUsedBytes: s.SwapUsed,
		})
		rows = append(rows, ReplayRow{
			MonoMS: s.MonotonicMs, State: string(st.State), Provenance: st.Provenance,
			Score: round6(st.Score), Rate: round6(fo.RateBytesS), TteS: round6(fo.TteS),
			LowerS: round6(fo.LowerBoundS), Conf: round6(fo.Confidence),
			Burst: fo.BurstActive, Actionable: fo.Actionable,
		})
	}
	b, _ := json.Marshal(rows)
	h := sha256.Sum256(b)
	return rows, fmt.Sprintf("sha256:%x", h)
}

func round6(f float64) float64 {
	return math.Round(f*1e6) / 1e6
}

func toEstimatorInput(s TraceSample) pressure.Input {
	lvl := modelLevel(s.KernelLevel)
	in := pressure.Input{
		TotalPages: s.TotalPages,
		SwapOK:     s.SwapUsed > 0,
		SwapUsedBytes: uint64(s.SwapUsed),
		DiskOK:     false,
	}
	in.Sample.PageBytes = s.PageBytes
	in.Sample.FreePages = uint64(s.FreeBytes / float64(s.PageBytes))
	in.Sample.CompressorPages = uint64(s.CompressorPg)
	if s.KernelLevel == 0 {
		in.Sample.MachStatsQuality = "good"
		in.Sample.KernelLevelQuality = "missing"
	} else {
		in.Sample.MachStatsQuality = "good"
		in.Sample.KernelLevelQuality = "good"
		in.Sample.KernelLevel = lvl
	}
	in.Sample.MonotonicMs = s.MonotonicMs
	return in
}

func modelLevel(v int) model.PressureLevel {
	switch v {
	case 1, 2, 4:
		return model.PressureLevel(v)
	}
	return model.LevelUnknown
}
