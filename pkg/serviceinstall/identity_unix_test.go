//go:build !windows

package serviceinstall

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestOpenServiceStoragePathCreatesDeepPathWithoutFollowingLinks(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	deep := filepath.Join(base, "one", "two", "sitebrush")
	fd, err := openServiceStoragePath(deep, os.Getuid(), os.Getgid())
	if err != nil {
		t.Fatal(err)
	}
	unix.Close(fd)
	for _, path := range []string{filepath.Join(base, "one"), filepath.Join(base, "one", "two")} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0o755 {
			t.Fatalf("storage parent %s: info=%v err=%v", path, info, err)
		}
	}
	info, err := os.Stat(deep)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("storage root: info=%v err=%v", info, err)
	}
	if err := os.Symlink(filepath.Join(base, "one"), filepath.Join(base, "link")); err != nil {
		t.Fatal(err)
	}
	fd, err = openServiceStoragePath(filepath.Join(base, "link", "elsewhere"), os.Getuid(), os.Getgid())
	if err == nil {
		unix.Close(fd)
		t.Fatal("storage path followed a symbolic link")
	}
}
