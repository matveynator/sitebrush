package shutdownsignals

import (
	"os"
	"syscall"
	"testing"
)

func TestServerShutdownSignalsIncludesInterruptAndTermination(t *testing.T) {
	signals := ServerShutdownSignals()
	if len(signals) < 2 || signals[0] != os.Interrupt || signals[1] != syscall.SIGTERM {
		t.Fatalf("shutdown signals = %v", signals)
	}
}
