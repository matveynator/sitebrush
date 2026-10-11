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

func TestSecurityCountryAudienceCountsUniqueAddressesAcrossDays(t *testing.T) {
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	state := &SecurityState{}
	for _, event := range []struct {
		age         int
		ip, country string
	}{
		{1, "192.0.2.1", "RU"}, {0, "192.0.2.1", "RU"}, {0, "192.0.2.1", "RU"},
		{0, "192.0.2.2", "RU"}, {0, "192.0.2.3", "DE"},
	} {
		state.Record(RequestObservation{Time: now.AddDate(0, 0, -event.age), IP: event.ip, Country: event.country, Path: "/.git/config", Method: "GET", Status: 404})
	}
	checkpoint, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	var restored SecurityState
	if err := json.Unmarshal(checkpoint, &restored); err != nil {
		t.Fatal(err)
	}
	report := restored.Report(now, 7)
	if got := report.CountryIPs["RU"]; !got.Known || got.Count != 2 {
		t.Fatalf("period audience: %+v", got)
	}
	if got := report.DailyCountryIPs["2026-10-11"]["RU"]; !got.Known || got.Count != 2 {
		t.Fatalf("day audience: %+v", got)
	}
	if got := restored.Report(now, 1).CountryIPs["DE"]; !got.Known || got.Count != 1 {
		t.Fatalf("country audience: %+v", got)
	}
	if got := restored.Report(now, 1).DailyCountryIPs["2026-10-10"]["RU"]; !got.Known || got.Count != 1 {
		t.Fatalf("calendar drilldown lost older day: %+v", got)
	}
	// Old aggregate counts cannot be reconstructed from newly observed addresses.
	restored.ActivityCountries["2026-10-10"]["RU"] += 100
	if restored.Report(now, 7).CountryIPs["RU"].Known {
		t.Fatal("partial legacy audience reported as exact")
	}
	if !restored.Report(now, 1).CountryIPs["RU"].Known {
		t.Fatal("legacy day damaged complete current day")
	}
	restored.Limit(1)
	if restored.Report(now, 7).CountryIPs["RU"].Known || !restored.Incomplete {
		t.Fatal("evicted audience reported as exact")
	}
}
