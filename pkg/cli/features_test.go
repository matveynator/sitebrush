package cli

import (
	"os"
	"runtime"
	"testing"
)

func TestBuildFeatureDefaults(t *testing.T) {
	if DesktopModeFlagSupported() {
		if InstallFlagSupported() || LinuxServerStorageDefaultEnabled() {
			t.Fatal("desktop build enabled server-only CLI defaults")
		}
		return
	}
	if !InstallFlagSupported() {
		t.Fatal("non-desktop build unexpectedly disabled the install flag")
	}
	if LinuxServerStorageDefaultEnabled() != (runtime.GOOS == "linux" && os.Geteuid() == 0) {
		t.Fatal("Linux storage default does not match runtime")
	}
}
