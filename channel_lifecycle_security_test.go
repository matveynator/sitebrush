package main

import (
	"testing"
	"time"

	"github.com/matveynator/sitebrush/v2/pkg/securitysync"
)

func TestSecurityBoundaryGlobalSyncStopsWhenSignalChannelCloses(t *testing.T) {
	stop := make(chan struct{})
	signals := make(chan securitysync.Signal)
	application := &App{securityGlobalSignals: signals}
	done := make(chan struct{})

	go func() {
		application.runSecurityGlobalSync(stop)
		close(done)
	}()

	close(signals)

	select {
	case <-done:
	case <-time.After(time.Second):
		close(stop)
		t.Fatal("SECURITY: closed global security signal channel did not terminate its worker")
	}
}
