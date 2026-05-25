//go:build !darwin && !linux && !freebsd && !openbsd && !netbsd

package shutdownsignals

import (
	"os"
	"syscall"
)

func ServerShutdownSignals() []os.Signal {
	return []os.Signal{os.Interrupt, syscall.SIGTERM}
}
