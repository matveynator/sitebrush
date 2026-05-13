//go:build !desktop && !linux

package cli

func SetupWizardFlagSupported() bool {
	return false
}

func DesktopModeFlagSupported() bool {
	return false
}

func LinuxServerStorageDefaultEnabled() bool {
	return false
}
