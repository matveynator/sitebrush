//go:build openbsd

package publicsite

import (
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// Keep policy files private but readable by the unprivileged service account.
// Match the ownership of the already-provisioned policy directory, rather
// than assuming a fixed UID or changing the permissions to world-readable.
func setPolicyFileOwner(file *os.File, root *os.Root) error {
	directory, err := root.Open(filepath.Join("storage", "public-sites"))
	if err != nil {
		return err
	}
	defer directory.Close()
	info, err := directory.Stat()
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New("public policy directory ownership unavailable")
	}
	current, err := file.Stat()
	if err != nil {
		return err
	}
	owner, ok := current.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New("public policy file ownership unavailable")
	}
	if owner.Uid == stat.Uid && owner.Gid == stat.Gid {
		return nil
	}
	return file.Chown(int(stat.Uid), int(stat.Gid))
}

// RepairPolicyOwnership brings pre-migration private snapshots under the
// existing service directory owner. It must run in the privileged launcher
// before opening the public catalog; later atomic writes preserve this owner.
// Never follow symlinks or change content, mode, or protection barriers.
func RepairPolicyOwnership(root string) error {
	if os.Geteuid() != 0 {
		return nil
	}
	storage, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer storage.Close()
	directory, err := storage.Open(filepath.Join("storage", "public-sites"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer directory.Close()
	info, err := directory.Stat()
	if err != nil {
		return err
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return errors.New("public policy directory ownership unavailable")
	}
	entries, err := directory.ReadDir(-1)
	if err != nil {
		return err
	}
	changed := false
	for _, entry := range entries {
		name := entry.Name()
		if !policyMetadataName(name) {
			continue
		}
		// An attacker-controlled symlink must never be chowned by root.
		if !entry.Type().IsRegular() {
			return errors.New("unsafe public policy metadata entry")
		}
		fd, openErr := unix.Openat(int(directory.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if openErr != nil {
			return openErr
		}
		var stat unix.Stat_t
		if err := unix.Fstat(fd, &stat); err != nil {
			unix.Close(fd)
			return err
		}
		if stat.Mode&unix.S_IFMT != unix.S_IFREG {
			unix.Close(fd)
			return errors.New("unsafe public policy metadata type")
		}
		if stat.Uid != owner.Uid || stat.Gid != owner.Gid {
			if err := unix.Fchown(fd, int(owner.Uid), int(owner.Gid)); err != nil {
				unix.Close(fd)
				return err
			}
			if err := unix.Fsync(fd); err != nil {
				unix.Close(fd)
				return err
			}
			changed = true
		}
		if err := unix.Close(fd); err != nil {
			return err
		}
	}
	if changed {
		return directory.Sync()
	}
	return nil
}

func policyMetadataName(name string) bool {
	if name == ".server-policy" {
		return true
	}
	hash := strings.TrimSuffix(strings.TrimSuffix(name, ".blocked"), ".json")
	if !(strings.HasSuffix(name, ".json") || strings.HasSuffix(name, ".json.blocked")) || len(hash) != 64 {
		return false
	}
	_, err := hex.DecodeString(hash)
	return err == nil
}
