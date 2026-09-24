//go:build !windows

package systeminit

import (
	"context"
	"math"
	"reflect"
	"syscall"
	"testing"
)

type coverageRawConn struct{ called bool }

func (connection *coverageRawConn) Control(callback func(uintptr)) error {
	connection.called = true
	callback(^uintptr(0))
	return nil
}
func (*coverageRawConn) Read(func(uintptr) bool) error  { return nil }
func (*coverageRawConn) Write(func(uintptr) bool) error { return nil }

func TestSystemInitUnixHelpersAndListener(t *testing.T) {
	if got := rlimitFieldUint(reflect.Value{}); got != 0 {
		t.Fatalf("invalid limit field=%d", got)
	}
	if got := rlimitFieldUint(reflect.ValueOf(int64(-1))); got != math.MaxUint64 {
		t.Fatalf("negative limit=%d", got)
	}
	if got := rlimitFieldUint(reflect.ValueOf(uint64(12))); got != 12 {
		t.Fatalf("unsigned limit=%d", got)
	}
	var integer int64
	setRlimitField(reflect.ValueOf(&integer).Elem(), math.MaxUint64)
	if integer != math.MaxInt64 {
		t.Fatalf("signed limit overflow=%d", integer)
	}
	if formatLimit(0) != stateUnsupported || formatLimit(42) != "42" {
		t.Fatal("resource limit formatting failed")
	}
	if setting := resourceLimitSetting("unsupported", 0, 10, 0, stateUnsupported, "n/a"); setting.Before != "-" || setting.After != "-" {
		t.Fatalf("unsupported limit=%+v", setting)
	}
	if _, _, _, status, partial := raiseResourceLimit(-1, 10); status != stateUnsupported || !partial {
		t.Fatalf("invalid resource limit result=%q partial=%v", status, partial)
	}
	if _, _, _, status, partial := raiseResourceLimit(syscall.RLIMIT_NOFILE, 0); status != stateSkipped || partial {
		t.Fatalf("zero target result=%q partial=%v", status, partial)
	}

	connection := &coverageRawConn{}
	if err := Control("udp", "127.0.0.1:0", connection); err != nil || connection.called {
		t.Fatalf("non-TCP control called=%v err=%v", connection.called, err)
	}
	if err := Control("tcp", "127.0.0.1:0", connection); err != nil || !connection.called {
		t.Fatalf("TCP control called=%v err=%v", connection.called, err)
	}
	listenConfig := ListenConfig()
	if listenConfig.KeepAlive <= 0 || listenConfig.Control == nil {
		t.Fatal("listener socket tuning was not configured")
	}
	if _, err := Listen(context.Background(), "invalid-network", "127.0.0.1:0"); err == nil {
		t.Fatal("invalid network accepted by listener")
	}
}
