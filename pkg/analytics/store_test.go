package analytics

import (
	"compress/gzip"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/matveynator/sitebrush/v2/pkg/database/drivers"
)

func TestStoreIsolatedFromEditingAndDeletion(t *testing.T) {
	root := t.TempDir()
	editor, err := sql.Open("sqlite", filepath.Join(root, "site.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer editor.Close()
	editor.SetMaxOpenConns(1)
	if _, err := editor.Exec(`CREATE TABLE pages(html TEXT); INSERT INTO pages VALUES('keep'); BEGIN EXCLUSIVE`); err != nil {
		t.Fatal(err)
	}
	store := OpenStore(filepath.Join(root, "analytics"))
	defer store.Close()
	now := time.Now().UTC()
	report := Report{Generated: now, PeriodEnd: now, Views: 12}
	archive, _ := json.Marshal(map[string]Report{dayKey(now): report})
	request := StorageRequest{Operation: SaveBrowser, Domain: "example.org", State: `{"Visitors":{"private-identity":{}}}`, Report: `{"1":{"Views":12}}`, Archive: string(archive)}
	if result := store.Exchange(request); result.Err != nil {
		t.Fatal(result.Err)
	}
	if _, err := editor.Exec(`COMMIT`); err != nil {
		t.Fatal(err)
	}
	var html string
	if err := editor.QueryRow(`SELECT html FROM pages`).Scan(&html); err != nil || html != "keep" {
		t.Fatal("editing database changed", err)
	}
	directory, err := storeDirectory(filepath.Join(root, "analytics"), "example.org")
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(filepath.Join(directory, "archives", dayKey(now)+".json.gz"))
	if err != nil {
		t.Fatal(err)
	}
	compressor, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(compressor)
	_ = compressor.Close()
	_ = file.Close()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "private-identity") || !strings.Contains(string(body), `"Views":12`) {
		t.Fatal("archive was not an anonymous summary")
	}
	if err := os.Remove(filepath.Join(directory, "analytics.db")); err != nil {
		t.Fatal(err)
	}
	if result := store.Exchange(StorageRequest{Operation: ReadBrowserReport, Domain: "example.org"}); !errors.Is(result.Err, sql.ErrNoRows) {
		t.Fatalf("deleted database was restored unexpectedly: %v", result.Err)
	}
	if err := editor.QueryRow(`SELECT html FROM pages`).Scan(&html); err != nil || html != "keep" {
		t.Fatal("deleting analytics damaged the site")
	}
	if result := store.Exchange(StorageRequest{Operation: SaveTechnical, Domain: "other.example", Report: `{"requests":1}`}); result.Err != nil {
		t.Fatal(result.Err)
	}
	if result := store.Exchange(StorageRequest{Operation: ReadTechnicalReport, Domain: "example.org"}); !errors.Is(result.Err, sql.ErrNoRows) {
		t.Fatal("domains were not isolated")
	}
}

func TestArchiveRetentionQuotaAndCompletedDay(t *testing.T) {
	directory := t.TempDir()
	now := time.Date(2026, 9, 21, 1, 0, 0, 0, time.UTC)
	partial := Report{Views: 1, PeriodEnd: now.Add(-2 * time.Hour)}
	encoded, _ := json.Marshal(map[string]Report{"2026-09-20": partial})
	if err := writeDailyArchive(directory, string(encoded), now.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	complete := Report{Views: 9, PeriodEnd: dayStart(now).Add(-time.Nanosecond)}
	encoded, _ = json.Marshal(map[string]Report{"2026-09-20": complete})
	if err := writeDailyArchive(directory, string(encoded), now); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "archives", "2026-09-20.json.gz")
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeArchive(file)
	_ = file.Close()
	if err != nil || decoded.Views != 9 {
		t.Fatalf("partial archive not finalized: %+v %v", decoded, err)
	}
	old := filepath.Join(directory, "archives", dayKey(now.AddDate(0, 0, -91))+".json.gz")
	if err := os.WriteFile(old, []byte("expired"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := pruneArchives(filepath.Join(directory, "archives"), now, archiveLimitBytes); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("expired archive retained")
	}
	if err := pruneArchives(filepath.Join(directory, "archives"), now, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("archive size quota ignored")
	}
}

func TestStoreContainsOnlyThreeBoundedCheckpoints(t *testing.T) {
	root := t.TempDir()
	store := OpenStore(root)
	defer store.Close()
	for index := 0; index < 5; index++ {
		for _, request := range []StorageRequest{{Operation: SaveBrowser, Domain: "example.org", State: `{}`, Report: `{}`}, {Operation: SaveTechnical, Domain: "example.org", Report: `{}`}} {
			if result := store.Exchange(request); result.Err != nil {
				t.Fatal(result.Err)
			}
		}
	}
	directory, _ := storeDirectory(root, "example.org")
	database, err := sql.Open("sqlite", filepath.Join(directory, "analytics.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM checkpoints`).Scan(&count); err != nil || count != 3 {
		t.Fatalf("checkpoint count=%d err=%v", count, err)
	}
	info, err := os.Stat(filepath.Join(directory, "analytics.db"))
	if err != nil || info.Size() > databaseLimitBytes {
		t.Fatal("database size limit exceeded", err)
	}
}
