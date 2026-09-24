package systeminit

import (
	"context"
	"math"
	"reflect"
	"strings"
	"syscall"
	"testing"
)

func TestStartupStatusReportsPartialForUnsupportedFeature(t *testing.T) {
	status := startupStatus(platformResult{
		OpenFileLimit:        "1024",
		ThreadProcessLimit:   stateSkipped,
		SocketOptions:        stateApplied,
		ZeroCopy:             stateUnsupported,
		ZeroCopyPartial:      true,
		OpenFileLimitPartial: false,
	})
	if status != startupStatusPartial {
		t.Fatalf("status = %q, want %q", status, startupStatusPartial)
	}
}

func TestFormatStartupReportUsesRequiredEnglishLabels(t *testing.T) {
	report := startupReport{
		OS:                 "linux",
		Architecture:       "amd64",
		CPUCores:           16,
		GOMAXPROCS:         16,
		OpenFileLimit:      "1048576",
		ThreadProcessLimit: stateApplied,
		SocketOptions:      stateApplied,
		ZeroCopy:           stateSupported,
		Status:             startupStatusOK,
		Settings: []tuningSetting{
			{Name: "Go scheduler", Before: "8", Target: "16", After: "16", Status: stateApplied, Notes: "GOMAXPROCS matches CPU cores"},
			{Name: "Open files", Before: "256", Target: "1048576", After: "1048576", Status: stateApplied, Notes: "max concurrent files"},
			{Name: "Socket options", Before: "OS default", Target: "low-latency TCP", After: "reuseaddr, reuseport, keepalive, nodelay", Status: stateApplied, Notes: "applied before bind"},
			{Name: "Zero-copy transfer", Before: "runtime check", Target: "sendfile", After: "available", Status: stateSupported, Notes: "sendfile available"},
		},
		AppliedCount:   3,
		SupportedCount: 1,
	}
	body := formatStartupReport(report, false)
	for _, expected := range []string{
		"System initialization",
		"Static file server startup tuning",
		"OS: linux  Architecture: amd64  CPU cores: 16",
		"GOMAXPROCS: 16",
		"Summary: 3 applied, 0 skipped, 1 supported, 0 unsupported",
		"| Setting",
		"| Open files",
		"| 256",
		"| 1048576",
		"| reuseaddr, reuseport, keepalive, nodelay",
		"| Zero-copy transfer",
		"| supported",
		"Status: OK",
	} {
		if !strings.Contains(body, expected) {
			t.Fatalf("report missing %q in %q", expected, body)
		}
	}
}

func TestStartupStatusCountsAndSettingFormatting(t *testing.T) {
	settings := []tuningSetting{
		{Status: stateApplied},
		{Status: stateSkipped},
		{Status: stateUnsupported},
		{Status: stateSupported},
		{Status: "unknown"},
	}
	if applied, skipped, unsupported, supported := tuningSettingCounts(settings); applied != 1 || skipped != 1 || unsupported != 1 || supported != 1 {
		t.Fatalf("setting counts = %d %d %d %d", applied, skipped, unsupported, supported)
	}
	if schedulerStatus(2, 4, 4) != stateApplied || schedulerStatus(4, 4, 4) != stateSkipped || schedulerStatus(2, 3, 4) != stateSkipped {
		t.Fatal("scheduler status classification failed")
	}
	if startupStatus(platformResult{}) != startupStatusOK || startupStatus(platformResult{SocketOptionsPartial: true}) != startupStatusPartial || startupStatus(platformResult{CriticalErr: context.DeadlineExceeded}) != startupStatusFailed {
		t.Fatal("platform startup status classification failed")
	}
	if !strings.Contains(colorStatus("applied", stateApplied, true), "\033[1;32m") || colorStatus("other", "unknown", true) != "other" || colorStatus("applied", stateApplied, false) != "applied" {
		t.Fatal("status color formatting failed")
	}
	if padRight("long name", 5) != "long." || padRight("x", 1) != "x"[:1] || padRight("x", 3) != "x  " {
		t.Fatal("table cell padding failed")
	}
	if got := unsupportedSetting("Feature", "target", "reason"); got.Status != stateUnsupported || got.Before != "-" || got.After != "-" {
		t.Fatalf("unsupported setting = %#v", got)
	}
	if socketOptionsSetting(false).After != "reuseaddr, keepalive, nodelay" || !strings.Contains(socketOptionsSetting(true).After, "reuseport") {
		t.Fatal("socket option report is incorrect")
	}
	if zeroCopySetting(stateSupported, "ready").After != "available" || zeroCopySetting(stateUnsupported, "missing").After != "not available" {
		t.Fatal("zero-copy report is incorrect")
	}
}

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
