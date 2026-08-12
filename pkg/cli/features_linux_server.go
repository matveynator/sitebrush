//go:build linux && !desktop

package cli

import "os"

func InstallFlagSupported() bool {
	return true
}

func DesktopModeFlagSupported() bool {
	return false
}

func LinuxServerStorageDefaultEnabled() bool {
	return os.Geteuid() == 0
}
