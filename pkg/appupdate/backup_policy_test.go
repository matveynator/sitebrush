package appupdate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSelectedBackupAllGroupCombinations(t *testing.T) {
	for mask := 0; mask < 16; mask++ {
		t.Run(fmt.Sprint(mask), func(t *testing.T) {
			root := t.TempDir()
			policy := BackupPolicy{Databases: mask&1 != 0, Analytics: mask&2 != 0, Files: mask&4 != 0, Settings: mask&8 != 0}
			names := []string{"storage/db/sitebrush.db", "storage/db/sites/example.db", "storage/analytics/example/report.db", "storage/analytics/example/archives/2026-10-01.json.gz", "storage/files/example/photo.png", "storage/static/example/index.html", "storage/chroot/example/index.html", "storage/keys/tls/key.pem", "security/ai-capabilities.json"}
			for _, name := range names {
				writeBackupFixtureFile(t, root, name, []byte("original"))
			}
			writeBackupFixtureFile(t, root, "backup/old.zip", []byte("never recurse"))
			writeBackupFixtureFile(t, root, "storage/analytics/example/archives/.summary-live", []byte("temporary"))
			store, err := openStore(root)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			scope := backupScope{Policy: policy, Control: defaultDatabaseBackupScope()}
			inspection, err := inspectSelectedSpace(root, scope, 0, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			staged, err := prepareSelectedBackup(root, store, "selected", scope, inspection.Work, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			backup, err := staged.finish(root, store, inspection.Work, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if mask == 0 {
				if backup != "" {
					t.Fatal("none created a snapshot")
				}
				return
			}
			if err := verifySnapshot(store, backup); err != nil {
				t.Fatal(err)
			}
			if _, err := store.Stat(filepath.Join(backup, "files/storage/analytics/example/archives/.summary-live")); !os.IsNotExist(err) {
				t.Fatal("transient analytics file was backed up", err)
			}
			if err := eachBackupEntry(store, backup, func(entry backupEntry) error {
				if !scope.contains(entry.Name) {
					t.Fatalf("unselected entry %s", entry.Name)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			for _, name := range names {
				writeBackupFixtureFile(t, root, name, []byte("changed"))
			}
			for _, name := range []string{"storage/db/sites/new.db", "storage/analytics/example/new.db", "storage/files/example/new.png", "storage/keys/new.pem"} {
				writeBackupFixtureFile(t, root, name, []byte("new"))
			}
			if err := restore(root, store, backup); err != nil {
				t.Fatal(err)
			}
			if contents, err := os.ReadFile(filepath.Join(root, "storage/analytics/example/archives/.summary-live")); err != nil || string(contents) != "temporary" {
				t.Fatal("transient file changed during recovery", err)
			}
			for _, name := range names {
				contents, err := os.ReadFile(filepath.Join(root, name))
				expected := "changed"
				if scope.contains(name) {
					expected = "original"
				}
				if err != nil || string(contents) != expected {
					t.Fatalf("%s = %q, want %q: %v", name, contents, expected, err)
				}
			}
			for _, name := range []string{"storage/db/sites/new.db", "storage/analytics/example/new.db", "storage/files/example/new.png", "storage/keys/new.pem"} {
				_, err := os.Stat(filepath.Join(root, name))
				if scope.contains(name) && !os.IsNotExist(err) {
					t.Fatalf("selected new file survived: %s", name)
				}
				if !scope.contains(name) && err != nil {
					t.Fatalf("unselected file lost: %s", name)
				}
			}
		})
	}
}

func TestStagedBackupReconcilesLiveChanges(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"keep", "change", "replace", "delete"} {
		writeBackupFixtureFile(t, root, "storage/files/example/"+name, []byte(name))
	}
	store, err := openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	scope := backupScope{Policy: FullBackupPolicy(), Control: defaultDatabaseBackupScope()}
	staged, err := prepareSelectedBackup(root, store, "changes", scope, WorkProgress{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	writeBackupFixtureFile(t, root, "storage/files/example/change", []byte("updated contents"))
	writeBackupFixtureFile(t, root, "storage/files/example/new", []byte("new contents"))
	if err := os.Remove(filepath.Join(root, "storage/files/example/delete")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "storage/files/example/replace")); err != nil {
		t.Fatal(err)
	}
	writeBackupFixtureFile(t, root, "storage/files/example/replace", []byte("new inode"))
	writeBackupFixtureFile(t, root, "storage/db/sitebrush.db", []byte("final checkpoint"))
	backup, err := staged.finish(root, store, WorkProgress{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifySnapshot(store, backup); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Stat(filepath.Join(backup, "files/storage/files/example/delete")); !os.IsNotExist(err) {
		t.Fatal("deleted file in final backup", err)
	}
	for _, name := range []string{"storage/files/example/change", "storage/files/example/replace", "storage/files/example/new", "storage/db/sitebrush.db"} {
		original, _ := os.ReadFile(filepath.Join(root, name))
		copied, err := store.ReadFile(filepath.Join(backup, "files", name))
		if err != nil || string(original) != string(copied) {
			t.Fatal(name, string(copied), err)
		}
	}
}

func TestCustomDatabaseInsideFileTreeIsNeverCopiedLiveOrWhenExcluded(t *testing.T) {
	for _, policy := range []BackupPolicy{FullBackupPolicy(), {Files: true}} {
		root := t.TempDir()
		control := databaseBackupScope{Control: "storage/files/control.db"}
		for _, name := range []string{control.Control, control.Control + "-wal", "storage/files/sites/example.db", "storage/files/image.png"} {
			writeBackupFixtureFile(t, root, name, []byte("original"))
		}
		store, err := openStore(root)
		if err != nil {
			t.Fatal(err)
		}
		scope := backupScope{Policy: policy, Control: control}
		staged, err := prepareSelectedBackup(root, store, "custom", scope, WorkProgress{}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		for name := range staged.Entries {
			if control.contains(name) {
				t.Fatal("copied live database", name)
			}
		}
		backup, err := staged.finish(root, store, WorkProgress{}, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := verifySnapshot(store, backup); err != nil {
			t.Fatal(err)
		}
		seen := make(map[string]bool)
		if err := eachBackupEntry(store, backup, func(entry backupEntry) error {
			if seen[entry.Name] {
				t.Fatal("duplicate entry", entry.Name)
			}
			seen[entry.Name] = true
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if seen[control.Control] != policy.Databases {
			t.Fatal("ignored database selection", seen)
		}
		store.Close()
	}
}

func TestSelectedBackupPreservesDirectoryModesAndSafeLinks(t *testing.T) {
	root := t.TempDir()
	writeBackupFixtureFile(t, root, "storage/chroot/example/download/1/app", []byte("binary"))
	directory := filepath.Join(root, "storage/chroot/example/download")
	if err := os.Chmod(directory, 0750); err != nil {
		t.Fatal(err)
	}
	originalDirectory, err := os.Stat(directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("1", filepath.Join(directory, "latest")); err != nil {
		t.Skip(err)
	}
	store, err := openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	scope := backupScope{Policy: BackupPolicy{Files: true}, Control: defaultDatabaseBackupScope()}
	staged, err := prepareSelectedBackup(root, store, "links", scope, WorkProgress{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	backup, err := staged.finish(root, store, WorkProgress{}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifySnapshot(store, backup); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(directory, "latest")); err != nil {
		t.Fatal(err)
	}
	if err := restore(root, store, backup); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(directory)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != originalDirectory.Mode().Perm() {
		t.Fatal(info.Mode())
	}
	if target, err := os.Readlink(filepath.Join(directory, "latest")); err != nil || target != "1" {
		t.Fatal(target, err)
	}
}

func TestBackupPolicyValidation(t *testing.T) {
	for _, selection := range []string{"all", "none", "databases,analytics", "files,settings"} {
		if _, err := ParseBackupPolicy(selection); err != nil {
			t.Fatal(err)
		}
	}
	for _, selection := range []string{"", "all,files", "unknown"} {
		if _, err := ParseBackupPolicy(selection); err == nil {
			t.Fatal("accepted", selection)
		}
	}
	for _, detail := range []string{"", "null", "{}{}", `{"unknown":true}`} {
		if _, err := decodeBackupPolicy(detail); err == nil {
			t.Fatal("accepted", detail)
		}
	}
}

func TestSupervisorExplicitNoBackupAndCapabilities(t *testing.T) {
	root := newFixtureInstallation(t)
	startFixtureSupervisor(t, root, "success")
	checked := checkedFixture(t, root)
	if !checked.BackupOptions {
		t.Fatal("missing capability")
	}
	payload, _ := json.Marshal(BackupPolicy{})
	if _, err := Exchange(root, Request{Action: "upgrade_with_options", ManifestHash: checked.ManifestHash, Detail: string(payload)}, nil); err != nil {
		t.Fatal(err)
	}
	state := awaitFixture(t, root)
	if state.Stage != "completed" || state.Backup != "" || state.BackupPolicy == nil || *state.BackupPolicy != (BackupPolicy{}) {
		t.Fatal(state)
	}
}

func waitMigrationFixture(t *testing.T, root string) {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for {
		if _, err := os.Stat(filepath.Join(root, "migration-entered")); err == nil {
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("migration did not start")
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func TestCanceledMigrationRecoversWithIndependentSession(t *testing.T) {
	root := newFixtureInstallation(t)
	writeBackupFixtureFile(t, root, "wait-migration", []byte("yes"))
	startFixtureSupervisor(t, root, "success")
	checked := checkedFixture(t, root)
	fixtureExchange(t, root, "upgrade", checked.ManifestHash)
	waitMigrationFixture(t, root)
	fixtureExchange(t, root, "cancel", "")
	state := awaitFixture(t, root)
	if state.Stage != "rolled_back" || state.Current.Version != "1" || state.RecoveryRequired || strings.Contains(state.Error, "health check canceled") {
		t.Fatal(state)
	}
	if _, err := os.Stat(filepath.Join(root, "storage/db/sites/migration-result.db")); !os.IsNotExist(err) {
		t.Fatal("migration data survived recovery", err)
	}
}

func TestServiceStopPersistsRecoveryWithoutCanceledHealthAndResumes(t *testing.T) {
	root := newFixtureInstallation(t)
	writeBackupFixtureFile(t, root, "wait-migration", []byte("yes"))
	_, done, stop := startFixtureSupervisor(t, root, "success")
	checked := checkedFixture(t, root)
	fixtureExchange(t, root, "upgrade", checked.ManifestHash)
	waitMigrationFixture(t, root)
	close(stop)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("service shutdown hung")
	}
	store, err := openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	state, err := loadState(store)
	store.Close()
	if err != nil {
		t.Fatal(err)
	}
	if state.Stage != "interrupted" || !state.RecoveryRequired || !state.DataChanged || strings.Contains(state.Error, "health check canceled") || state.CleanupError != "" {
		t.Fatal(state)
	}
	if err := os.Remove(filepath.Join(root, "wait-migration")); err != nil {
		t.Fatal(err)
	}
	startFixtureSupervisor(t, root, "success")
	state = awaitFixture(t, root)
	if state.RecoveryRequired || state.Current.Version != "1" {
		t.Fatal(state)
	}
	if _, err := os.Stat(filepath.Join(root, "storage/db/sites/migration-result.db")); !os.IsNotExist(err) {
		t.Fatal("interrupted migration not restored", err)
	}
}

func TestUnbackedIncompatibleMigrationRequiresManualRecovery(t *testing.T) {
	root := newFixtureInstallation(t)
	writeBackupFixtureFile(t, root, "reject-health", []byte("yes"))
	startFixtureSupervisor(t, root, "incompatible")
	checked := checkedFixture(t, root)
	if _, err := Exchange(root, Request{Action: "upgrade_with_options", ManifestHash: checked.ManifestHash, Detail: `{"databases":false,"analytics":false,"files":false,"settings":false}`}, nil); err != nil {
		t.Fatal(err)
	}
	state := awaitFixture(t, root)
	if !state.RecoveryRequired || !strings.Contains(state.Error, "manual recovery required") || state.Backup != "" {
		t.Fatal(state)
	}
	if _, err := Exchange(root, Request{Action: "upgrade", ManifestHash: checked.ManifestHash}, nil); err == nil {
		t.Fatal("accepted operation before manual recovery")
	}
}

func TestAnalyticsGroupIncludesLiveArchivesAndPreservesTransientFiles(t *testing.T) {
	root := t.TempDir()
	archive := "storage/analytics/example/archives/2026-10-01.json.gz"
	database := "storage/analytics/example/analytics.db"
	writeBackupFixtureFile(t, root, archive, []byte("archive"))
	writeBackupFixtureFile(t, root, database, []byte("database"))
	writeBackupFixtureFile(t, root, "storage/analytics/example/archives/.summary-live", []byte("temporary"))
	scope := backupScope{Policy: BackupPolicy{Analytics: true}, Control: defaultDatabaseBackupScope()}
	inspection, err := inspectSelectedSpace(root, scope, 0, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.Work.TotalFiles != 2 || inspection.Groups[1].Bytes != int64(len("archive")+len("database")) {
		t.Fatal(inspection)
	}
	store, err := openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	staged, err := prepareSelectedBackup(root, store, "analytics", scope, inspection.Work, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := staged.Entries[archive]; !exists {
		t.Fatal("archive was not staged while the site was running")
	}
	if _, exists := staged.Entries[database]; exists {
		t.Fatal("live analytics database was staged")
	}
	writeBackupFixtureFile(t, root, archive, []byte("final archive"))
	backup, err := staged.finish(root, store, inspection.Work, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifySnapshot(store, backup); err != nil {
		t.Fatal(err)
	}
	writeBackupFixtureFile(t, root, archive, []byte("migration changed the archive"))
	writeBackupFixtureFile(t, root, "storage/analytics/example/archives/2026-10-02.json.gz", []byte("new archive"))
	writeBackupFixtureFile(t, root, "storage/analytics/new/archives/.summary-live", []byte("new temporary"))
	if err := restore(root, store, backup); err != nil {
		t.Fatal(err)
	}
	if contents, err := os.ReadFile(filepath.Join(root, archive)); err != nil || string(contents) != "final archive" {
		t.Fatal(string(contents), err)
	}
	if _, err := os.Stat(filepath.Join(root, "storage/analytics/example/archives/2026-10-02.json.gz")); !os.IsNotExist(err) {
		t.Fatal("new archive survived recovery", err)
	}
	if contents, err := os.ReadFile(filepath.Join(root, "storage/analytics/new/archives/.summary-live")); err != nil || string(contents) != "new temporary" {
		t.Fatal("unselected transient file was lost", string(contents), err)
	}
}
