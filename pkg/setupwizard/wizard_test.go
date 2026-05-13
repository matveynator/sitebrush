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
