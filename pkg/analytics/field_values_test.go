package analytics

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFormAnalyticsRejectsFieldContentsAndKeepsStructuralOutcomes(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	site := New(now)
	unsafe := conversionEvent("1111111111111111", "2222222222222222", "/", 1,
		Signal{Kind: "page"},
		Signal{Kind: "form-start", Form: "key:contact"},
		Signal{Kind: "field-value", Form: "key:contact", Field: "key:plan", FieldName: "plan", FieldText: "selected-plan"},
	)
	if Valid(unsafe) {
		t.Fatal("field contents must never be accepted by analytics")
	}

	safe := conversionEvent("1111111111111111", "2222222222222222", "/", 1,
		Signal{Kind: "page"},
		Signal{Kind: "form-view", Form: "key:contact"},
		Signal{Kind: "form-start", Form: "key:contact"},
		Signal{Kind: "field", Form: "key:contact", Field: "key:plan", Filled: true},
		Signal{Kind: "form-submit", Form: "key:contact"},
		Signal{Kind: "form-success", Form: "key:contact"},
	)
	if !Valid(safe) || !site.Record(safe, now, 4<<20) {
		t.Fatal("structural form analytics rejected")
	}
	report := site.Report(now.Add(time.Hour), 7).Conversion
	if len(report.Forms) != 1 || report.Forms[0].Started != 1 || report.Forms[0].Submitted != 1 || report.Forms[0].Succeeded != 1 {
		t.Fatal("structural form outcomes missing", report.Forms)
	}
	if len(report.Forms[0].Values) != 0 {
		t.Fatal("form report exposed field contents")
	}
	if SafeFieldValue("plan", "selected-plan") {
		t.Fatal("field contents admitted")
	}
}

func TestDailyArchivesExcludeVisitorAndFieldDetails(t *testing.T) {
	now := time.Now().UTC()
	directory := t.TempDir()
	report := Report{Conversion: ConversionReport{Locations: []VisitorLocation{{Address: "192.0.2.9", Fingerprint: "fingerprint"}}, Forms: []ConversionForm{{Started: 1, Values: []FormFieldValue{{Field: "key:plan", Text: "legacy-value"}}}}}}
	encoded, _ := json.Marshal(map[string]Report{dayKey(now): report})
	if err := writeDailyArchive(directory, string(encoded), now); err != nil {
		t.Fatal(err)
	}
	archive, err := os.Open(filepath.Join(directory, "archives", dayKey(now)+".json.gz"))
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	decoded, err := DecodeArchive(archive)
	if err != nil || len(decoded.Conversion.Locations) != 0 || len(decoded.Conversion.Forms[0].Values) != 0 || decoded.Conversion.Forms[0].Started != 1 {
		t.Fatal("archive contains visitor details or lost aggregates", err)
	}
}

func TestVisitorMapDoesNotTruncateAtTwoHundred(t *testing.T) {
	now := time.Now().UTC()
	site := New(now)
	for index := 0; index < 201; index++ {
		identifier := fmt.Sprintf("%016x", index+1)
		event := conversionEvent(identifier, identifier, "/", 1, Signal{Kind: "page"})
		event.Address = "192.0.2.9"
		event.Location = VisitorLocation{Address: event.Address, Known: true, Latitude: 51.5, Longitude: -0.1}
		if !site.Record(event, now, 8<<20) {
			t.Fatal("observation rejected", index)
		}
	}
	if len(site.Report(now, 7).Conversion.Locations) != 201 {
		t.Fatal("map hid visitors after the two hundredth")
	}
}
