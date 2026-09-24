package winservice

import (
	"context"
	"testing"
)

func TestRunIfNeededIsNoopOutsideWindows(t *testing.T) {
	called := false
	wasService, err := RunIfNeeded("sitebrush", func(context.Context) error { called = true; return nil })
	if err != nil || wasService || called {
		t.Fatalf("RunIfNeeded = %v, %v; callback=%v", wasService, err, called)
	}
}
