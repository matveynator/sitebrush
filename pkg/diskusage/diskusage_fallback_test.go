//go:build !darwin && !linux && !freebsd && !openbsd

package diskusage

import (
	"testing"
)

func TestDiskSpaceFallbackReportsUnsupported(t *testing.T) {
	if free, total, ok := DiskSpace(t.TempDir()); free != 0 || total != 0 || ok {
		t.Fatalf("fallback DiskSpace() = %d/%d, %t; want 0/0/false", free, total, ok)
	}
}
