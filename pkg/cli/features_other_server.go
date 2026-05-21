//go:build !desktop && !linux

package cli

func InstallFlagSupported() bool {
	return true
}

func DesktopModeFlagSupported() bool {
	return false
}

func LinuxServerStorageDefaultEnabled() bool {
	return false
}
