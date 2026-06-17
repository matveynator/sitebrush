//go:build openbsd

package diskusage

import (
	"os"
	"syscall"
)

func fileInfoDiskBytes(fileInfo os.FileInfo) int64 {
	stat, ok := fileInfo.Sys().(*syscall.Stat_t)
	if !ok || stat.Blocks <= 0 {
		return fileInfo.Size()
	}
	return stat.Blocks * 512
}

func diskSpace(path string) (uint64, uint64, bool) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, 0, false
	}
	blockSize := uint64(stat.F_bsize)
	return uint64(stat.F_bavail) * blockSize, stat.F_blocks * blockSize, true
}
