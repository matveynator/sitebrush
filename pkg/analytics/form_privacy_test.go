package analytics

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStoreRemovesLegacyFieldContentsWithoutResettingCounts(t *testing.T) {
	root := t.TempDir()
	store := OpenStore(root)
	defer store.Close()
	legacy := `{"conversion":{"Visitors":{"browser":{"Fingerprint":"legacy-fingerprint","Client":{"UserAgent":"legacy-agent"},"Forms":{"contact":{"Started":true,"LastField":"key:plan","Values":{"key:plan":{"Text":"legacy-secret"}}}}}},"Daily":{"2026-10-01":{"Visitors":9007199254740993}}}}`
	if result := store.Exchange(StorageRequest{Operation: SaveBrowser, Domain: "example.org", State: legacy, Report: `{"7":{"Conversion":{"Forms":[{"Started":1,"Values":[{"Text":"legacy-secret"}]}]}}}`}); result.Err != nil {
		t.Fatal(result.Err)
	}
	for _, operation := range []Operation{LoadHistory, ReadBrowserReport} {
		result := store.Exchange(StorageRequest{Operation: operation, Domain: "example.org"})
		if result.Err != nil || strings.Contains(result.Text, "legacy-") {
			t.Fatal("obsolete details survived storage read", result)
		}
		if operation == LoadHistory && (!strings.Contains(result.Text, "9007199254740993") || !strings.Contains(result.Text, "key:plan")) {
			t.Fatal("migration changed counts or structural field identifiers", result.Text)
		}
	}
	database, err := sql.Open("sqlite", filepath.Join(root, "site-example.org", DatabaseName("example.org")))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	rows, err := database.Query(`SELECT body FROM checkpoints`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var body string
		if err := rows.Scan(&body); err != nil || strings.Contains(body, "legacy-") {
			t.Fatal("stored checkpoint still contains removed details", body, err)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestFormAnalyticsRejectsFieldContentsAndKeepsStructuralOutcomes(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	site := New(now)
	unsafe := conversionEvent("1111111111111111", "2222222222222222", "/", 1,
		Signal{Kind: "page"},
		Signal{Kind: "form-start", Form: "key:contact"},
		Signal{Kind: "field-value", Form: "key:contact", Field: "key:plan"},
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
	if len(report.Forms) != 1 || report.Forms[0].Started != 1 || report.Forms[0].Submitted != 1 || report.Forms[0].Completed != 1 {
		t.Fatal("structural form outcomes missing", report.Forms)
	}
}

func TestDailyArchivesExcludeVisitorAndFieldDetails(t *testing.T) {
	now := time.Now().UTC()
	directory := t.TempDir()
	encoded := []byte(fmt.Sprintf(`{"%s":{"Conversion":{"Locations":[{"Address":"192.0.2.9","Fingerprint":"legacy-fingerprint"}],"Forms":[{"Started":1,"Values":[{"Field":"key:plan","Text":"legacy-value"}]}]}}}`, dayKey(now)))
	if err := writeDailyArchive(directory, string(encoded), now); err != nil {
		t.Fatal(err)
	}
	archive, err := os.Open(filepath.Join(directory, "archives", dayKey(now)+".json.gz"))
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	decoded, err := DecodeArchive(archive)
	sanitized, _ := json.Marshal(decoded)
	if strings.Contains(string(sanitized), "legacy-value") || strings.Contains(string(sanitized), "legacy-fingerprint") {
		t.Fatal("legacy personal details survived archive decoding")
	}
	if err != nil || len(decoded.Conversion.Locations) != 0 || decoded.Conversion.Forms[0].Started != 1 {
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
