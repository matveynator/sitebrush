//go:build windows

package serviceinstall

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

func diskSpace(path string) (uint64, uint64, bool) {
	probePath := filepath.Clean(path)
	for {
		pathPointer, err := windows.UTF16PtrFromString(probePath)
		if err == nil {
			var freeBytes uint64
			var totalBytes uint64
			var totalFreeBytes uint64
			if err := windows.GetDiskFreeSpaceEx(pathPointer, &freeBytes, &totalBytes, &totalFreeBytes); err == nil {
				return freeBytes, totalBytes, totalBytes > 0
			}
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
