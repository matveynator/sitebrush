package sitefreeze

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestCapturePaginatesLargeSiteAndRetainsEveryBaseline(t *testing.T) {
	database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "pages.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	for _, statement := range []string{
		`CREATE TABLE pages(domain TEXT,path TEXT,title TEXT,html TEXT,published INTEGER,PRIMARY KEY(domain,path))`,
		`CREATE TABLE published_pages(domain TEXT,path TEXT,title TEXT,html TEXT,PRIMARY KEY(domain,path))`,
		`CREATE TABLE page_pending_deletions(domain TEXT,path TEXT,PRIMARY KEY(domain,path))`,
		`CREATE TABLE revisions(id INTEGER PRIMARY KEY AUTOINCREMENT,domain TEXT,page_path TEXT,html TEXT,created_at TEXT,is_active INTEGER)`,
		`CREATE INDEX idx_revisions_domain_page_id ON revisions(domain,page_path,id)`,
		`CREATE INDEX idx_revisions_domain_page_active_id ON revisions(domain,page_path,is_active,id)`,
		`CREATE TABLE domain_storage_usage(domain TEXT PRIMARY KEY,page_bytes INTEGER,published_page_bytes INTEGER,revision_bytes INTEGER,file_bytes INTEGER,published_static_bytes INTEGER,import_queue_bytes INTEGER,analytics_bytes INTEGER,analytics_reserved_bytes INTEGER,limit_bytes INTEGER)`,
		`INSERT INTO domain_storage_usage VALUES('example.org',0,0,0,0,0,0,0,0,1000000)`,
	} {
		if _, err := database.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	for number := 0; number < 130; number++ {
		pagePath := fmt.Sprintf("/%04d", number)
		if _, err := database.Exec(`INSERT INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,0)`, "example.org", pagePath, pagePath, "<p>content</p>"); err != nil {
			t.Fatal(err)
		}
	}
	transaction, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	point, err := Capture(transaction, "example.org", true)
	if err != nil {
		transaction.Rollback()
		t.Fatal(err)
	}
	if err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}
	if len(point.Pages) != 130 || len(point.Revisions) != 130 || point.Pages[129].Path != "/0129" {
		t.Fatalf("incomplete large checkpoint: pages=%d revisions=%d last=%q", len(point.Pages), len(point.Revisions), point.Pages[len(point.Pages)-1].Path)
	}
}

func TestRetainKnownUsesInventoryDigestAndRejectsChangedFile(t *testing.T) {
	storage := t.TempDir()
	name := filepath.Join("storage", "files", "example.org", "large.txt")
	if err := os.MkdirAll(filepath.Join(storage, filepath.Dir(name)), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storage, name), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(storage)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	files, err := Inventory(root, filepath.Dir(name), "files", nil)
	if err != nil || len(files) != 1 {
		t.Fatalf("inventory: files=%v err=%v", files, err)
	}
	if err := RetainKnown(root, "example.org", name, files[0].Hash, files[0].Bytes); err != nil {
		t.Fatal(err)
	}
	if err := RetainKnown(root, "example.org", name, files[0].Hash, files[0].Bytes); err != nil {
		t.Fatalf("reusing a retained object: %v", err)
	}
	if digest, _, err := HashFile(root, ObjectPath("example.org", files[0].Hash)); err != nil || digest != files[0].Hash {
		t.Fatalf("retained body: digest=%s err=%v", digest, err)
	}
	if err := os.WriteFile(filepath.Join(storage, name), []byte("changed file"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := RetainKnown(root, "example.org", name, files[0].Hash, files[0].Bytes); err == nil {
		t.Fatal("changed file size was accepted as the inventoried version")
	}
}

func TestPreviewIgnoresTokenConsumptionWithoutChangingSnapshot(t *testing.T) {
	before := Checkpoint{Properties: []Property{{Table: "file_access_rules", Columns: []string{"file_name", "single_use_left", "token_use_count"}, Rows: [][]any{{"file", int64(1), int64(0)}}}}}
	after := Checkpoint{CreatedAt: "later", Properties: []Property{{Table: "file_access_rules", Columns: []string{"file_name", "single_use_left", "token_use_count"}, Rows: [][]any{{"file", int64(0), int64(1)}}}}}
	if preview := Compare(before, after); preview.Total != 0 {
		t.Fatalf("visitor consumption is not an editorial change: %+v", preview)
	}
	if Compare(before, before).Digest != Compare(before, after).Digest {
		t.Fatal("visitor invalidated owner confirmation")
	}
	if before.Properties[0].Rows[0][1] != int64(1) {
		t.Fatal("comparison mutated the security snapshot")
	}
}

func TestExclusiveLeaseCancellationAndGenerationFence(t *testing.T) {
	stop := make(chan struct{})
	defer close(stop)
	gate := NewGate(stop)
	editor, err := gate.Enter("example.org", 1, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	canceled := make(chan struct{})
	queued := make(chan struct{})
	admitted := make(chan error, 1)
	go func() {
		lease, enterErr := gate.Enter("example.org", 1, true, canceled, queued)
		if lease != nil {
			lease.Close(false)
		}
		admitted <- enterErr
	}()
	select {
	case <-queued:
	case <-time.After(2 * time.Second):
		t.Fatal("exclusive admission was not queued")
	}
	if lease, enterErr := gate.Enter("example.org", 1, false, nil); !errors.Is(enterErr, ErrBusy) {
		if lease != nil {
			lease.Close(false)
		}
		t.Fatalf("exclusive waiter did not stop new work: %v", enterErr)
	}
	close(canceled)
	select {
	case enterErr := <-admitted:
		if enterErr == nil {
			t.Fatal("canceled operation admitted")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("canceled waiter blocked behind an editor")
	}

	editor.Close(false)
	restoration, err := gate.Enter("example.org", 1, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	restoration.Close(true)
	if _, err = gate.Enter("example.org", 1, false, nil); !errors.Is(err, ErrStale) {
		t.Fatalf("old task admitted after restore: %v", err)
	}
	lease, err := gate.Enter("example.org", 2, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	lease.Close(false)
}
