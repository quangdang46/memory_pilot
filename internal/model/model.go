// Package model holds the value types shared across layers (MP-ARCH-002 §1).
// No behavior, no I/O. Canonical shapes also live in schemas/*.schema.json.
package model

import "time"

// Quality — SI-4: missing evidence never improves the verdict.
type Quality string

const (
	QualGood     Quality = "good"
	QualDegraded Quality = "degraded"
	QualStale    Quality = "stale"
	QualMissing  Quality = "missing"
)

// PressureLevel — kernel decode 1/2/4 (S-3); anything else is Unknown.
type PressureLevel int

const (
	LevelUnknown  PressureLevel = 0
	LevelNormal   PressureLevel = 1
	LevelWarn     PressureLevel = 2
	LevelCritical PressureLevel = 4
)

type SystemSample struct {
	At              time.Time
	MonotonicMs     int64
	PageBytes       uint64
	FreePages       uint64 // free_count (excl. speculative, documented)
	SpecPages       uint64
	ActivePages     uint64
	InactivePages   uint64
	WiredPages      uint64
	CompressorPages uint64
	InternalPages   uint64
	ExternalPages   uint64
	Swapins         uint64
	Swapouts        uint64
	KernelLevel     PressureLevel
	MemStatusLevel  int32 // kern.memorystatus_level, -1 if unread
	// Quality flags per source — a dead collector never looks calm (SI-4).
	MachStatsQuality   Quality
	KernelLevelQuality Quality
}

// Process identity tuple (MP-ARCH-004): pid alone is not identity.
type ProcessIdentity struct {
	Pid        int32
	Ppid       int32     // parent at sample time; 0 when unreadable
	StartTime  time.Time // proc start abstime mapped to wall clock
	Executable string
}

type ProcessSample struct {
	Identity       ProcessIdentity
	FootprintBytes uint64 // phys_footprint (S-7: rusage_info_v4)
	ResidentBytes  uint64
	CpuUserS       float64
	CpuSystemS     float64
	Quality        Quality
}

type Coverage struct {
	Attempted int
	Succeeded int
	Denied    int // readable-but-refused: same user, permission problem
	// Foreign counts processes owned by other users. They are not a
	// visibility failure: a user-scope daemon was never entitled to them, and
	// counting them as "denied" made every coverage ratio look broken
	// (226 of 614 on a normal desktop, all root/other-user processes).
	Foreign int
}

// VisibleRatio is the fraction of processes this daemon could ever read that
// it actually read. Foreign processes are excluded from the denominator
// rather than silently inflating the failure count.
func (c Coverage) VisibleRatio() float64 {
	inScope := c.Attempted - c.Foreign
	if inScope <= 0 {
		return 0
	}
	return float64(c.Succeeded) / float64(inScope)
}

// InScope reports how many processes this daemon was entitled to read.
func (c Coverage) InScope() int { return c.Attempted - c.Foreign }

type ProcessSnapshot struct {
	At          time.Time
	MonotonicMs int64
	Processes   []ProcessSample
	Coverage    Coverage // part of the return value, not an afterthought
	Quality     Quality
}

type PressureState struct {
	At      time.Time
	Level   PressureLevel
	DwellS  int64
	Score   float64
	Quality Quality
}

type Forecast struct {
	At           time.Time
	HorizonS     int64
	RateBytesS   float64
	Acceleration float64
	EtaS         float64 // -1 = no crossing within horizon
	Confidence   float64
	Calibrated   bool
	Fallback     bool
	Actionable   bool // actionable ⇒ bound ∧ calibrated ∧ !fallback (schema rule)
	Bound        bool
}

type Classification string

const (
	ClassNone           Classification = "none"
	ClassLeakLike       Classification = "leak-like"
	ClassWorkloadGrowth Classification = "workload-growth"
	ClassCacheSawtooth  Classification = "cache-sawtooth"
	ClassUnknown        Classification = "unknown"
)

type Attribution struct {
	At      time.Time
	WindowS int64
	// identity-scoped deltas; never summed RSS (MP-ALG-004)
	Growth   []ProcessGrowth
	Coverage Coverage
	Quality  Quality
}

type ProcessGrowth struct {
	Identity     ProcessIdentity
	DeltaBytes   int64
	EvidenceRefs []string
}

type ActionKind string

// Closed allowlist (SI-1/SI-2). There is no Kill/Signal kind anywhere.
const (
	ActionTrimOwnBuffers  ActionKind = "trim_own_buffers"
	ActionRotateOwnLedger ActionKind = "rotate_own_ledger"
	ActionEmitAdvisory    ActionKind = "emit_advisory"
	ActionDeferLaunch     ActionKind = "defer_launch"
	ActionStopOwned       ActionKind = "stop_owned_child"
)

type Decision struct {
	At           time.Time
	NoAction     bool
	Reason       string
	Candidates   []Candidate
	RequiredMode string
}

type Candidate struct {
	Kind        ActionKind
	TargetID    string // empty or OwnedPid token; never a foreign pid
	ExpectBytes int64
}

type ActionResult struct {
	ActionID string
	Kind     ActionKind
	State    string // executed | refused | failed
	Verified string // pending | confirmed | refuted
	Evidence map[string]string
}

type Event struct {
	At   time.Time
	Kind string
	Data map[string]any
}

// OwnedPidRef authorizes the single signal call site (executor.stopOwnedChild).
// Constructed only from the daemon's own spawn records — never from process
// scans (SI-1).
type OwnedPidRef struct {
	Pid  int32
	Pgid int32
}
