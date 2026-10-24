// Package config: load + validate, defaults single-sourced here
// (docs/appendix/constants.md is generated against this file).
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// Defaults — single source; MP-REQ values cite these.
type Defaults struct {
	SystemSampleEvery time.Duration `json:"system_sample_every"` // 5s
	ProcessSampleEvery time.Duration `json:"process_sample_every"` // 15s
	RotateBytes       int64         `json:"rotate_bytes"`
	LedgerKeep        int           `json:"ledger_keep"`
	IDLEMaxRSSMB      float64       `json:"idle_max_rss_mb"`  // 60 (budget)
	IDLEMaxCPUPct     float64       `json:"idle_max_cpu_pct"` // 0.3
}

var defaults = Defaults{
	SystemSampleEvery:  5 * time.Second,
	ProcessSampleEvery: 15 * time.Second,
	RotateBytes:        64 << 20,
	LedgerKeep:         8,
	IDLEMaxRSSMB:       60,
	IDLEMaxCPUPct:      0.3,
}

func DefaultsForTests() Defaults { return defaults }

type Config struct {
	DataRoot string          `json:"data_root"`
	Mode     string          `json:"mode"` // observe | enforce
	Overrides Defaults        `json:"overrides"`
}

func DefaultConfig(dataRoot string) Config {
	return Config{DataRoot: dataRoot, Mode: "observe", Overrides: defaults}
}

// Load reads a JSON file and validates; failure exits before goroutines start
// (module-contracts §7 rule 2).
func Load(path string) (Config, error) {
	var c Config
	b, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, err
	}
	if c.Overrides.SystemSampleEvery == 0 {
		c.Overrides = defaults
	}
	return c, c.Validate()
}

func (c Config) Validate() error {
	if c.DataRoot == "" {
		return fmt.Errorf("data_root required")
	}
	if c.Mode != "observe" && c.Mode != "canary" && c.Mode != "enforce" {
		return fmt.Errorf("mode must be observe|canary|enforce, got %q", c.Mode)
	}
	if c.Overrides.SystemSampleEvery < time.Second {
		return fmt.Errorf("system_sample_every below 1s floor (hard floor, ADR-004)")
	}
	if c.Overrides.ProcessSampleEvery < 5*time.Second {
		return fmt.Errorf("process_sample_every below 5s floor (hard floor, ADR-004)")
	}
	if c.Overrides.RotateBytes < 1<<20 {
		return fmt.Errorf("rotate_bytes too small")
	}
	if c.Overrides.LedgerKeep < 2 {
		return fmt.Errorf("ledger_keep must be >= 2")
	}
	return nil
}
