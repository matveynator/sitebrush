//go:build linux

package systeminit

import "syscall"

const linuxRLimitNProc = 6
const linuxSOReusePort = 15

func platformInit() platformResult {
	openFileBefore, openFileTarget, openFileAfter, openFileStatus, openFilePartial := raiseResourceLimit(syscall.RLIMIT_NOFILE, desiredOpenFileLimit)
	threadProcessBefore, threadProcessTarget, threadProcessAfter, threadProcessStatus, threadProcessPartial := raiseResourceLimit(linuxRLimitNProc, desiredThreadProcessLimit)
	return platformResult{
		OpenFileLimit:             formatLimit(openFileAfter),
		OpenFileLimitPartial:      openFilePartial,
		ThreadProcessLimit:        threadProcessStatus,
		ThreadProcessLimitPartial: threadProcessPartial,
		SocketOptions:             stateApplied,
		ZeroCopy:                  stateSupported,
		Settings: []tuningSetting{
			resourceLimitSetting("Open files", openFileBefore, openFileTarget, openFileAfter, openFileStatus, "max concurrent files"),
			resourceLimitSetting("Thread/processes", threadProcessBefore, threadProcessTarget, threadProcessAfter, threadProcessStatus, "worker thread headroom"),
			socketOptionsSetting(true),
			zeroCopySetting(stateSupported, "sendfile available"),
		},
	}
}

func platformSocketControl(network string, address string, connection syscall.RawConn) error {
	return unixSocketControl(network, address, connection, true)
}

func platformReusePortSocketOption() (int, bool) {
	return linuxSOReusePort, true
}
