// Package wire: Go mirror of the normative JSON schemas (schemas/*.schema.json).
// Round-trip rule (bead zh5): every valid fixture must
// unmarshal -> marshal -> byte-identical (after canonical re-marshal).
package wire

import "encoding/json"

type Metric struct {
	Value  any     `json:"value"`
	Unit   string  `json:"unit"`
	Source string  `json:"source"`
	Quality string `json:"quality"`
	Reason *string `json:"reason"`
	Mono   int64   `json:"mono"`
	Ts     string  `json:"ts"`
}

type PressureState struct {
	V          int             `json:"v"`
	State      string          `json:"state"`
	Score      float64         `json:"score"`
	Reliable   bool            `json:"reliable"`
	Provenance string          `json:"provenance"`
	Inputs     map[string]Metric `json:"inputs"`
	Reasons    []string        `json:"reasons"`
	Hysteresis *struct {
		ChangedAt string `json:"changed_at"`
		Previous  string `json:"previous"`
		DwellS    int64  `json:"dwell_s"`
	} `json:"hysteresis,omitempty"`
}

type Forecast struct {
	V                int             `json:"v"`
	Series           string          `json:"series"`
	HorizonS         int64           `json:"horizon_s"`
	RateBps          float64         `json:"rate_bps"`
	AccelerationBps2 *float64        `json:"acceleration_bps2,omitempty"`
	Trend            string          `json:"trend"`
	BurstProbability *float64        `json:"burst_probability,omitempty"`
	EtaPointS        *int64          `json:"eta_point_s"`
	EtaLowerBoundS   *int64          `json:"eta_lower_bound_s"`
	Confidence       float64         `json:"confidence"`
	ConfidenceTerms  map[string]float64 `json:"confidence_terms"`
	SampleCount      int             `json:"sample_count"`
	FallbackActive   bool            `json:"fallback_active"`
	Calibration      *struct {
		State            string  `json:"state"`
		CoverageEmpirical *float64 `json:"coverage_empirical"`
		CoverageTarget   float64 `json:"coverage_target"`
		ResolvedCount    int     `json:"resolved_count"`
		PendingCount     int     `json:"pending_count"`
		Alpha            float64 `json:"alpha"`
	} `json:"calibration"`
	Actionable bool `json:"actionable"`
}

type Attribution struct {
	V               int    `json:"v"`
	Window          struct {
		Start     string `json:"start"`
		End       string `json:"end"`
		DurationS int64  `json:"duration_s"`
	} `json:"window"`
	GroupingRule string `json:"grouping_rule"`
	Coverage     struct {
		ProcessesSeen         int     `json:"processes_seen"`
		ProcessesDenied       int     `json:"processes_denied"`
		ProcessesEstimatedTotal int   `json:"processes_estimated_total"`
		CoverageRatioSystem   float64 `json:"coverage_ratio_system"`
		Note                  string  `json:"note"`
	} `json:"coverage"`
	SystemDeltaBytes     int64 `json:"system_delta_bytes"`
	CompressorDeltaBytes int64 `json:"compressor_delta_bytes"`
	Groups               []struct {
		ID            string  `json:"id"`
		Label         string  `json:"label"`
		GrowthBytes   int64   `json:"growth_bytes"`
		GrowthRateBps float64 `json:"growth_rate_bps"`
		Accelerating  bool    `json:"accelerating"`
		ProcessCount  int     `json:"process_count"`
		TopContributor *struct {
			Identity struct {
				Pid       int32 `json:"pid"`
				StartSec  int64 `json:"start_sec"`
				StartUsec int64 `json:"start_usec"`
			} `json:"identity"`
			Name        string `json:"name"`
			GrowthBytes int64  `json:"growth_bytes"`
		} `json:"top_contributor"`
		Targetable   bool      `json:"targetable"`
		Confidence   float64   `json:"confidence"`
		ConfidenceTerms map[string]float64 `json:"confidence_terms"`
		Evidence     []string  `json:"evidence"`
		Alternatives []string  `json:"alternative_explanations"`
	} `json:"groups"`
	LargeButStable []struct {
		Label        string `json:"label"`
		ResidentBytes int64 `json:"resident_bytes"`
		DeltaBytes   int64  `json:"delta_bytes"`
	} `json:"large_but_stable"`
}

type Classification struct {
	V             int      `json:"v"`
	GroupID       string   `json:"group_id"`
	Classification string  `json:"classification"`
	Confidence    float64  `json:"confidence"`
	Basis         []string `json:"basis"`
	Alternatives  []string `json:"alternative_explanations"`
	WhatWouldChangeMind []string `json:"what_would_change_our_mind"`
	RecommendedNextStep string  `json:"recommended_next_step"`
}

type DecisionCandidate struct {
	Action              string   `json:"action"`
	ProviderID          *string  `json:"provider_id,omitempty"`
	ExpectedEffectBytes []int64  `json:"expected_effect_bytes"`
	Cost                float64  `json:"cost"`
	Risk                float64  `json:"risk"`
}

type DecisionRefusal struct {
	Action string `json:"action"`
	Reason string `json:"reason"`
}

// Normalize ensures list fields marshal as [] not null when absent.
func (d *Decision) Normalize() {
	if d.Candidates == nil {
		d.Candidates = []DecisionCandidate{}
	}
	if d.Refused == nil {
		d.Refused = []DecisionRefusal{}
	}
}

type Decision struct {
	V            int    `json:"v"`
	ID           string `json:"id"`
	Mode         string `json:"mode"`
	RequiredMode string `json:"required_mode"`
	Candidates   []DecisionCandidate `json:"candidates"`
	Chosen         *struct {
		Action     string          `json:"action"`
		ProviderID json.RawMessage `json:"provider_id"`
	} `json:"chosen"`
	NoActionReason *string `json:"no_action_reason"`
	Refused        []DecisionRefusal `json:"refused"`
	PolicySnapshot struct {
		Mode       string            `json:"mode"`
		Allowlist  []string          `json:"allowlist"`
		RateLimits map[string]any    `json:"rate_limits"`
	} `json:"policy_snapshot"`
	CreatedAt string `json:"created_at"`
}

type ActionResult struct {
	V            int    `json:"v"`
	ActionID     string `json:"action_id"`
	DecisionID   string `json:"decision_id"`
	Class        string `json:"class"`
	ProviderID   json.RawMessage `json:"provider_id,omitempty"` // raw: fixtures distinguish absent vs explicit null
	Mode         string `json:"mode"`
	Preconditions []struct {
		Name     string `json:"name"`
		OK       bool   `json:"ok"`
		Observed string `json:"observed"`
	} `json:"preconditions"`
	ExecutedAt        string  `json:"executed_at,omitempty"`
	DurationMS        *int64  `json:"duration_ms,omitempty"`
	Outcome           string  `json:"outcome"`
	RefusalReason     *string `json:"refusal_reason"`
	ExpectedEffectBytes []int64 `json:"expected_effect_bytes,omitempty"`
	Verification      *struct {
		WindowS            *int64             `json:"window_s,omitempty"`
		PressureBefore     *map[string]any    `json:"pressure_before,omitempty"`
		PressureAfter      *map[string]any    `json:"pressure_after,omitempty"`
		ObservedEffectBytes *int64            `json:"observed_effect_bytes,omitempty"`
		Verdict            string             `json:"verdict"`
	} `json:"verification"`
}

type StateFile struct {
	V          int    `json:"v"`
	Pid        int32  `json:"pid"`
	StartedAt  string `json:"started_at"`
	UptimeS    int64  `json:"uptime_s"`
	Mode       string `json:"mode"`
	Degradation int   `json:"degradation"`
	Pressure   struct {
		State      string  `json:"state"`
		Score      float64 `json:"score"`
		Provenance string  `json:"provenance"`
		Reliable   bool    `json:"reliable"`
	} `json:"pressure"`
	Forecast struct {
		EtaLowerBoundS *int64  `json:"eta_lower_bound_s"`
		Confidence     float64 `json:"confidence"`
		Trustworthy    bool    `json:"trustworthy"`
		Actionable     bool    `json:"actionable"`
	} `json:"forecast"`
	Self struct {
		RSSBytes   int64   `json:"rss_bytes"`
		CPUPct     float64 `json:"cpu_pct"`
		Goroutines int     `json:"goroutines"`
	} `json:"self"`
	LastSampleMonoNS int64  `json:"last_sample_mono_ns"`
	LastSampleWall   string `json:"last_sample_wall"`
	Ledger           struct {
		Segments int   `json:"segments"`
		Bytes    int64 `json:"bytes"`
		Drops    int   `json:"drops"`
	} `json:"ledger"`
	ConfigHash string `json:"config_hash"`
}

// canonical re-marshals through generic maps so key order differences between
// hand-written fixtures and struct field order do not create false failures.
func Canonical(b []byte) ([]byte, error) {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, err
	}
	return json.Marshal(v)
}
