package main

import (
	"strings"
	"testing"
)

// The status line reports the ledger's size on disk, which survives the write
// path dying. Measured: "ledger 40.8MB" printed for 31 hours of no writes.
func TestLedgerWarningIsSilentWhenHealthy(t *testing.T) {
	if w := ledgerWarning(true, ""); w != "" {
		t.Fatalf("healthy ledger produced a warning: %q", w)
	}
}

func TestLedgerWarningNamesTheFailure(t *testing.T) {
	w := ledgerWarning(false, "write ledger.0001.jsonl: file already closed")
	if !strings.Contains(w, "not accepting writes") {
		t.Fatalf("warning does not say writes are failing: %q", w)
	}
	if !strings.Contains(w, "file already closed") {
		t.Fatalf("warning hides the reason: %q", w)
	}
	if !strings.Contains(w, "nothing is being recorded") {
		t.Fatalf("warning does not state the consequence: %q", w)
	}
}

func TestLedgerWarningWithoutAReasonStillWarns(t *testing.T) {
	w := ledgerWarning(false, "")
	if !strings.Contains(w, "not accepting writes") {
		t.Fatalf("unhealthy with no reason produced no warning: %q", w)
	}
	if !strings.Contains(w, "unknown") {
		t.Fatalf("missing reason should say unknown: %q", w)
	}
}
