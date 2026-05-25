//go:build windows

package systeminit

import (
	"os"
	"strings"
	"syscall"
	"unsafe"
)

const (
	windowsStdErrorHandle                uintptr = ^uintptr(11)
	windowsEnableVirtualTerminalSequence uint32  = 0x0004
)

var (
	kernel32           = syscall.NewLazyDLL("kernel32.dll")
	procGetConsoleMode = kernel32.NewProc("GetConsoleMode")
	procSetConsoleMode = kernel32.NewProc("SetConsoleMode")
	procGetStdHandle   = kernel32.NewProc("GetStdHandle")
)

func platformInit() platformResult {
	return platformResult{
		OpenFileLimit:             stateUnsupported,
		OpenFileLimitPartial:      true,
		ThreadProcessLimit:        stateUnsupported,
		ThreadProcessLimitPartial: true,
		SocketOptions:             stateApplied,
		ZeroCopy:                  stateSupported,
		Settings: []tuningSetting{
			unsupportedSetting("Open files", "OS managed", "not a POSIX fd limit"),
			unsupportedSetting("Thread/processes", "OS managed", "not exposed safely"),
			socketOptionsSetting(false),
			zeroCopySetting(stateSupported, "TransmitFile available"),
		},
	}
}

func colorsEnabled() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	handle, _, _ := procGetStdHandle.Call(windowsStdErrorHandle)
	if handle == 0 || handle == uintptr(syscall.InvalidHandle) {
		return false
	}
	var mode uint32
	getModeResult, _, _ := procGetConsoleMode.Call(handle, uintptr(unsafe.Pointer(&mode)))
	if getModeResult == 0 {
		return false
	}
	if mode&windowsEnableVirtualTerminalSequence != 0 {
		return true
	}
	nextMode := mode | windowsEnableVirtualTerminalSequence
	setModeResult, _, _ := procSetConsoleMode.Call(handle, uintptr(nextMode))
	return setModeResult != 0
}

func platformSocketControl(network string, address string, connection syscall.RawConn) error {
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(network)), "tcp") {
		return nil
	}
	_ = address
	_ = connection.Control(func(fileDescriptor uintptr) {
		socketHandle := syscall.Handle(fileDescriptor)
		_ = syscall.SetsockoptInt(socketHandle, syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
		_ = syscall.SetsockoptInt(socketHandle, syscall.SOL_SOCKET, syscall.SO_KEEPALIVE, 1)
		_ = syscall.SetsockoptInt(socketHandle, syscall.IPPROTO_TCP, syscall.TCP_NODELAY, 1)
	})
	return nil
}
