//go:build linux || darwin || freebsd

package serviceinstall

import (
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
