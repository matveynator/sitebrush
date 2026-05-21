//go:build desktop

package cli

func InstallFlagSupported() bool {
	return false
}

func DesktopModeFlagSupported() bool {
	return true
}

func LinuxServerStorageDefaultEnabled() bool {
	return false
}
