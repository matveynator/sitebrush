//go:build !linux && !freebsd && !openbsd && !netbsd && !darwin && !windows

package systeminit

import "syscall"

func platformInit() platformResult {
	return platformResult{
		OpenFileLimit:             stateUnsupported,
		OpenFileLimitPartial:      true,
		ThreadProcessLimit:        stateUnsupported,
		ThreadProcessLimitPartial: true,
		SocketOptions:             stateUnsupported,
		SocketOptionsPartial:      true,
		ZeroCopy:                  stateUnsupported,
		ZeroCopyPartial:           true,
		Settings: []tuningSetting{
			unsupportedSetting("Open files", "1048576", "platform not supported"),
			unsupportedSetting("Thread/processes", "1048576", "platform not supported"),
			unsupportedSetting("Socket options", "low-latency TCP", "platform not supported"),
			zeroCopySetting(stateUnsupported, "platform not supported"),
		},
	}
}

func platformSocketControl(network string, address string, connection syscall.RawConn) error {
	_ = network
	_ = address
	_ = connection
	return nil
}

func platformReusePortSocketOption() (int, bool) {
	return 0, false
}
