//go:build darwin || linux || freebsd || openbsd

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
	return stat.Bavail * uint64(stat.Bsize), stat.Blocks * uint64(stat.Bsize), true
}
