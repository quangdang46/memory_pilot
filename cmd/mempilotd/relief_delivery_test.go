package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"memory_pilot/internal/ledger"
	"memory_pilot/internal/model"
)

// On 2026-10-26 this machine reached Critical fourteen times. Each time a
// relief recommendation was written to the ledger and nothing else happened:
// no notification, no message, nothing the owner would see. The board existed
// as a log entry and not as a warning.
//
// This drives the same function with the same inputs and requires that a
// delivery attempt is recorded for every recommendation emitted at Warn or
// above.
func TestReliefRecommendationIsDeliveredNotJustLogged(t *testing.T) {
	dir := t.TempDir()
	lg, err := ledger.Open(dir, "ledger")
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()
	h := &daemonHandler{lg: lg}
	at := time.Unix(1793000000, 0)

	h.emitReliefIfChanged(at, "closing 5 abandoned workload(s) would return ~601 MB "+
		"shortfall is 3181 MB — this will NOT cover it", 601, 3181, 22, model.LevelCritical)

	recs := readLedger(t, lg)
	var recsCount, notifs int
	var notif map[string]any
	for _, r := range recs {
		switch r["kind"] {
		case "relief_recommendation":
			recsCount++
		case "advisory_notification":
			notifs++
			notif = r
		}
	}
	if recsCount != 1 {
		t.Fatalf("%d relief recommendations, want 1", recsCount)
	}
	// The notification goroutine may not have run yet.
	deadline := time.Now().Add(3 * time.Second)
	for notifs == 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		recs = readLedger(t, lg)
		notifs = 0
		for _, r := range recs {
			if r["kind"] == "advisory_notification" {
				notifs++
				notif = r
			}
		}
	}
	if notifs == 0 {
		t.Fatal("BOARD DID NOT SPEAK: a Critical recommendation was logged and never delivered")
	}
	if g, _ := notif["group"].(string); g != "relief" {
		t.Errorf("notification not attributed to the relief path: %v", notif)
	}
	if _, ok := notif["delivered"]; !ok {
		t.Errorf("notification record does not say whether it was delivered: %v", notif)
	}
}

// Nothing below Warn may interrupt the owner. A comfortable machine with a
// releasable idle process is not news.
func TestNoNotificationBelowWarn(t *testing.T) {
	dir := t.TempDir()
	lg, err := ledger.Open(dir, "ledger")
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()
	h := &daemonHandler{lg: lg}
	h.emitReliefIfChanged(time.Unix(1793000000, 0), "~300 MB could be released", 300, 0, 2,
		model.LevelNormal)
	time.Sleep(300 * time.Millisecond)
	for _, r := range readLedger(t, lg) {
		if r["kind"] == "advisory_notification" {
			t.Fatalf("notified the owner at Normal pressure: %v", r)
		}
	}
}

// The notification body must be readable in a banner and must carry the
// command. The full recommendation text is many lines; Notification Center
// shows two and truncates, which would push the actionable part out of sight.
func TestReliefNotificationBodyIsShortAndActionable(t *testing.T) {
	for _, c := range []struct {
		name                   string
		reliefMB, shortfallMB  int64
		wantCovered, wantWords bool
	}{
		{"covers the gap", 3181, 601, true, true},
		{"falls short", 601, 3181, false, true},
		{"nothing releasable", 0, 900, false, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			body := reliefNotificationBody("(ignored long text)", c.reliefMB, c.shortfallMB)
			if len(body) > 160 {
				t.Errorf("notification body is %d chars, too long for a banner: %q",
					len(body), body)
			}
			if !strings.Contains(body, "mempilot relief") {
				t.Errorf("body carries no command: %q", body)
			}
			if strings.Contains(body, "\n") {
				t.Errorf("body is multi-line: %q", body)
			}
			covered := strings.Contains(body, "enough to cover")
			if covered != c.wantCovered {
				t.Errorf("covered=%v, want %v: %q", covered, c.wantCovered, body)
			}
		})
	}
}

func readLedger(t *testing.T, lg *ledger.Ledger) []map[string]any {
	t.Helper()
	raw, err := lg.ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	for _, r := range raw {
		var m map[string]any
		if json.Unmarshal(r, &m) == nil {
			out = append(out, m)
		}
	}
	return out
}
