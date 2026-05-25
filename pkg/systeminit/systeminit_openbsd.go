//go:build openbsd

package systeminit

import "syscall"

func platformInit() platformResult {
	openFileBefore, openFileTarget, openFileAfter, openFileStatus, openFilePartial := raiseResourceLimit(syscall.RLIMIT_NOFILE, desiredOpenFileLimit)
	return platformResult{
		OpenFileLimit:             formatLimit(openFileAfter),
		OpenFileLimitPartial:      openFilePartial,
		ThreadProcessLimit:        stateUnsupported,
		ThreadProcessLimitPartial: true,
		SocketOptions:             stateApplied,
		ZeroCopy:                  stateUnsupported,
		ZeroCopyPartial:           true,
		Settings: []tuningSetting{
			resourceLimitSetting("Open files", openFileBefore, openFileTarget, openFileAfter, openFileStatus, "max concurrent files"),
			unsupportedSetting("Thread/processes", "1048576", "not exposed safely"),
			socketOptionsSetting(true),
			zeroCopySetting(stateUnsupported, "safe sendfile path not enabled"),
		},
	}
}

func platformSocketControl(network string, address string, connection syscall.RawConn) error {
	return unixSocketControl(network, address, connection, true)
}

func platformReusePortSocketOption() (int, bool) {
	return 0x0200, true
}
