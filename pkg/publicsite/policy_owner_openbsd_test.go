//go:build openbsd

package publicsite

import (
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
)

// Regression: a privileged publication writer must never leave 0600 policy
// snapshots owned by root inside a directory owned by the service account.
func TestWritePolicyPreservesOpenBSDDirectoryOwner(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "storage", "public-sites")
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if os.Geteuid() == 0 {
		account, err := user.Lookup("_sitebrush")
		if err == nil {
			uid, uidErr := strconv.Atoi(account.Uid)
			gid, gidErr := strconv.Atoi(account.Gid)
			if uidErr != nil || gidErr != nil {
				t.Fatal("invalid service identity")
			}
			if err := os.Chown(directory, uid, gid); err != nil {
				t.Fatal(err)
			}
		}
	}
	policy := Policy{Domain: "example.test"}
	if err := Write(root, policy); err != nil {
		t.Fatal(err)
	}
	directoryInfo, err := os.Stat(directory)
	if err != nil {
		t.Fatal(err)
	}
	policyInfo, err := os.Stat(PolicyPath(root, policy.Domain))
	if err != nil {
		t.Fatal(err)
	}
	directoryOwner := directoryInfo.Sys().(*syscall.Stat_t)
	policyOwner := policyInfo.Sys().(*syscall.Stat_t)
	if directoryOwner.Uid != policyOwner.Uid || directoryOwner.Gid != policyOwner.Gid {
		t.Fatalf("policy owner %d:%d differs from directory %d:%d",
			policyOwner.Uid, policyOwner.Gid, directoryOwner.Uid, directoryOwner.Gid)
	}
	if policyInfo.Mode().Perm() != 0600 {
		t.Fatalf("policy mode = %04o, want 0600", policyInfo.Mode().Perm())
	}
}

func TestRepairLegacyOpenBSDPolicyOwnership(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root to simulate migrated root-owned snapshots")
	}
	account, err := user.Lookup("_sitebrush")
	if err != nil {
		t.Skip("service account is not installed")
	}
	uid, err := strconv.Atoi(account.Uid)
	if err != nil {
		t.Fatal(err)
	}
	gid, err := strconv.Atoi(account.Gid)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	dir := filepath.Join(root, "storage", "public-sites")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(dir, uid, gid); err != nil {
		t.Fatal(err)
	}
	name := PolicyPath(root, "example.test")
	if err := os.WriteFile(name, []byte(`{"Domain":"example.test"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := RepairPolicyOwnership(root); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(name)
	if err != nil {
		t.Fatal(err)
	}
	stat := info.Sys().(*syscall.Stat_t)
	if int(stat.Uid) != uid || int(stat.Gid) != gid || info.Mode().Perm() != 0600 {
		t.Fatalf("repaired owner=%d:%d mode=%04o, want %d:%d mode=0600", stat.Uid, stat.Gid, info.Mode().Perm(), uid, gid)
	}
	if err := RepairPolicyOwnership(root); err != nil {
		t.Fatalf("ownership migration must be repeatable: %v", err)
	}
}

func TestRepairRejectsPolicySymlinks(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires privileged launch mode")
	}
	root := t.TempDir()
	dir := filepath.Join(root, "storage", "public-sites")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(root, "victim")
	if err := os.WriteFile(victim, []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, PolicyPath(root, "example.test")); err != nil {
		t.Fatal(err)
	}
	if err := RepairPolicyOwnership(root); err == nil {
		t.Fatal("policy ownership repair must fail closed on symlinks")
	}
	payload, err := os.ReadFile(victim)
	if err != nil || string(payload) != "untouched" {
		t.Fatalf("symlink target changed: %q, %v", payload, err)
	}
}
