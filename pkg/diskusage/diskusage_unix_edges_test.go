//go:build darwin || linux || freebsd

package diskusage

import (
	"io/fs"
	"testing"
	"time"
)

type fakeFileInfo struct {
	size int64
}

func (info fakeFileInfo) Name() string       { return "fake" }
func (info fakeFileInfo) Size() int64        { return info.size }
func (info fakeFileInfo) Mode() fs.FileMode  { return 0 }
func (info fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (info fakeFileInfo) IsDir() bool        { return false }
func (info fakeFileInfo) Sys() any           { return nil }

func TestDiskUsageFallbackAndMissingFilesystemBranches(t *testing.T) {
	if got := fileInfoDiskBytes(fakeFileInfo{size: 123}); got != 123 {
		t.Fatalf("fallback file size = %d, want 123", got)
	}
	if free, total, ok := DiskSpace(t.TempDir() + "/missing/path"); free != 0 || total != 0 || ok {
		t.Fatalf("missing DiskSpace = %d/%d/%v, want 0/0/false", free, total, ok)
	}
}
