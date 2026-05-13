//go:build linux && !desktop

package cli

func SetupWizardFlagSupported() bool {
	return true
}

func DesktopModeFlagSupported() bool {
	return false
}

func LinuxServerStorageDefaultEnabled() bool {
	return true
}
