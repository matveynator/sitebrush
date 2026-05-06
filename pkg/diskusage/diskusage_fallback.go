//go:build !darwin && !linux && !freebsd && !openbsd

package diskusage

import "os"

func fileInfoDiskBytes(fileInfo os.FileInfo) int64 {
	return fileInfo.Size()
}
