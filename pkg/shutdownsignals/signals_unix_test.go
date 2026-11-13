//go:build darwin || linux || freebsd || openbsd || netbsd

package shutdownsignals

import (
	"syscall"
	"testing"
)

func TestUnixShutdownSignalsIncludeHangupAndQuit(t *testing.T) {
	signals := ServerShutdownSignals()
	for _, expected := range []syscall.Signal{syscall.SIGHUP, syscall.SIGQUIT} {
		found := false
		for _, signal := range signals {
			if signal == expected {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("shutdown signals %v do not include %v", signals, expected)
		}
	}
}
