package appupdate

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeBackupFixtureFile(t *testing.T, root, name string, contents []byte) {
	t.Helper()
	filename := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(filename), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, contents, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestDatabaseBackupAccountsForOnlyManagedDatabases(t *testing.T) {
	root := t.TempDir()
	databases := []string{"storage/db/sitebrush.db", "storage/db/sitebrush.db-wal", "storage/db/sitebrush.db-shm", "storage/db/sites/owner.db", "storage/db/sites/owner.db-journal", "storage/analytics/site-owner/owner.analytics.db", "storage/analytics/site-owner/owner.analytics.db-wal"}
	for _, name := range databases {
		writeBackupFixtureFile(t, root, name, []byte("original database"))
	}
	assets := []string{"storage/chroot/sitebrush.com/download/921/server-app/sitebrush_linux_amd64", "storage/chroot/sitebrush.com/uploaded.db", "storage/analytics/site-owner/archive.json.gz", "storage/db/notes.txt", "storage/db/sites/photo.jpg"}
	for _, name := range assets {
		writeBackupFixtureFile(t, root, name, bytes.Repeat([]byte("asset"), 1024))
	}
	totals, err := inspectBackupSpace(root, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if totals.TotalFiles != int64(len(databases)) || totals.TotalBytes != int64(len(databases)*len("original database")) {
		t.Fatalf("assets inflated database backup requirements: %+v", totals)
	}
	store, err := openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	backup, err := snapshot(root, store, strings.Repeat("e", 32), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := eachBackupEntry(store, backup, func(entry backupEntry) error {
		if !entry.Mode.IsRegular() || !defaultDatabaseBackupScope().contains(entry.Name) {
			return errors.New("non-database entry in snapshot")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, name := range assets {
		if _, err := store.Lstat(filepath.Join(backup, "files", filepath.FromSlash(name))); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("asset was included in snapshot", name, err)
		}
		writeBackupFixtureFile(t, root, name, []byte("changed asset"))
	}
	for _, name := range databases {
		writeBackupFixtureFile(t, root, name, []byte("migrated database"))
	}
	created := []string{"storage/db/sites/new.db", "storage/db/sites/new.db-wal", "storage/db/sitebrush.db-journal", "storage/analytics/site-new/new.analytics.db"}
	for _, name := range created {
		writeBackupFixtureFile(t, root, name, []byte("created during migration"))
	}
	for range 2 {
		if err := restore(root, store, backup); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range databases {
		contents, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil || string(contents) != "original database" {
			t.Fatal("database or journal was not restored", name, err)
		}
	}
	for _, name := range assets {
		contents, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil || string(contents) != "changed asset" {
			t.Fatal("database rollback changed an asset", name, err)
		}
	}
	for _, name := range created {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(name))); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("migration-created database or journal survived rollback", name, err)
		}
	}
	markerBytes, err := store.ReadFile(filepath.Join(backup, "complete.json"))
	if err != nil {
		t.Fatal(err)
	}
	var legacyMarker struct{ SHA256 string }
	if err := json.Unmarshal(markerBytes, &legacyMarker); err != nil || legacyMarker.SHA256 != "" {
		t.Fatal("older full-tree restorers could accept a database-only snapshot", err)
	}
}

func TestDatabaseBackupUsesConfiguredControlPath(t *testing.T) {
	root := t.TempDir()
	for _, argument := range [][]string{{"-db-path", filepath.Join(root, "custom", "control")}, {"-db-path=custom/control"}} {
		scope, err := updateDatabaseBackupScope(root, root, argument)
		if err != nil || scope.Control != "custom/control" {
			t.Fatal("configured database path was lost", scope, err)
		}
		writeBackupFixtureFile(t, root, "custom/control", []byte("control"))
		writeBackupFixtureFile(t, root, "custom/control-wal", []byte("journal"))
		writeBackupFixtureFile(t, root, "custom/sites/site.db", []byte("site"))
		writeBackupFixtureFile(t, root, "storage/db/sitebrush.db", []byte("unused control"))
		totals, err := inspectBackupSpace(root, nil, nil, scope)
		if err != nil || totals.TotalFiles != 3 || totals.TotalBytes != 18 {
			t.Fatal("wrong configured database inventory", totals, err)
		}
		store, err := openStore(root)
		if err != nil {
			t.Fatal(err)
		}
		identifier, err := Identifier()
		if err != nil {
			t.Fatal(err)
		}
		backup, err := snapshotProgress(root, store, identifier, nil, nil, totals, scope)
		if err != nil {
			t.Fatal(err)
		}
		writeBackupFixtureFile(t, root, "custom/control", []byte("changed"))
		if err := restore(root, store, backup); err != nil {
			t.Fatal(err)
		}
		contents, err := os.ReadFile(filepath.Join(root, "custom", "control"))
		if err != nil || string(contents) != "control" {
			t.Fatal("configured database was not recovered", err)
		}
		store.Close()
	}
	for _, name := range []string{"../outside", Directory + "/state.json", ".", "custom\\control"} {
		if err := (databaseBackupScope{Control: name}).validate(); err == nil {
			t.Fatal("unsafe database scope accepted", name)
		}
	}
}

func TestDatabaseSnapshotRejectsOutOfScopeInventoryAndSupportsLegacyRecovery(t *testing.T) {
	root := t.TempDir()
	writeBackupFixtureFile(t, root, "storage/db/sitebrush.db", []byte("database"))
	store, err := openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	backup, err := snapshot(root, store, "inventory-policy", nil)
	if err != nil {
		t.Fatal(err)
	}
	assetName := filepath.Join(backup, "files", "legacy.html")
	if err := store.WriteFile(assetName, []byte("legacy asset"), 0600); err != nil {
		t.Fatal(err)
	}
	assetDigest, err := fileDigest(store, assetName)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := json.Marshal(backupEntry{Name: "legacy.html", Mode: fs.FileMode(0600), SHA256: assetDigest})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.WriteFile(filepath.Join(backup, "index.jsonl"), append(entry, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	indexDigest, err := fileDigest(store, filepath.Join(backup, "index.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := atomicJSON(store, filepath.Join(backup, "complete.json"), snapshotMarker{DatabaseSHA256: indexDigest, DatabasePath: defaultDatabaseBackupScope().Control}); err != nil {
		t.Fatal(err)
	}
	if err := restore(root, store, backup); err == nil {
		t.Fatal("database recovery accepted an unrelated file")
	}
	if err := atomicJSON(store, filepath.Join(backup, "complete.json"), snapshotMarker{SHA256: indexDigest}); err != nil {
		t.Fatal(err)
	}
	if err := restore(root, store, backup); err != nil {
		t.Fatal("legacy full-tree crash recovery was broken", err)
	}
	contents, err := os.ReadFile(filepath.Join(root, "legacy.html"))
	if err != nil || string(contents) != "legacy asset" {
		t.Fatal("legacy snapshot did not recover its recorded content", err)
	}
}
