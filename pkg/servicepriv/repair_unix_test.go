//go:build !windows

package servicepriv

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestStorageRepairPrivateModesAndUnsafeLinks(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("fixture uses an unprivileged current user")
	}
	root := repairTestRoot(t)
	directory := filepath.Join(root, "storage", "files", "demo.example")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	ordinary := filepath.Join(directory, "index.html")
	executable := filepath.Join(root, "sitebrush_openbsd_amd64")
	outside := filepath.Join(t.TempDir(), "outside")
	for _, path := range []string{ordinary, executable, outside} {
		if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for path, mode := range map[string]os.FileMode{directory: 0o777, ordinary: 0o666, executable: 0o755, outside: 0o644} {
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(outside, filepath.Join(root, "symlink")); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(outside, filepath.Join(root, "hardlink")); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(filepath.Join(root, "pipe"), 0o600); err != nil {
		t.Fatal(err)
	}
	repair := storageRepair{uid: os.Getuid(), gid: os.Getgid()}
	if err := repair.run(root); err != nil {
		t.Fatal(err)
	}
	if repair.failed != 0 || repair.skipped != 3 || repair.repaired == 0 {
		t.Fatalf("unexpected repair counters: %+v", repair)
	}
	for path, wanted := range map[string]os.FileMode{root: 0o700, directory: 0o700, ordinary: 0o600, executable: 0o700, outside: 0o644} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != wanted {
			t.Errorf("%s mode=%#o, want %#o", path, info.Mode().Perm(), wanted)
		}
	}
	secondPass := storageRepair{uid: os.Getuid(), gid: os.Getgid()}
	if err := secondPass.run(root); err != nil || secondPass.repaired != 0 {
		t.Fatalf("second pass changed correct permissions: %+v err=%v", secondPass, err)
	}
}

func TestStorageRepairRejectsLinkedRootAndClosedSession(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("fixture uses an unprivileged current user")
	}
	root := repairTestRoot(t)
	linked := filepath.Join(root, "linked")
	if err := os.Symlink(t.TempDir(), linked); err != nil {
		t.Fatal(err)
	}
	repair := storageRepair{uid: os.Getuid(), gid: os.Getgid()}
	for _, path := range []string{"/", "relative", linked, filepath.Join(linked, "nested")} {
		if err := repair.run(path); err == nil {
			t.Errorf("accepted unsafe root %q", path)
		}
	}
	stop := make(chan struct{})
	close(stop)
	repair.stop = stop
	if err := repair.run(root); err == nil || repair.visited != 0 {
		t.Fatalf("closed session performed repair: %+v err=%v", repair, err)
	}
}

func TestStorageRepairChangesCopiedRootOwnership(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("changing copied root ownership requires root")
	}
	root := repairTestRoot(t)
	directory := filepath.Join(root, "demo")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "index.html")
	if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	repair := storageRepair{uid: 65534, gid: 65534}
	if err := repair.run(root); err != nil {
		t.Fatal(err)
	}
	for _, repairedPath := range []string{root, directory, path} {
		var stat unix.Stat_t
		if err := unix.Stat(repairedPath, &stat); err != nil {
			t.Fatal(err)
		}
		if stat.Uid != 65534 || stat.Gid != 65534 {
			t.Errorf("%s uid=%d gid=%d", repairedPath, stat.Uid, stat.Gid)
		}
	}
}

func repairTestRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}
