package systeminit

import (
	"strings"
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
