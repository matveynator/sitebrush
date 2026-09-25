package crawler

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestImportFrontierPersistsOffsetsAndEnforcesStorageQuota(t *testing.T) {
	contextValue := context.Background()
	databasePath := filepath.Join(t.TempDir(), "site.db")
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	defer database.Close()
	if _, err := database.Exec(`CREATE TABLE domain_storage_usage(domain TEXT PRIMARY KEY,page_bytes INTEGER DEFAULT 0,published_page_bytes INTEGER DEFAULT 0,revision_bytes INTEGER DEFAULT 0,file_bytes INTEGER DEFAULT 0,published_static_bytes INTEGER DEFAULT 0,import_queue_bytes INTEGER DEFAULT 0,limit_bytes INTEGER NOT NULL,updated_at TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO domain_storage_usage(domain,limit_bytes) VALUES('alpha.example',100000)`); err != nil {
		t.Fatal(err)
	}
	for _, schemaStatement := range ImportFrontierSchema() {
		if _, err := database.Exec(schemaStatement); err != nil {
			t.Fatalf("create import frontier schema: %v", err)
		}
	}
	run := ImportFrontierRun{ID: "run-one", Domain: "alpha.example", PagePath: "/", SourceURL: "https://source.example/", AutoDetectTemplates: true}
	if err := CreateImportFrontier(contextValue, database, run); err != nil {
		t.Fatal(err)
	}
	rootPage := ImportFrontierPage{Key: "https://source.example/", URL: "https://source.example/", LocalPath: "/"}
	added, err := AddImportFrontierPage(contextValue, database, run.ID, run.Domain, rootPage, 1000)
	if err != nil || !added {
		t.Fatalf("add root page: added=%v err=%v", added, err)
	}
	added, err = AddImportFrontierPage(contextValue, database, run.ID, run.Domain, rootPage, 1000)
	if err != nil || added {
		t.Fatalf("duplicate page was not ignored: added=%v err=%v", added, err)
	}
	rootStorageBytes := int64(len(run.ID)+len(run.Domain)+len(rootPage.Key)+len(rootPage.URL)+len(rootPage.LocalPath)) + importFrontierEntryOverheadBytes
	_, err = AddImportFrontierPage(contextValue, database, run.ID, run.Domain, ImportFrontierPage{Key: "https://source.example/too-large", URL: "https://source.example/too-large", LocalPath: "/too-large"}, rootStorageBytes)
	if !errors.Is(err, ErrImportFrontierStorageLimit) {
		t.Fatalf("queue storage cap error = %v", err)
	}
	claimedPages, err := ClaimImportFrontierPages(contextValue, database, run.ID, run.Domain, 10)
	if err != nil || len(claimedPages) != 1 {
		t.Fatalf("claim queued page: pages=%v err=%v", claimedPages, err)
	}
	if err := UpdateImportFrontierPageCursor(contextValue, database, run.ID, run.Domain, rootPage.Key, 1200, "pending"); err != nil {
		t.Fatal(err)
	}
	if err := SetImportFrontierRunState(contextValue, database, run.ID, run.Domain, "partial"); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	database, err = sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	defer database.Close()
	storedRun, err := FindImportFrontier(contextValue, database, run.ID, run.Domain)
	if err != nil || storedRun.SourceURL != run.SourceURL || !storedRun.AutoDetectTemplates {
		t.Fatalf("stored run did not survive database reopen: run=%+v err=%v", storedRun, err)
	}
	if err := ClaimImportFrontier(contextValue, database, run.ID, run.Domain); err != nil {
		t.Fatal(err)
	}
	if err := ClaimImportFrontier(contextValue, database, run.ID, run.Domain); err == nil {
		t.Fatal("a second consumer claimed an already running import")
	}
	claimedPages, err = ClaimImportFrontierPages(contextValue, database, run.ID, run.Domain, 10)
	if err != nil || len(claimedPages) != 1 || claimedPages[0].LinkOffset != 1200 {
		t.Fatalf("resume did not restore page cursor: pages=%+v err=%v", claimedPages, err)
	}
	if err := SetImportFrontierPageState(contextValue, database, run.ID, run.Domain, rootPage.Key, "done"); err != nil {
		t.Fatal(err)
	}
	stats, err := ImportFrontierStatsForRun(contextValue, database, run.ID, run.Domain)
	if err != nil || stats.PendingPages != 0 {
		t.Fatalf("completed queue stats = %+v, err=%v", stats, err)
	}
	if err := FinishImportFrontier(contextValue, database, run.ID, run.Domain); err != nil {
		t.Fatal(err)
	}
	var queueBytes int64
	if err := database.QueryRow(`SELECT import_queue_bytes FROM domain_storage_usage WHERE domain='alpha.example'`).Scan(&queueBytes); err != nil {
		t.Fatal(err)
	}
	if queueBytes != 0 {
		t.Fatalf("finished queue left %d bytes reserved", queueBytes)
	}
}

func TestImportFrontierCannotExceedSiteStorageLimit(t *testing.T) {
	database := openFrontierTestDatabase(t)
	if _, err := database.Exec(`UPDATE domain_storage_usage SET limit_bytes=100`); err != nil {
		t.Fatal(err)
	}
	if err := CreateImportFrontier(context.Background(), database, ImportFrontierRun{ID: "run-small", Domain: "alpha.example", PagePath: "/", SourceURL: "https://source.example/"}); err != nil {
		t.Fatal(err)
	}
	_, err := AddImportFrontierPage(context.Background(), database, "run-small", "alpha.example", ImportFrontierPage{Key: "https://source.example/", URL: "https://source.example/", LocalPath: "/"}, 1000)
	if !errors.Is(err, ErrImportFrontierStorageLimit) {
		t.Fatalf("site quota error = %v", err)
	}
}

func openFrontierTestDatabase(t *testing.T) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "site.db"))
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(1)
	if _, err := database.Exec(`CREATE TABLE domain_storage_usage(domain TEXT PRIMARY KEY,page_bytes INTEGER DEFAULT 0,published_page_bytes INTEGER DEFAULT 0,revision_bytes INTEGER DEFAULT 0,file_bytes INTEGER DEFAULT 0,published_static_bytes INTEGER DEFAULT 0,import_queue_bytes INTEGER DEFAULT 0,limit_bytes INTEGER NOT NULL,updated_at TEXT)`); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO domain_storage_usage(domain,limit_bytes) VALUES('alpha.example',100000)`); err != nil {
		database.Close()
		t.Fatal(err)
	}
	for _, schemaStatement := range ImportFrontierSchema() {
		if _, err := database.Exec(schemaStatement); err != nil {
			database.Close()
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}
