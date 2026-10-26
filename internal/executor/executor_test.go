package executor

import (
	"testing"
	"time"

	"memory_pilot/internal/model"
)

// An advisory is a record, not an intervention. Policy offers one on every Warn
// tick — 720/hour against a 60/hour cap — so the action rate limiter counted
// attempts rather than emissions: measured, 193 of 261 attempts were refused,
// including at Critical where the refusal silenced the only output the daemon
// had while the machine was out of memory.
func TestAdvisoryIsNotActionRateLimited(t *testing.T) {
	rl := newRateLimiter()
	now := time.Unix(1793000000, 0)
	// Burn well past the 60/hour cap the way the policy does: one per tick.
	for i := 0; i < 200; i++ {
		ok, why := rl.allow(model.ActionEmitAdvisory, now.Add(time.Duration(i)*5*time.Second))
		if !ok {
			t.Fatalf("attempt %d refused: %s", i, why)
		}
	}
	// A real intervention is still limited.
	allowed := 0
	for i := 0; i < 20; i++ {
		if ok, _ := rl.allow(model.ActionStopOwned, now.Add(time.Duration(i)*time.Second)); ok {
			allowed++
		}
	}
	if allowed > 2 {
		t.Errorf("L2 stop allowed %d times in an hour, want at most 2", allowed)
	}
}
