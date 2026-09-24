//go:build darwin || linux || freebsd || openbsd

package diskusage

import "testing"

func TestDiskSpaceForExistingPath(t *testing.T) {
	free, total, ok := DiskSpace(t.TempDir())
	if !ok || total == 0 || free > total {
		t.Fatalf("DiskSpace() = %d/%d, %t", free, total, ok)
	}
}
