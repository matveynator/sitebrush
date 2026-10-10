package analytics

import (
	"encoding/json"
	"testing"
	"time"
)

func TestSecurityDailyCountriesSurviveCheckpointAndBucketRetention(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	state := &SecurityState{}
	for _, event := range []struct {
		age     int
		country string
	}{{40, "US"}, {1, "DE"}, {0, "RU"}, {0, "RU"}} {
		state.Record(RequestObservation{Time: now.AddDate(0, 0, -event.age), IP: "192.0.2.1", Country: event.country, Path: "/.git/config", Method: "GET", Agent: "curl/8", Status: 404})
	}
	state.Prune(now)
	checkpoint, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	var restored SecurityState
	if err := json.Unmarshal(checkpoint, &restored); err != nil {
		t.Fatal(err)
	}
	report := restored.Report(now, 365)
	if report.ActivityCountries["2026-10-10"]["RU"] != 2 || report.ActivityCountries["2026-10-09"]["DE"] != 1 || report.ActivityCountries["2026-08-31"]["US"] != 1 {
		t.Fatalf("daily country evidence: %+v", report.ActivityCountries)
	}
	total := 0
	for _, country := range report.Countries {
		total += country.Count
	}
	if total != 4 {
		t.Fatalf("all-day country total=%d, want 4", total)
	}
}
