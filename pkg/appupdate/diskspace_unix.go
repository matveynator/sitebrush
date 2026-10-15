//go:build linux || darwin || freebsd

package appupdate

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func diskSpace(path string) (uint64, uint64, bool) {
	probePath := filepath.Clean(path)
	for {
		var stat unix.Statfs_t
		if err := unix.Statfs(probePath, &stat); err == nil {
			freeBytes := uint64(stat.Bavail) * uint64(stat.Bsize)
			totalBytes := uint64(stat.Blocks) * uint64(stat.Bsize)
			return freeBytes, totalBytes, totalBytes > 0
		}
		parent := filepath.Dir(probePath)
		if parent == probePath {
			return 0, 0, false
		}
		if _, err := os.Stat(parent); err == nil {
			probePath = parent
			continue
		}
		probePath = parent
	}
}

func availableSpace(path string) (uint64, error) {
	freeBytes, _, ok := diskSpace(path)
	if !ok {
		return 0, fmt.Errorf("cannot determine free disk space")
	}
	return freeBytes, nil
}
