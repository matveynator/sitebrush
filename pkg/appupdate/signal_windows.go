//go:build windows

package appupdate

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// Windows shutdown is requested over the private channel before this fallback.
func signalChild(child *os.Process) error { return errors.New("Windows requires channel shutdown") }

func detachCommand(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x00000008 | 0x00000200}
}
