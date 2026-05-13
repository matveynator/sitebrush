package setupwizard

import (
	"strings"
	"testing"
)

func TestValidateDefaultDBType(t *testing.T) {
	options := availableDBTypes()

	if err := validateDefaultDBType("sqlite", options); err != nil {
		t.Fatalf("sqlite should be accepted: %v", err)
	}
	if err := validateDefaultDBType("", options); err != nil {
		t.Fatalf("empty default should be accepted: %v", err)
	}
	if err := validateDefaultDBType("duckdb", options); err == nil {
		t.Fatalf("duckdb should be rejected when unavailable")
	}
}

func TestBuildExecArgsUsesOnlySitebrushFlags(t *testing.T) {
	args := buildExecArgs("/usr/local/bin/sitebrush", 8080, "/var/lib/sitebrush", "sqlite", "/var/lib/sitebrush/storage/db/sitebrush.db")
	got := strings.Join(args, " ")
	want := "/usr/local/bin/sitebrush -port 8080 -storage-path /var/lib/sitebrush -db-type sqlite -db-path /var/lib/sitebrush/storage/db/sitebrush.db"
	if got != want {
		t.Fatalf("buildExecArgs() = %q, want %q", got, want)
	}

	for _, forbidden := range []string{"-domain", "-support-email", "-json-archive-path", "-import-tgz-url", "-safecast-realtime"} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("buildExecArgs() contains unsupported flag %q in %q", forbidden, got)
		}
	}
}

func TestBuildExecArgsUsesStandardPortPair(t *testing.T) {
	args := buildExecArgs("/usr/local/bin/sitebrush", 80, "/var/lib/sitebrush", "sqlite", "/var/lib/sitebrush/storage/db/sitebrush.db")
	got := strings.Join(args, " ")
	if !strings.Contains(got, "-port 80,443") {
		t.Fatalf("buildExecArgs() should use comma port pair, got %q", got)
	}
}

func TestSystemctlCommandsStartAndCheckLogs(t *testing.T) {
	commands := systemctlCommands(false, "sitebrush-80.service")
	got := strings.Join(commands, "\n")
	for _, want := range []string{
		"systemctl daemon-reload",
		"systemctl enable --now sitebrush-80.service",
		"systemctl status --no-pager --full sitebrush-80.service",
		"journalctl -u sitebrush-80.service -n 40 --no-pager",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("systemctlCommands() missing %q in:\n%s", want, got)
		}
	}
}

func TestPortFormattingAndCustomFallback(t *testing.T) {
	if got := formatPortChoice(80); got != "80,443" {
		t.Fatalf("formatPortChoice(80) = %q", got)
	}
	if got := formatPortChoice(8080); got != "8080" {
		t.Fatalf("formatPortChoice(8080) = %q", got)
	}
	if got := suggestCustomPort(80); got != 8080 {
		t.Fatalf("suggestCustomPort(80) = %d, want 8080", got)
	}
	if got := suggestCustomPort(9090); got != 9090 {
		t.Fatalf("suggestCustomPort(9090) = %d, want 9090", got)
	}
}
