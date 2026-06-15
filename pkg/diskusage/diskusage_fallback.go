//go:build !darwin && !linux && !freebsd && !openbsd

package diskusage

import "os"

func fileInfoDiskBytes(fileInfo os.FileInfo) int64 {
	return fileInfo.Size()
}

func diskSpace(path string) (uint64, uint64, bool) {
	return 0, 0, false
}
