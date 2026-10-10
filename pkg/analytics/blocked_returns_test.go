package analytics

import (
	"encoding/json"
	"testing"
	"time"
)

func TestBlockedReturnsRequirePriorBlockAndCountVisitsSeparately(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	state := &SecurityState{}
	observation := RequestObservation{Time: now, IP: "192.0.2.1", Path: "/", Method: "GET", Agent: "Mozilla/5.0", Status: 429, Blocked: true}
	state.Record(observation)
	if len(state.BlockedReturns) != 0 {
		t.Fatal("first block counted as a return")
	}
	observation.AlreadyBlocked = true
	for index := 0; index < 50; index++ {
		observation.Time = now.Add(time.Duration(index+1) * time.Second)
		state.Record(observation)
	}
	checkpoint, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	var restored SecurityState
	if err := json.Unmarshal(checkpoint, &restored); err != nil {
		t.Fatal(err)
	}
	observation.Time = now.Add(32 * time.Minute)
	restored.Record(observation)
	report := restored.Report(observation.Time, 365)
	if len(report.BlockedReturns) != 1 || report.BlockedReturns[0].Requests != 51 || report.BlockedReturns[0].Visits != 2 {
		t.Fatalf("repeat evidence: %+v", report.BlockedReturns)
	}
	observation.IP, observation.Status, observation.AlreadyBlocked = "192.0.2.2", 403, false
	restored.Record(observation)
	if len(restored.BlockedReturns) != 1 {
		t.Fatal("generic HTTP denial counted as a previously blocked IP")
	}
	restored.Prune(now.AddDate(0, 0, 31))
	if len(restored.BlockedReturns) != 0 {
		t.Fatal("expired repeat evidence retained")
	}
}
