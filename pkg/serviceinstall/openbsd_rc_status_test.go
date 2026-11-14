package serviceinstall

import (
	"strings"
	"testing"
)

func TestOpenBSDRcDMatchesActualLauncherCommand(t *testing.T) {
	plan := installPlan{
		BinaryPath: "/usr/local/bin/sitebrush",
		WorkingDir: "/data1/sitebrush",
		ExecArgs: []string{"/usr/local/bin/sitebrush", "-port", "80,443", "-path", "/data1/sitebrush"},
	}
	script := openBSDRcScript(plan)
	if !strings.Contains(script, `pexp="^${daemon}([[:space:]]|$)"`) {
		t.Fatalf("OpenBSD rc.d must match the launcher itself, not its internal worker flags:\n%s", script)
	}
	if strings.Contains(script, `pexp="${daemon}.*-service-runtime"`) {
		t.Fatal("OpenBSD rc.d expects an internal flag absent from the real launcher")
	}
}
