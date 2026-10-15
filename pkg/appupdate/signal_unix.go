//go:build !windows

package appupdate

import (
	"os"
	"os/exec"
	"syscall"
)

func signalChild(child *os.Process) error { return child.Signal(syscall.SIGTERM) }

func detachCommand(command *exec.Cmd) { command.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
