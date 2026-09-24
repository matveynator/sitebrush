//go:build linux

package systeminit

import (
	"errors"
	"reflect"
	"syscall"
	"testing"
)

type failingRawConn struct{}

func (failingRawConn) Control(func(uintptr)) error { return errors.New("control failed") }
func (failingRawConn) Read(func(uintptr) bool) error { return nil }
func (failingRawConn) Write(func(uintptr) bool) error { return nil }

func TestLinuxPlatformInitializationCoverage(t *testing.T) {
	result := platformInit()
	if len(result.Settings) != 4 {
		t.Fatalf("platform settings = %d, want 4", len(result.Settings))
	}
	if result.SocketOptions != stateApplied || result.ZeroCopy != stateSupported {
		t.Fatalf("platform result = %#v", result)
	}
	if option, ok := platformReusePortSocketOption(); !ok || option != linuxSOReusePort {
		t.Fatalf("reuse port option = %d, %v", option, ok)
	}

	connection := &coverageRawConn{}
	if err := platformSocketControl("tcp4", "127.0.0.1:0", connection); err != nil || !connection.called {
		t.Fatalf("platform socket control called=%v err=%v", connection.called, err)
	}
	if err := unixSocketControl("tcp", "127.0.0.1:0", failingRawConn{}, true); err != nil {
		t.Fatalf("best-effort socket control propagated error: %v", err)
	}
}

func TestUnixResourceLimitHelperBranches(t *testing.T) {
	var unsigned uint64
	setRlimitField(reflect.ValueOf(&unsigned).Elem(), 123)
	if unsigned != 123 {
		t.Fatalf("unsigned field = %d", unsigned)
	}
	var unsupported string
	setRlimitField(reflect.ValueOf(&unsupported).Elem(), 123)
	if unsupported != "" {
		t.Fatalf("unsupported field was modified: %q", unsupported)
	}
	if got := rlimitFieldUint(reflect.ValueOf("nope")); got != 0 {
		t.Fatalf("unsupported rlimit field = %d", got)
	}

	setting := resourceLimitSetting("Open files", 10, 20, 20, stateApplied, "note")
	if setting.Before != "10" || setting.Target != "20" || setting.After != "20" || setting.Status != stateApplied {
		t.Fatalf("resource setting = %#v", setting)
	}

	before, target, after, status, partial := raiseResourceLimit(syscall.RLIMIT_NOFILE, 1)
	if before == 0 || target == 0 || after == 0 || status != stateSkipped || partial {
		t.Fatalf("already-satisfied limit = %d/%d/%d %q partial=%v", before, target, after, status, partial)
	}
}
