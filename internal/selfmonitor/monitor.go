// Package selfmonitor: budgets, monotonic degradation, honest self-measurement
// (SI-6, SI-7; ADR-008). Reads its own RSS via proc_pidinfo — same API as for
// other processes (no ReadMemStats-as-RSS lie).
package selfmonitor

import (
	"context"
	"runtime"
	"sync/atomic"
	"time"

	"memory_pilot/internal/clock"
	"memory_pilot/internal/config"
)

type Budget struct {
	MaxRSSMB    float64
	MaxCPUPct   float64
	SampleEvery time.Duration
	// SI-6: shrinks monotonically with pressure escalation.
	Detail string // full | reduced | minimal
}

type snapshot struct {
	rssBytes   atomic.Int64
	cpuSeconds atomic.Int64 // user+sys via rusage, sampled
	goroutines atomic.Int64
	ledgerSize atomic.Int64
}

type Monitor struct {
	clk      clock.Clock
	budget   atomic.Pointer[Budget]
	snap     snapshot
	startCPU time.Time // wall for % calc
	lastCPU  int64
	healthy  atomic.Bool
}

func New(clk clock.Clock, cfg config.Defaults) *Monitor {
	m := &Monitor{clk: clk}
	m.budget.Store(&Budget{MaxRSSMB: cfg.IDLEMaxRSSMB, MaxCPUPct: cfg.IDLEMaxCPUPct,
		SampleEvery: cfg.SystemSampleEvery, Detail: "full"})
	m.startCPU = time.Now()
	return m
}

func (m *Monitor) Budget() Budget { return *m.budget.Load() }

// Degrade applies SI-6: budgets only shrink, never grow.
func (m *Monitor) Degrade(detail string) {
	rank := map[string]int{"full": 0, "reduced": 1, "minimal": 2}
	for {
		cur := m.budget.Load()
		if rank[cur.Detail] >= rank[detail] {
			return // monotone: only shrink (SI-6)
		}
		n := *cur
		n.Detail = detail
		if detail == "reduced" {
			n.SampleEvery = cur.SampleEvery * 2
		} else if detail == "minimal" {
			n.SampleEvery = cur.SampleEvery * 2 // from reduced, or 2x from full
		}
		if m.budget.CompareAndSwap(cur, &n) {
			return
		}
	}
}

func (m *Monitor) Healthy() bool     { return m.healthy.Load() }
func (m *Monitor) SetHealthy(h bool) { m.healthy.Store(h) }

// Sample records self-usage; called once per tick from the supervisor.
func (m *Monitor) Sample(ledgerBytes int64) {
	rss := selfRSSBytes()
	m.snap.rssBytes.Store(int64(rss))
	m.snap.goroutines.Store(int64(runtime.NumGoroutine()))
	m.snap.ledgerSize.Store(ledgerBytes)
}

type SelfSnapshot struct {
	RSSMB      float64
	Goroutines int
	LedgerMB   float64
	CPUPct     float64
	Budget     Budget
}

func (m *Monitor) Snapshot() SelfSnapshot {
	elapsed := time.Since(m.startCPU).Seconds()
	var cpuUser, cpuSys float64
	// getrusage of self via Go runtime is not exposed; use /proc-less rusage
	// through the same proc_pid_rusage API (honest self-measurement).
	cpuUser, cpuSys = selfCPURusage()
	cpu := cpuUser + cpuSys
	pct := 0.0
	if elapsed > 0 {
		pct = cpu / elapsed * 100
	}
	return SelfSnapshot{
		RSSMB:      float64(m.snap.rssBytes.Load()) / 1e6,
		Goroutines: int(m.snap.goroutines.Load()),
		LedgerMB:   float64(m.snap.ledgerSize.Load()) / 1e6,
		CPUPct:     pct,
		Budget:     m.Budget(),
	}
}

// ObserveAndMaybeDegrade applies the degradation ladder (SI-6) from
// performance-budget.md §5.
func (m *Monitor) ObserveAndMaybeDegrade() SelfSnapshot {
	s := m.Snapshot()
	switch {
	case s.RSSMB > 400:
		// self-protective exit is the supervisor's call; signal via Healthy()
		m.healthy.Store(false)
	case s.RSSMB > 200:
		m.Degrade("minimal")
	case s.RSSMB > 120:
		m.Degrade("reduced")
	}
	return s
}

// Run wires periodic self-sampling; returns when ctx is done.
func (m *Monitor) Run(ctx context.Context, ledgerBytes func() int64) {
	t := m.clk.NewTicker(m.Budget().SampleEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C():
			m.Sample(ledgerBytes())
			m.ObserveAndMaybeDegrade()
			m.SetHealthy(true)
		}
	}
}

// ErrOverBudget returns a reason string when a hard cap is breached, else "".
func (m *Monitor) ErrOverBudget() string {
	s := m.Snapshot()
	if s.RSSMB > 400 {
		return "RSS > 400MB: self-protective exit (exit 10)"
	}
	return ""
}
