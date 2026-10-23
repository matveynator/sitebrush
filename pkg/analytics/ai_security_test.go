package analytics

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestAISecuritySummaryUsesOnlyBoundedAggregates(t *testing.T) {
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	state := SecurityState{}
	for _, at := range []time.Time{now, now.AddDate(0, 0, -8)} {
		state.Record(RequestObservation{Time: at, IP: "192.0.2.19", Path: "/.git/config", Query: "secret=private-value", Agent: "private-agent", Status: 404})
	}
	summary := AISecurityView(state.Report(now, 7), "example.org", now, 7, true, 20, 0)
	encoded, err := json.Marshal(summary)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"192.0.2.19", "private-value", "private-agent", "/.git/config"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatal("private detail exposed", secret)
		}
	}
	if summary.Requests != 1 || summary.Errors != 1 || summary.SuspiciousEvents != 1 || len(summary.Daily) != 1 || summary.BlockedRequests != nil {
		t.Fatalf("wrong period/coverage: %+v", summary)
	}
	missing := AISecurityView(SecurityReport{}, "example.org", now, 7, false, 20, 0)
	if missing.Available {
		t.Fatal("missing evidence reported as available")
	}
}
