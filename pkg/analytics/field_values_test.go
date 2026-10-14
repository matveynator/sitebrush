package analytics

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestMarkedFieldValuesExpireWithoutRemovingFormOutcomes(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	site := New(now)
	event := conversionEvent("1111111111111111", "2222222222222222", "/", 1, Signal{Kind: "page"}, Signal{Kind: "form-start", Form: "key:contact"}, Signal{Kind: "field-value", Form: "key:contact", Field: "key:plan", FieldName: "plan", FieldText: "pro"})
	if !Valid(event) || !site.Record(event, now, 4<<20) {
		t.Fatal("safe field rejected")
	}
	report := site.Report(now.Add(time.Hour), 7).Conversion
	if len(report.Forms) != 1 || len(report.Forms[0].Values) != 1 || report.Forms[0].Values[0].Text != "pro" {
		t.Fatal("field value missing")
	}
	site.Prune(now.AddDate(0, 0, 8))
	report = site.Report(now.AddDate(0, 0, 8), 30).Conversion
	if report.Forms[0].Started != 1 || len(report.Forms[0].Values) != 0 {
		t.Fatal("field expiry changed funnel outcomes")
	}
	for _, pair := range [][2]string{{"password", "password-secret"}, {"token", "opaque"}, {"plan", "4111 1111 1111 1111"}, {"plan", "Bearer opaque"}} {
		if SafeFieldValue(pair[0], pair[1]) {
			t.Fatal("sensitive value admitted", pair[0])
		}
	}
}

func TestDailyArchivesExcludeVisitorAndFieldDetails(t *testing.T) {
	now := time.Now().UTC()
	directory := t.TempDir()
	report := Report{Conversion: ConversionReport{Locations: []VisitorLocation{{Address: "192.0.2.9", Fingerprint: "fingerprint"}}, Forms: []ConversionForm{{Started: 1, Values: []FormFieldValue{{Field: "key:plan", Text: "pro"}}}}}}
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
