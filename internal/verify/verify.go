// Package verify: post-action verification verdicts (ST-10: unverified by
// default). Actions never report "succeeded" — only "executed" + verdict.
package verify

import (
	"sync"
	"time"

)

type Verifier struct {
	mu      sync.Mutex
	pending map[string]verifyJob
	results map[string]string
}

type verifyJob struct {
	windowEnd time.Time
	before    float64 // pressure score before
}

type Verdict string

const (
	Unverified Verdict = "unverified"
	Confirmed  Verdict = "confirmed"
	Refuted    Verdict = "refuted"
)

func New() *Verifier {
	return &Verifier{pending: map[string]verifyJob{}, results: map[string]string{}}
}

func (v *Verifier) Schedule(actionID string, before float64, window time.Duration, now time.Time) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.pending[actionID] = verifyJob{windowEnd: now.Add(window), before: before}
}

// Resolve records the observed effect for an action whose window elapsed.
func (v *Verifier) Resolve(actionID string, scoreAfter float64, now time.Time) {
	v.mu.Lock()
	defer v.mu.Unlock()
	j, ok := v.pending[actionID]
	if !ok || now.Before(j.windowEnd) {
		return // window not elapsed: stays unverified
	}
	if scoreAfter < j.before*0.95 {
		v.results[actionID] = "confirmed"
	} else {
		v.results[actionID] = "refuted"
	}
	delete(v.pending, actionID)
}

func (v *Verifier) Result(actionID string) (string, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	r, ok := v.results[actionID]
	return r, ok
}

// PendingUnverified reports actions still inside their window.
func (v *Verifier) PendingUnverified() int {
	v.mu.Lock()
	defer v.mu.Unlock()
	return len(v.pending)
}
