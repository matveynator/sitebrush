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

func TestImportFrontierTransitionsAndStartupRecovery(t *testing.T) {
	contextValue := context.Background()
	database := openFrontierTestDatabase(t)
	run := ImportFrontierRun{ID: "run-recovery", Domain: "alpha.example", PagePath: "/copy", SourceURL: "https://source.example/", AutoDetectTemplates: true}
	if err := CreateImportFrontier(contextValue, database, run); err != nil {
		t.Fatal(err)
	}
	page := ImportFrontierPage{Key: "https://source.example/", URL: "https://source.example/", LocalPath: "/copy"}
	if _, err := AddImportFrontierPage(contextValue, database, run.ID, run.Domain, page, 1000); err != nil {
		t.Fatal(err)
	}
	if err := SetImportFrontierRunState(contextValue, database, run.ID, run.Domain, "partial"); err != nil {
		t.Fatal(err)
	}
	activeRun, err := FindActiveImportFrontier(contextValue, database, run.Domain, run.PagePath, run.SourceURL)
	if err != nil || activeRun.ID != run.ID || !activeRun.AutoDetectTemplates {
		t.Fatalf("active import = %+v, err=%v", activeRun, err)
	}
	if _, err := FindActiveImportFrontier(contextValue, database, run.Domain, "/missing", run.SourceURL); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing active import error = %v", err)
	}
	if pages, err := ClaimImportFrontierPages(contextValue, database, run.ID, run.Domain, 0); err != nil || len(pages) != 0 {
		t.Fatalf("zero-sized claim = %+v, err=%v", pages, err)
	}
	claimedPages, err := ClaimImportFrontierPages(contextValue, database, run.ID, run.Domain, 1)
	if err != nil || len(claimedPages) != 1 {
		t.Fatalf("claim frontier page: pages=%+v err=%v", claimedPages, err)
	}
	if err := ReleaseProcessingImportFrontierPages(contextValue, database, run.ID, run.Domain); err != nil {
		t.Fatal(err)
	}
	if err := SetImportFrontierPageState(contextValue, database, run.ID, run.Domain, page.Key, "invalid"); err == nil {
		t.Fatal("invalid page state was accepted")
	}
	if err := SetImportFrontierPageState(contextValue, database, run.ID, run.Domain, "missing", "done"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("missing page state error = %v", err)
	}
	if err := UpdateImportFrontierPageCursor(contextValue, database, run.ID, run.Domain, page.Key, -1, "pending"); err == nil {
		t.Fatal("negative page offset was accepted")
	}
	if err := UpdateImportFrontierPageCursor(contextValue, database, run.ID, run.Domain, page.Key, 3, "invalid"); err == nil {
		t.Fatal("invalid page cursor state was accepted")
	}
	if err := SetImportFrontierPageState(contextValue, database, run.ID, run.Domain, page.Key, "processing"); err != nil {
		t.Fatal(err)
	}
	listedPages, err := ListImportFrontierPages(contextValue, database, run.ID, run.Domain)
	if err != nil || len(listedPages) != 1 || listedPages[0].LocalPath != page.LocalPath {
		t.Fatalf("list persisted import pages = %+v, err=%v", listedPages, err)
	}
	if err := RecoverImportFrontiers(contextValue, database); err != nil {
		t.Fatal(err)
	}
	if err := ClaimImportFrontier(contextValue, database, run.ID, run.Domain); err != nil {
		t.Fatal(err)
	}
	if err := ClaimImportFrontier(contextValue, database, run.ID, run.Domain); err == nil {
		t.Fatal("recovered running import was claimed twice")
	}
	claimedPages, err = ClaimImportFrontierPages(contextValue, database, run.ID, run.Domain, 1)
	if err != nil || len(claimedPages) != 1 {
		t.Fatalf("startup did not recover processing page: pages=%+v err=%v", claimedPages, err)
	}
	if err := SetImportFrontierRunState(contextValue, database, run.ID, run.Domain, "partial"); err != nil {
		t.Fatal(err)
	}
	if err := DiscardPartialImportFrontiers(contextValue, database, run.Domain, run.PagePath); err != nil {
		t.Fatal(err)
	}
	if _, err := FindImportFrontier(contextValue, database, run.ID, run.Domain); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("discarded import lookup error = %v", err)
	}
	var reservedBytes int64
	if err := database.QueryRow(`SELECT import_queue_bytes FROM domain_storage_usage WHERE domain=?`, run.Domain).Scan(&reservedBytes); err != nil || reservedBytes != 0 {
		t.Fatalf("discarded queue accounting = %d, err=%v", reservedBytes, err)
	}
}

func TestImportFrontierValidationAndInterruptedCleanup(t *testing.T) {
	contextValue := context.Background()
	database := openFrontierTestDatabase(t)
	if err := CreateImportFrontier(contextValue, database, ImportFrontierRun{}); err == nil {
		t.Fatal("incomplete import identity was accepted")
	}
	if err := CreateImportFrontier(contextValue, database, ImportFrontierRun{ID: "run-duplicate", Domain: "alpha.example", SourceURL: "https://source.example/"}); err != nil {
		t.Fatal(err)
	}
	if err := CreateImportFrontier(contextValue, database, ImportFrontierRun{ID: "run-duplicate", Domain: "alpha.example", SourceURL: "https://source.example/"}); err == nil {
		t.Fatal("duplicate import identity was accepted")
	}
	if _, err := AddImportFrontierPage(contextValue, database, "", "alpha.example", ImportFrontierPage{}, 1000); err == nil {
		t.Fatal("incomplete page identity was accepted")
	}
	if _, err := database.Exec(`CREATE TRIGGER reject_queue_reservation BEFORE UPDATE ON domain_storage_usage BEGIN SELECT RAISE(ABORT,'reservation rejected'); END`); err != nil {
		t.Fatal(err)
	}
	page := ImportFrontierPage{Key: "https://source.example/reservation", URL: "https://source.example/reservation", LocalPath: "/reservation"}
	if _, err := AddImportFrontierPage(contextValue, database, "run-duplicate", "alpha.example", page, 1000); err == nil {
		t.Fatal("storage reservation failure was ignored")
	}
	if _, err := database.Exec(`DROP TRIGGER reject_queue_reservation`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`CREATE TRIGGER reject_frontier_insert BEFORE INSERT ON whole_site_import_pages BEGIN SELECT RAISE(ABORT,'page rejected'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := AddImportFrontierPage(contextValue, database, "run-duplicate", "alpha.example", page, 1000); err == nil {
		t.Fatal("frontier insert failure was ignored")
	}
	var queueBytes int64
	if err := database.QueryRow(`SELECT import_queue_bytes FROM domain_storage_usage WHERE domain='alpha.example'`).Scan(&queueBytes); err != nil || queueBytes != 0 {
		t.Fatalf("failed enqueue retained %d reserved bytes, err=%v", queueBytes, err)
	}
	if _, err := database.Exec(`DROP TRIGGER reject_frontier_insert`); err != nil {
		t.Fatal(err)
	}
	run := ImportFrontierRun{ID: "run-discarding", Domain: "alpha.example", PagePath: "/", SourceURL: "https://source.example/"}
	if err := CreateImportFrontier(contextValue, database, run); err != nil {
		t.Fatal(err)
	}
	page = ImportFrontierPage{Key: "https://source.example/", URL: "https://source.example/", LocalPath: "/"}
	if _, err := AddImportFrontierPage(contextValue, database, run.ID, run.Domain, page, 1000); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE whole_site_imports SET state='discarding' WHERE import_id=?`, run.ID); err != nil {
		t.Fatal(err)
	}
	if err := CleanupDiscardedImportFrontiers(contextValue, database); err != nil {
		t.Fatal(err)
	}
	if _, err := FindImportFrontier(contextValue, database, run.ID, run.Domain); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("cleaned import lookup error = %v", err)
	}
}

func TestImportFrontierDatabaseFailuresAreReturned(t *testing.T) {
	database := openFrontierTestDatabase(t)
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	contextValue := context.Background()
	if err := CreateImportFrontier(contextValue, database, ImportFrontierRun{ID: "run", Domain: "alpha.example", SourceURL: "https://source.example/"}); err == nil {
		t.Fatal("create succeeded with a closed database")
	}
	if _, err := FindImportFrontier(contextValue, database, "run", "alpha.example"); err == nil {
		t.Fatal("find succeeded with a closed database")
	}
	if _, err := FindActiveImportFrontier(contextValue, database, "alpha.example", "/", "https://source.example/"); err == nil {
		t.Fatal("find active succeeded with a closed database")
	}
	if err := ClaimImportFrontier(contextValue, database, "run", "alpha.example"); err == nil {
		t.Fatal("claim succeeded with a closed database")
	}
	page := ImportFrontierPage{Key: "https://source.example/", URL: "https://source.example/", LocalPath: "/"}
	if _, err := AddImportFrontierPage(contextValue, database, "run", "alpha.example", page, 1000); err == nil {
		t.Fatal("enqueue succeeded with a closed database")
	}
	if _, err := ClaimImportFrontierPages(contextValue, database, "run", "alpha.example", 1); err == nil {
		t.Fatal("page claim succeeded with a closed database")
	}
	if err := SetImportFrontierPageState(contextValue, database, "run", "alpha.example", page.Key, "pending"); err == nil {
		t.Fatal("page state update succeeded with a closed database")
	}
	if err := UpdateImportFrontierPageCursor(contextValue, database, "run", "alpha.example", page.Key, 0, "pending"); err == nil {
		t.Fatal("page cursor update succeeded with a closed database")
	}
	if err := ReleaseProcessingImportFrontierPages(contextValue, database, "run", "alpha.example"); err == nil {
		t.Fatal("page release succeeded with a closed database")
	}
	if _, err := ImportFrontierStatsForRun(contextValue, database, "run", "alpha.example"); err == nil {
		t.Fatal("frontier stats succeeded with a closed database")
	}
	if err := FinishImportFrontier(contextValue, database, "run", "alpha.example"); err == nil {
		t.Fatal("frontier finish succeeded with a closed database")
	}
	if err := DiscardPartialImportFrontiers(contextValue, database, "alpha.example", "/"); err == nil {
		t.Fatal("frontier discard succeeded with a closed database")
	}
	if err := CleanupDiscardedImportFrontiers(contextValue, database); err == nil {
		t.Fatal("frontier cleanup succeeded with a closed database")
	}
	if err := RecoverImportFrontiers(contextValue, database); err == nil {
		t.Fatal("frontier recovery succeeded with a closed database")
	}
	if _, err := ListImportFrontierPages(contextValue, database, "run", "alpha.example"); err == nil {
		t.Fatal("frontier page listing succeeded with a closed database")
	}
	if err := SetImportFrontierRunState(contextValue, database, "run", "alpha.example", "invalid"); err == nil {
		t.Fatal("invalid run state was accepted")
	}
	if err := SetImportFrontierRunState(contextValue, database, "run", "alpha.example", "partial"); err == nil {
		t.Fatal("run state update succeeded with a closed database")
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
