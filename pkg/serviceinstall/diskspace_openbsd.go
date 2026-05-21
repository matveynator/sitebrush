//go:build openbsd

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
			freeBlocks := stat.F_bavail
			if freeBlocks < 0 {
				freeBlocks = 0
			}
			freeBytes := uint64(freeBlocks) * uint64(stat.F_bsize)
			totalBytes := stat.F_blocks * uint64(stat.F_bsize)
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
