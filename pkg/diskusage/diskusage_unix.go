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
