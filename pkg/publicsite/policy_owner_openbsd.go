//go:build openbsd

package publicsite

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
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
