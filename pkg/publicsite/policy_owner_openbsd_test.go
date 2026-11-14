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
