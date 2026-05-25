package systeminit

import (
	"context"
	"fmt"
	"log"
	"net"
	"runtime"
	"strings"
	"syscall"
	"time"
)

const (
	stateApplied     = "applied"
	stateSkipped     = "skipped"
	stateUnsupported = "unsupported"
	stateSupported   = "supported"

	startupStatusOK      = "OK"
	startupStatusPartial = "PARTIAL"
	startupStatusFailed  = "FAILED"
)

type platformResult struct {
	OpenFileLimit             string
	OpenFileLimitPartial      bool
	ThreadProcessLimit        string
	ThreadProcessLimitPartial bool
	SocketOptions             string
	SocketOptionsPartial      bool
	ZeroCopy                  string
	ZeroCopyPartial           bool
	Settings                  []tuningSetting
	CriticalErr               error
}

type startupReport struct {
	OS                 string
	Architecture       string
	CPUCores           int
	GOMAXPROCS         int
	OpenFileLimit      string
	ThreadProcessLimit string
	SocketOptions      string
	ZeroCopy           string
	Status             string
	Settings           []tuningSetting
	AppliedCount       int
	SkippedCount       int
	UnsupportedCount   int
	SupportedCount     int
}

type tuningSetting struct {
	Name   string
	Before string
	Target string
	After  string
	Status string
	Notes  string
}

// Init applies safe process-level tuning and prints a startup report.
func Init() error {
	cpuCores := runtime.NumCPU()
	if cpuCores < 1 {
		cpuCores = 1
	}
	beforeGOMAXPROCS := runtime.GOMAXPROCS(0)
	runtime.GOMAXPROCS(cpuCores)
	afterGOMAXPROCS := runtime.GOMAXPROCS(0)

	result := platformInit()
	settings := append([]tuningSetting{{
		Name:   "Go scheduler",
		Before: fmt.Sprintf("%d", beforeGOMAXPROCS),
		Target: fmt.Sprintf("%d", cpuCores),
		After:  fmt.Sprintf("%d", afterGOMAXPROCS),
		Status: schedulerStatus(beforeGOMAXPROCS, afterGOMAXPROCS, cpuCores),
		Notes:  "GOMAXPROCS matches CPU cores",
	}}, result.Settings...)
	report := startupReport{
		OS:                 runtime.GOOS,
		Architecture:       runtime.GOARCH,
		CPUCores:           cpuCores,
		GOMAXPROCS:         afterGOMAXPROCS,
		OpenFileLimit:      result.OpenFileLimit,
		ThreadProcessLimit: result.ThreadProcessLimit,
		SocketOptions:      result.SocketOptions,
		ZeroCopy:           result.ZeroCopy,
		Status:             startupStatus(result),
		Settings:           settings,
	}
	report.AppliedCount, report.SkippedCount, report.UnsupportedCount, report.SupportedCount = tuningSettingCounts(settings)
	log.Print("\n" + formatStartupReport(report, colorsEnabled()))
	return result.CriticalErr
}

func schedulerStatus(beforeValue int, afterValue int, targetValue int) string {
	if afterValue == targetValue && beforeValue != afterValue {
		return stateApplied
	}
	if afterValue == targetValue {
		return stateSkipped
	}
	return stateSkipped
}

func startupStatus(result platformResult) string {
	if result.CriticalErr != nil {
		return startupStatusFailed
	}
	if result.OpenFileLimitPartial || result.ThreadProcessLimitPartial || result.SocketOptionsPartial || result.ZeroCopyPartial {
		return startupStatusPartial
	}
	return startupStatusOK
}

func formatStartupReport(report startupReport, colorEnabled bool) string {
	colorReset := ""
	colorTitle := ""
	colorDim := ""
	colorBorder := ""
	colorOK := ""
	colorPartial := ""
	colorFailed := ""
	if colorEnabled {
		colorReset = "\033[0m"
		colorTitle = "\033[1;36m"
		colorDim = "\033[2m"
		colorBorder = "\033[36m"
		colorOK = "\033[1;32m"
		colorPartial = "\033[1;33m"
		colorFailed = "\033[1;31m"
	}
	statusColor := colorOK
	switch report.Status {
	case startupStatusPartial:
		statusColor = colorPartial
	case startupStatusFailed:
		statusColor = colorFailed
	}

	var builder strings.Builder
	fmt.Fprintf(&builder, "%sSystem initialization%s\n", colorTitle, colorReset)
	fmt.Fprintf(&builder, "%sStatic file server startup tuning%s\n", colorDim, colorReset)
	fmt.Fprintf(&builder, "OS: %s  Architecture: %s  CPU cores: %d  GOMAXPROCS: %d\n", report.OS, report.Architecture, report.CPUCores, report.GOMAXPROCS)
	fmt.Fprintf(&builder, "Summary: %s%d applied%s, %s%d skipped%s, %s%d supported%s, %s%d unsupported%s\n",
		colorOK, report.AppliedCount, colorReset,
		colorPartial, report.SkippedCount, colorReset,
		colorOK, report.SupportedCount, colorReset,
		colorFailed, report.UnsupportedCount, colorReset)
	builder.WriteString(formatTuningTable(report.Settings, colorEnabled, colorBorder, colorReset))
	fmt.Fprintf(&builder, "\nStatus: %s%s%s", statusColor, report.Status, colorReset)
	return builder.String()
}

func formatTuningTable(settings []tuningSetting, colorEnabled bool, colorBorder string, colorReset string) string {
	const (
		nameWidth   = 22
		beforeWidth = 14
		targetWidth = 16
		afterWidth  = 44
		statusWidth = 12
		notesWidth  = 34
	)
	border := "+" + strings.Repeat("-", nameWidth+2) +
		"+" + strings.Repeat("-", beforeWidth+2) +
		"+" + strings.Repeat("-", targetWidth+2) +
		"+" + strings.Repeat("-", afterWidth+2) +
		"+" + strings.Repeat("-", statusWidth+2) +
		"+" + strings.Repeat("-", notesWidth+2) + "+"
	var builder strings.Builder
	writeBorder := func() {
		if colorEnabled {
			builder.WriteString(colorBorder)
		}
		builder.WriteString(border)
		if colorEnabled {
			builder.WriteString(colorReset)
		}
		builder.WriteString("\n")
	}
	writeBorder()
	builder.WriteString("| ")
	builder.WriteString(padRight("Setting", nameWidth))
	builder.WriteString(" | ")
	builder.WriteString(padRight("Before", beforeWidth))
	builder.WriteString(" | ")
	builder.WriteString(padRight("Target", targetWidth))
	builder.WriteString(" | ")
	builder.WriteString(padRight("After", afterWidth))
	builder.WriteString(" | ")
	builder.WriteString(padRight("Status", statusWidth))
	builder.WriteString(" | ")
	builder.WriteString(padRight("Notes", notesWidth))
	builder.WriteString(" |\n")
	writeBorder()
	for _, setting := range settings {
		builder.WriteString("| ")
		builder.WriteString(padRight(setting.Name, nameWidth))
		builder.WriteString(" | ")
		builder.WriteString(padRight(setting.Before, beforeWidth))
		builder.WriteString(" | ")
		builder.WriteString(padRight(setting.Target, targetWidth))
		builder.WriteString(" | ")
		builder.WriteString(padRight(setting.After, afterWidth))
		builder.WriteString(" | ")
		builder.WriteString(colorStatus(padRight(setting.Status, statusWidth), setting.Status, colorEnabled))
		builder.WriteString(" | ")
		builder.WriteString(padRight(setting.Notes, notesWidth))
		builder.WriteString(" |\n")
	}
	writeBorder()
	return builder.String()
}

func tuningSettingCounts(settings []tuningSetting) (int, int, int, int) {
	appliedCount := 0
	skippedCount := 0
	unsupportedCount := 0
	supportedCount := 0
	for _, setting := range settings {
		switch setting.Status {
		case stateApplied:
			appliedCount++
		case stateSkipped:
			skippedCount++
		case stateUnsupported:
			unsupportedCount++
		case stateSupported:
			supportedCount++
		}
	}
	return appliedCount, skippedCount, unsupportedCount, supportedCount
}

func colorStatus(text string, status string, enabled bool) string {
	if !enabled {
		return text
	}
	switch status {
	case stateApplied, stateSupported:
		return "\033[1;32m" + text + "\033[0m"
	case stateSkipped:
		return "\033[1;33m" + text + "\033[0m"
	case stateUnsupported:
		return "\033[1;31m" + text + "\033[0m"
	default:
		return text
	}
}

func padRight(value string, width int) string {
	if len(value) > width {
		if width <= 1 {
			return value[:width]
		}
		return value[:width-1] + "."
	}
	return value + strings.Repeat(" ", width-len(value))
}

func unsupportedSetting(name string, target string, notes string) tuningSetting {
	return tuningSetting{Name: name, Before: "-", Target: target, After: "-", Status: stateUnsupported, Notes: notes}
}

func socketOptionsSetting(reusePortSupported bool) tuningSetting {
	after := "reuseaddr, keepalive, nodelay"
	if reusePortSupported {
		after = "reuseaddr, reuseport, keepalive, nodelay"
	}
	return tuningSetting{
		Name:   "Socket options",
		Before: "OS default",
		Target: "low-latency TCP",
		After:  after,
		Status: stateApplied,
		Notes:  "applied before bind",
	}
}

func zeroCopySetting(status string, notes string) tuningSetting {
	after := "not available"
	if status == stateSupported {
		after = "available"
	}
	return tuningSetting{
		Name:   "Zero-copy transfer",
		Before: "runtime check",
		Target: "sendfile",
		After:  after,
		Status: status,
		Notes:  notes,
	}
}

// Listen returns a listener with the socket options prepared by this package.
func Listen(ctx context.Context, network string, address string) (net.Listener, error) {
	listenConfig := ListenConfig()
	return listenConfig.Listen(ctx, network, address)
}

// ListenConfig exposes the prepared socket options for callers that need a net.ListenConfig.
func ListenConfig() net.ListenConfig {
	return net.ListenConfig{
		Control:   Control,
		KeepAlive: 3 * time.Minute,
	}
}

// Control applies safe best-effort socket options to a socket before bind.
func Control(network string, address string, connection syscall.RawConn) error {
	return platformSocketControl(network, address, connection)
}
