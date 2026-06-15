package diskusage

import (
	"io/fs"
	"os"
	"path/filepath"
)

// FileSize returns the disk space occupied by one filesystem entry.
func FileSize(filePath string) int64 {
	fileInfo, err := os.Stat(filePath)
	if err != nil {
		return 0
	}
	return fileInfoDiskBytes(fileInfo)
}

// DirectorySize returns the disk space occupied by a directory tree.
func DirectorySize(rootPath string) int64 {
	var totalBytes int64
	_ = filepath.WalkDir(rootPath, func(currentPath string, currentEntry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		fileInfo, statErr := currentEntry.Info()
		if statErr != nil {
			return nil
		}
		totalBytes += fileInfoDiskBytes(fileInfo)
		return nil
	})
	return totalBytes
}

// DiskSpace returns filesystem capacity for the path that stores SiteBrush data.
func DiskSpace(path string) (uint64, uint64, bool) {
	return diskSpace(path)
}
