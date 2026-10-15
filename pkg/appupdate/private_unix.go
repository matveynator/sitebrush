//go:build !windows

package appupdate

import (
	"errors"
	"os"
	"syscall"
)

func secureStore(root *os.Root) error {
	info, err := root.Stat(".")
	if err != nil {
		return err
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if !ok || owner.Uid != uint32(os.Geteuid()) || info.Mode().Perm()&0o077 != 0 {
		return errors.New("update directory must be private and owned by the installation process user")
	}
	return nil
}
