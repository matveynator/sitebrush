package serviceinstall

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSecurityBoundaryInstallRejectsForgedServiceNames(t *testing.T) {
	for _, serviceName := range []string{
		"../sitebrush",
		"..",
		".",
		"/tmp/sitebrush",
		"sitebrush/../../owned",
		"sitebrush\\..\\owned",
		"sitebrush;touch-owned",
		"sitebrush $(owned)",
		"sitebrush\nowned",
		strings.Repeat("a", 129),
	} {
		if validServiceName(serviceName) {
			t.Fatalf("SECURITY: forged service name accepted by validator: %q", serviceName)
		}
		if _, err := buildInstallPlan(Options{ServiceName: serviceName}); err == nil {
			t.Fatalf("SECURITY: forged service name reached install plan: %q", serviceName)
		}
	}
}

func TestSecurityBoundaryInstallAcceptsOnlyPortableServiceNameAlphabet(t *testing.T) {
	for _, serviceName := range []string{
		"sitebrush",
		"sitebrush-v2",
		"sitebrush_test",
		"sitebrush.test",
		"SiteBrush123",
	} {
		if !validServiceName(serviceName) {
			t.Fatalf("valid service name rejected: %q", serviceName)
		}
	}
}

func TestSecurityBoundaryInstallKeepsArgumentsSeparateFromShellSyntax(t *testing.T) {
	storagePath := platformAbsolutePath(`/srv/site;touch /tmp/storage && echo owned`)
	plan, err := buildInstallPlan(Options{
		Port:        `8080;touch /tmp/owned $(command)`,
		StoragePath: storagePath,
		ServiceName: "sitebrush-security-test",
	})
	if err != nil {
		t.Fatalf("buildInstallPlan() error = %v", err)
	}

	wantArgs := 7
	if runtime.GOOS == "windows" {
		wantArgs = 9
	}
	if len(plan.ExecArgs) != wantArgs {
		t.Fatalf("exec argument count = %d, want %d: %#v", len(plan.ExecArgs), wantArgs, plan.ExecArgs)
	}
	if plan.ExecArgs[1] != "-port" || plan.ExecArgs[2] != `8080;touch /tmp/owned $(command)` {
		t.Fatalf("port argument was split or rewritten: %#v", plan.ExecArgs)
	}
	if plan.ExecArgs[3] != "-path" || plan.ExecArgs[4] != storagePath {
		t.Fatalf("storage argument was split or rewritten: %#v", plan.ExecArgs)
	}
	logFile := filepath.Join(storagePath, "storage", "logs", "sitebrush.log")
	if plan.ExecArgs[5] != "-logfile" || plan.ExecArgs[6] != logFile {
		t.Fatalf("logfile argument was split or rewritten: %#v", plan.ExecArgs)
	}
}

func TestSecurityBoundaryInstalledBinaryCopyDoesNotFollowDestinationSymlink(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	outside := filepath.Join(root, "outside")
	destination := filepath.Join(root, "installed")
	if err := os.WriteFile(source, []byte("trusted-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, []byte("outside-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, destination); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	err := copyExecutable(source, destination)
	if err == nil {
		t.Fatal("SECURITY: installer followed a destination symlink while copying executable")
	}
	content, readErr := os.ReadFile(outside)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(content) != "outside-secret" {
		t.Fatalf("SECURITY: installer overwrote symlink target outside install destination: %q", content)
	}
}
