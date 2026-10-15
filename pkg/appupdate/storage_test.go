package appupdate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPruneArtifactsRetainsRecoveryAndVersionReferences(t *testing.T) {
	root := t.TempDir()
	store, err := openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	identifiers := []string{strings.Repeat("a", 32), strings.Repeat("b", 32), strings.Repeat("c", 32), strings.Repeat("d", 32), strings.Repeat("e", 32)}
	for _, id := range identifiers {
		for _, name := range []string{filepath.Join(id, "backup", "files", "content"), filepath.Join("versions", id, "server")} {
			if err := store.MkdirAll(filepath.Dir(name), 0700); err != nil {
				t.Fatal(err)
			}
			if err := store.WriteFile(name, []byte("preserve referenced content"), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	// More than one directory-read batch must be removed, including orphaned
	// partial operations left before their snapshot could be journaled.
	for index := 1; index <= 130; index++ {
		if err := store.MkdirAll(fmt.Sprintf("%032x/partial", index), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.MkdirAll("operator-notes", 0700); err != nil {
		t.Fatal(err)
	}
	state := State{Busy: true, Current: Version{Binary: filepath.Join("versions", identifiers[0], "server")}, Previous: &Version{Binary: filepath.Join("versions", identifiers[1], "server")}, Candidate: &Version{Binary: filepath.Join("versions", identifiers[2], "server")}, Backup: filepath.Join(identifiers[3], "backup"), RetainedBackup: filepath.Join(identifiers[4], "backup")}
	for range 2 {
		if err := pruneArtifacts(store, state, nil); err != nil {
			t.Fatal(err)
		}
	}
	for index, id := range identifiers {
		_, versionErr := store.Stat(filepath.Join("versions", id))
		_, backupErr := store.Stat(id)
		if index < 3 && versionErr != nil {
			t.Fatal("referenced binary lost", id, versionErr)
		}
		if index >= 3 && !errors.Is(versionErr, os.ErrNotExist) {
			t.Fatal("superseded binary retained", id, versionErr)
		}
		if index >= 3 && backupErr != nil {
			t.Fatal("recovery snapshot lost", id, backupErr)
		}
		if index < 3 && !errors.Is(backupErr, os.ErrNotExist) {
			t.Fatal("superseded snapshot retained", id, backupErr)
		}
	}
	for index := 1; index <= 130; index++ {
		if _, err := store.Stat(fmt.Sprintf("%032x", index)); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("partial operation retained", index, err)
		}
	}
	if _, err := store.Stat("operator-notes"); err != nil {
		t.Fatal("unmanaged directory removed", err)
	}
	if _, err := store.Stat("identity.json"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
}

func TestPruneArtifactsRejectsUnsafeReferencesAndCancellation(t *testing.T) {
	root := t.TempDir()
	store, err := openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	id := strings.Repeat("a", 32)
	if err := store.MkdirAll(id, 0700); err != nil {
		t.Fatal(err)
	}
	valid := State{Busy: true, Current: Version{Binary: filepath.Join("versions", strings.Repeat("b", 32), "server")}}
	scenarios := []State{valid, valid, valid, valid}
	scenarios[0].Busy = false
	scenarios[1].Current.Binary = filepath.Join("..", "outside")
	scenarios[2].Backup = filepath.Join("..", "backup")
	scenarios[3].Candidate = &Version{Binary: "versions"}
	for _, state := range scenarios {
		if err := pruneArtifacts(store, state, nil); err == nil {
			t.Fatal("unsafe pruning accepted", state)
		}
		if _, err := store.Stat(id); err != nil {
			t.Fatal("validation deleted an artifact", err)
		}
	}
	canceled := make(chan struct{})
	close(canceled)
	if err := pruneArtifacts(store, valid, canceled); err == nil {
		t.Fatal("cleanup ignored cancellation")
	}
	if _, err := store.Stat(id); err != nil {
		t.Fatal("canceled cleanup removed an artifact", err)
	}
}

func TestPruneArtifactsDoesNotFollowLinksAndProtectsLauncher(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks require Windows privileges")
	}
	root := t.TempDir()
	outside := t.TempDir()
	store, err := openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := os.WriteFile(filepath.Join(outside, "precious"), []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("a", 32)
	if err := os.Symlink(outside, filepath.Join(root, Directory, id)); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join("versions", strings.Repeat("b", 32), "server")
	if err := store.MkdirAll(filepath.Dir(binary), 0700); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteFile(binary, []byte("launcher"), 0700); err != nil {
		t.Fatal(err)
	}
	state := State{Busy: true, Current: Version{Binary: filepath.Join("versions", strings.Repeat("c", 32), "server")}, Launcher: filepath.Join(root, Directory, binary)}
	if err := pruneArtifacts(store, state, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(outside, "precious")); err != nil {
		t.Fatal("cleanup followed link outside store", err)
	}
	if _, err := store.Stat(binary); err != nil {
		t.Fatal("active launcher removed", err)
	}
	if _, err := store.Lstat(id); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("obsolete link retained", err)
	}
}

func TestSettlementRequiresDurableJournalBeforeDeleting(t *testing.T) {
	root := t.TempDir()
	store, err := openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	obsolete := strings.Repeat("a", 32)
	if err := store.MkdirAll(obsolete, 0700); err != nil {
		t.Fatal(err)
	}
	progress := make(chan transition)
	done := make(chan State, 1)
	state := State{Stage: "completed", Current: Version{Binary: filepath.Join("versions", strings.Repeat("b", 32), "server")}}
	go func() { done <- settleOperation(store, state, progress, nil) }()
	step := <-progress
	if !step.state.Busy {
		t.Fatal("new operations could race cleanup")
	}
	if _, err := store.Stat(obsolete); err != nil {
		t.Fatal("cleanup ran before journal acknowledgment", err)
	}
	step.reply <- errors.New("journal could not be persisted")
	result := <-done
	if result.CleanupError == "" || result.Busy {
		t.Fatal("journal failure left ambiguous cleanup", result)
	}
	if _, err := store.Stat(obsolete); err != nil {
		t.Fatal("failed journal caused deletion", err)
	}
}

func TestInstalledVersionBelongsToTheActualLauncher(t *testing.T) {
	root := t.TempDir()
	launcher := filepath.Join(root, "launcher")
	if err := os.WriteFile(launcher, []byte("working launcher"), 0700); err != nil {
		t.Fatal(err)
	}
	if got := InstalledVersion(root, launcher); got != "" {
		t.Fatal("version invented before installation", got)
	}
	store, err := openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	state, err := bootstrap(store, Options{Executable: launcher, Version: "21", Compatibility: CurrentCompatibility()})
	if err != nil {
		t.Fatal(err)
	}
	if got := InstalledVersion(root, launcher); got != "21" {
		t.Fatal("installed identity missing", got)
	}
	stranger := filepath.Join(root, "different-launcher")
	if err := os.WriteFile(stranger, []byte("other executable"), 0700); err != nil {
		t.Fatal(err)
	}
	if got := InstalledVersion(root, stranger); got != "" {
		t.Fatal("unrelated binary claimed installation", got)
	}
	if got := InstalledVersion(root, filepath.Join(root, "missing")); got != "" {
		t.Fatal("missing binary claimed installation", got)
	}
	state.Launcher = ""
	if err := atomicJSON(store, "state.json", state); err != nil {
		t.Fatal(err)
	}
	if got := InstalledVersion(root, launcher); got != "" {
		t.Fatal("missing launcher accepted", got)
	}
	if err := store.WriteFile("state.json", []byte("broken journal"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := InstalledVersion(root, launcher); got != "" {
		t.Fatal("corrupt journal supplied version", got)
	}
}

func TestSnapshotRestoresInternalReleasePublicationLinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks require Windows privileges")
	}
	root := t.TempDir()
	directory := filepath.Join(root, "download", "42")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "release"), []byte("verified release"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "download", "latest")
	if err := os.Symlink("42", link); err != nil {
		t.Fatal(err)
	}
	store, err := openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	backup, err := snapshot(root, store, strings.Repeat("a", 32), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(link, []byte("wrong index"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := restore(root, store, backup); err != nil {
		t.Fatal(err)
	}
	if target, err := os.Readlink(link); err != nil || target != "42" {
		t.Fatal("release index link was not restored", target, err)
	}
	if contents, err := os.ReadFile(filepath.Join(link, "release")); err != nil || string(contents) != "verified release" {
		t.Fatal("release link lost its target", err)
	}
	if err := restore(root, store, backup); err != nil {
		t.Fatal("repeated link recovery failed", err)
	}
	backedUpLink := filepath.Join(backup, "files", "download", "latest")
	if err := store.Remove(backedUpLink); err != nil {
		t.Fatal(err)
	}
	if err := store.Symlink("../..", backedUpLink); err != nil {
		t.Fatal(err)
	}
	if err := restore(root, store, backup); err == nil {
		t.Fatal("modified backup link accepted")
	}
}

func TestSnapshotRejectsEscapingPrivateAndDanglingLinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks require Windows privileges")
	}
	for _, target := range []string{"../outside", Directory + "/state.json", "missing"} {
		t.Run(target, func(t *testing.T) {
			root := t.TempDir()
			store, err := openStore(root)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if err := os.Symlink(target, filepath.Join(root, "link")); err != nil {
				t.Fatal(err)
			}
			if _, err := snapshot(root, store, strings.Repeat("b", 32), nil); err == nil {
				t.Fatal("unsafe link accepted", target)
			}
		})
	}
}
