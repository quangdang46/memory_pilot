package model

import "testing"

// ST-2 — sealed action type: the allowlist is closed. Adding a kind here is a
// spec change (ADR-013 §5 widening), never a code convenience.
func TestActionKindAllowlistClosed(t *testing.T) {
	allowed := map[ActionKind]bool{
		ActionTrimOwnBuffers: true, ActionRotateOwnLedger: true,
		ActionEmitAdvisory: true, ActionDeferLaunch: true, ActionStopOwned: true,
	}
	for _, k := range []ActionKind{"kill", "signal", "suspend", "purge", "migrate_heap"} {
		if allowed[k] {
			t.Fatalf("forbidden action kind in allowlist: %s", k)
		}
	}
	if len(allowed) != 5 {
		t.Fatalf("allowlist size changed: %d", len(allowed))
	}
}
