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
	order   []string // insertion order, for bounded retention
}

// maxResults bounds retained verdicts. An action's verdict matters while a
// report can still mention it; keeping every verdict for the process lifetime
// is a leak in the component that exists to catch leaks.
const maxResults = 2048

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

// ResolveDue resolves every action whose window has elapsed. Called once per
// tick by the daemon; without it the pending map only ever grew and no action
// ever received a verdict.
func (v *Verifier) ResolveDue(scoreAfter float64, now time.Time) int {
	v.mu.Lock()
	defer v.mu.Unlock()
	n := 0
	for id, j := range v.pending {
		if now.Before(j.windowEnd) {
			continue
		}
		if scoreAfter < j.before*0.95 {
			v.record(id, "confirmed")
		} else {
			v.record(id, "refuted")
		}
		delete(v.pending, id)
		n++
	}
	return n
}

func (v *Verifier) record(id, verdict string) {
	v.results[id] = verdict
	v.order = append(v.order, id)
	if len(v.order) > maxResults {
		drop := len(v.order) - maxResults
		for _, old := range v.order[:drop] {
			delete(v.results, old)
		}
		v.order = append([]string(nil), v.order[drop:]...)
	}
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
		v.record(actionID, "confirmed")
	} else {
		v.record(actionID, "refuted")
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
