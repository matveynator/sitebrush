//go:build !windows

package serviceinstall

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestServiceUserNameByPlatform(t *testing.T) {
	for _, testCase := range []struct {
		goos, want string
	}{
		{"darwin", "_sitebrush"},
		{"openbsd", "_sitebrush"},
		{"freebsd", "sitebrush"},
		{"linux", "sitebrush"},
		{"netbsd", "sitebrush"},
	} {
		if got := serviceUserName(testCase.goos); got != testCase.want {
			t.Errorf("%s service user=%q, want %q", testCase.goos, got, testCase.want)
		}
	}
}

func TestServiceIdentityCommandsByPlatform(t *testing.T) {
	for _, testCase := range []struct {
		goos string
		want [][]string
	}{
		{"linux", [][]string{{"useradd", "--system", "--no-create-home", "--home-dir", "/var/empty", "--shell", "/usr/sbin/nologin", "sitebrush"}}},
		{"freebsd", [][]string{{"pw", "groupadd", "sitebrush"}, {"pw", "useradd", "sitebrush", "-g", "sitebrush", "-d", "/nonexistent", "-s", "/usr/sbin/nologin"}}},
		{"openbsd", [][]string{{"groupadd", "_sitebrush"}, {"useradd", "-g", "_sitebrush", "-d", "/nonexistent", "-s", "/sbin/nologin", "_sitebrush"}}},
		{"netbsd", [][]string{{"groupadd", "sitebrush"}, {"useradd", "-g", "sitebrush", "-d", "/nonexistent", "-s", "/sbin/nologin", "sitebrush"}}},
	} {
		t.Run(testCase.goos, func(t *testing.T) {
			commands, err := serviceIdentityCommands(testCase.goos, serviceUserName(testCase.goos))
			if err != nil || !reflect.DeepEqual(commands, testCase.want) {
				t.Fatalf("commands=%v, want %v, err=%v", commands, testCase.want, err)
			}
		})
	}
	if commands, err := serviceIdentityCommands("unsupported", "sitebrush"); err == nil || commands != nil {
		t.Fatalf("unsupported platform commands=%v err=%v", commands, err)
	}
}

func TestServiceStoragePathMustNotBeRoot(t *testing.T) {
	for _, path := range []string{"", ".", "relative/sitebrush", string(filepath.Separator)} {
		if err := checkServiceStoragePath(path); err == nil {
			t.Errorf("unsafe storage path %q was accepted", path)
		}
	}
	if err := checkServiceStoragePath(filepath.Join(string(filepath.Separator), "data", "sitebrush")); err != nil {
		t.Fatal(err)
	}
}

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

func TestServiceCanTraverseUsesApplicablePermissionClass(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "parent")
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	testCases := []struct {
		name        string
		permissions os.FileMode
		uid, gid    int
		allowed     bool
	}{
		{"owner execute", 0o700, 1001, 3000, true},
		{"group execute", 0o050, 3000, 2002, true},
		{"other execute", 0o001, 3000, 4000, true},
		{"owner denied despite group and other execute", 0o011, 1001, 2002, false},
		{"group denied despite other execute", 0o001, 3000, 2002, false},
		{"other denied", 0o770, 3000, 4000, false},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if err := os.Chmod(directory, testCase.permissions); err != nil {
				t.Fatal(err)
			}
			var stat unix.Stat_t
			if err := unix.Stat(directory, &stat); err != nil {
				t.Fatal(err)
			}
			stat.Uid = 1001
			stat.Gid = 2002
			if allowed := serviceCanTraverse(&stat, testCase.uid, testCase.gid); allowed != testCase.allowed {
				t.Fatalf("permissions=%#o uid=%d gid=%d allowed=%v, want %v", testCase.permissions, testCase.uid, testCase.gid, allowed, testCase.allowed)
			}
		})
	}
}

func TestServiceStorageWorkingDirectoryRejectsSymlink(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	storagePath := filepath.Join(root, "parent", "sitebrush")
	storageFD, err := openServiceStoragePath(storagePath, os.Getuid(), os.Getgid())
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(storageFD)
	info, err := os.Stat(storagePath)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("storage root permissions=%v err=%v", info, err)
	}
	if err := ensureServiceWorkingDirectory(storageFD, filepath.Join("app", "cache")); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(storagePath, "app", "cache")); err != nil || !info.IsDir() {
		t.Fatalf("working directory=%v err=%v", info, err)
	}
	outside := filepath.Join(root, "outside")
	if err := os.Mkdir(outside, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(storagePath, "linked")); err != nil {
		t.Fatal(err)
	}
	if linkedFD, err := ensureServiceDirectory(storageFD, "linked"); err == nil {
		unix.Close(linkedFD)
		t.Fatal("working directory followed a symbolic link")
	}
	if err := ensureServiceWorkingDirectory(storageFD, filepath.Join("linked", "data")); err == nil {
		t.Fatal("nested working directory followed a symbolic link")
	}
	if _, err := os.Stat(filepath.Join(outside, "data")); !os.IsNotExist(err) {
		t.Fatalf("working directory was created outside storage: %v", err)
	}
}

func TestRestrictServiceSecretTreeProtectsCertificates(t *testing.T) {
	root := t.TempDir()
	secretDirectory := filepath.Join(root, "storage", "letsencrypt")
	nestedDirectory := filepath.Join(secretDirectory, "nested")
	if err := os.MkdirAll(nestedDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, directory := range []string{secretDirectory, nestedDirectory} {
		if err := os.Chmod(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	certificate := filepath.Join(nestedDirectory, "cert.pem")
	unrelated := filepath.Join(root, "unrelated")
	for _, path := range []string{certificate, unrelated} {
		if err := os.WriteFile(path, []byte("certificate"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	rootFD, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(rootFD)
	if err := restrictServiceSecretTree(rootFD, []string{"storage", "keys", "tls"}); err != nil {
		t.Fatalf("missing certificate directory: %v", err)
	}
	if err := restrictServiceSecretTree(rootFD, []string{"storage", "letsencrypt"}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{secretDirectory, nestedDirectory} {
		assertServiceFileMode(t, path, 0o700)
	}
	assertServiceFileMode(t, certificate, 0o600)
	assertServiceFileMode(t, unrelated, 0o644)
	if err := os.Symlink(unrelated, filepath.Join(secretDirectory, "linked.pem")); err != nil {
		t.Fatal(err)
	}
	if err := restrictServiceSecretTree(rootFD, []string{"storage", "letsencrypt"}); err == nil {
		t.Fatal("certificate tree accepted a symbolic link")
	}
	assertServiceFileMode(t, unrelated, 0o644)
}

func TestChownServiceStorageTreeRejectsHardLinks(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "site.db")
	if err := os.WriteFile(file, []byte("site"), 0o600); err != nil {
		t.Fatal(err)
	}
	checkTree := func() error {
		rootFD, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
		if err != nil {
			return err
		}
		defer unix.Close(rootFD)
		var rootStat unix.Stat_t
		if err := unix.Fstat(rootFD, &rootStat); err != nil {
			return err
		}
		return chownServiceStorageTree(rootFD, root, &rootStat, os.Getuid(), os.Getgid())
	}
	if err := checkTree(); err != nil {
		t.Fatalf("ordinary storage tree: %v", err)
	}
	outside := filepath.Join(t.TempDir(), "outside.db")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(outside, filepath.Join(root, "linked.db")); err != nil {
		t.Fatal(err)
	}
	if err := checkTree(); err == nil || !strings.Contains(err.Error(), "single-link regular file") {
		t.Fatalf("storage hard link was not rejected: %v", err)
	}
	if err := os.Remove(filepath.Join(root, "linked.db")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked.db")); err != nil {
		t.Fatal(err)
	}
	if err := checkTree(); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("storage symbolic link was not rejected: %v", err)
	}
}

func assertServiceFileMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s permissions=%#o, want %#o", path, got, want)
	}
}
