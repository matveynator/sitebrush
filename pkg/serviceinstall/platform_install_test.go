package serviceinstall

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestServiceManagersInstallAndUninstallUnderTemporaryRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "service"), 0o755); err != nil {
		t.Fatal(err)
	}
	probe := runtimeProbe{
		goos: "linux", filesystemRoot: root,
		lookPath:    func(name string) (string, error) { return "/test/" + name, nil },
		dirExists:   func(path string) bool { return path == filepath.Join(root, "service") },
		fileExists:  func(string) bool { return false },
		commandRuns: func(_ context.Context, _ string, _ ...string) (string, error) { return "ok", nil },
	}
	plan := installPlan{ServiceName: "sitebrush-test", BinaryPath: "/opt/sitebrush", WorkingDir: "/srv/sitebrush", ExecArgs: []string{"/opt/sitebrush", "-port", "8080"}}
	testCases := []struct {
		name      string
		install   func(context.Context, runtimeProbe, installPlan) (Result, error)
		uninstall func(context.Context, runtimeProbe, installPlan) (Result, error)
	}{
		{"systemd", installSystemd, uninstallSystemd},
		{"OpenRC", installOpenRC, uninstallOpenRC},
		{"runit", installRunit, uninstallRunit},
		{"Upstart", installUpstart, uninstallUpstart},
		{"SysV", installSysVInit, uninstallSysVInit},
		{"launchd", installLaunchd, uninstallLaunchd},
		{"FreeBSD rc.d", installFreeBSDRcD, uninstallFreeBSDRcD},
		{"OpenBSD rc.d", installOpenBSDRcD, uninstallOpenBSDRcD},
		{"NetBSD rc.d", installNetBSDRcD, uninstallNetBSDRcD},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			installed, err := testCase.install(context.Background(), probe, plan)
			if err != nil {
				t.Fatalf("install: %v", err)
			}
			if installed.ServicePath == "" {
				t.Fatal("install returned an empty service path")
			}
			if _, err := os.Stat(installed.ServicePath); err != nil {
				t.Fatalf("service file %q: %v", installed.ServicePath, err)
			}
			removed, err := testCase.uninstall(context.Background(), probe, plan)
			if err != nil {
				t.Fatalf("uninstall: %v", err)
			}
			if removed.ServicePath == "" {
				t.Fatal("uninstall returned an empty service path")
			}
			if _, err := os.Stat(installed.ServicePath); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("service file remains: %v", err)
			}
		})
	}

	windowsProbe := probe
	windowsProbe.goos = "windows"
	queryCount := 0
	windowsProbe.commandRuns = func(_ context.Context, name string, args ...string) (string, error) {
		if name == "sc.exe" && len(args) > 0 && args[0] == "query" {
			queryCount++
			if queryCount == 1 {
				return "missing", errors.New("service missing")
			}
		}
		return "ok", nil
	}
	if _, err := installWindowsService(context.Background(), windowsProbe, plan); err != nil {
		t.Fatalf("install Windows service: %v", err)
	}
	if _, err := uninstallWindowsService(context.Background(), windowsProbe, plan); err != nil {
		t.Fatalf("uninstall Windows service: %v", err)
	}
}

func TestBSDRCConfigurationHelpersUseProvidedPath(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "etc", "rc.conf")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := appendRcConfAt(configPath, "sitebrush_enable=\"YES\"\n"); err != nil {
		t.Fatal(err)
	}
	if err := appendRcConfAt(configPath, "sitebrush_enable=\"YES\"\n"); err != nil {
		t.Fatal(err)
	}
	if err := appendRcConfAt(configPath, "other_enable=\"YES\"\n"); err != nil {
		t.Fatal(err)
	}
	if err := removeRcConfLinesAt(configPath, "sitebrush"); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "other_enable=\"YES\"\n" {
		t.Fatalf("rc.conf after removal=%q", content)
	}
	if err := removeRcConfLinesAt(filepath.Join(t.TempDir(), "missing.conf"), "sitebrush"); err != nil {
		t.Fatal(err)
	}
	if err := appendRcConfAt(filepath.Join(t.TempDir(), "missing", "rc.conf"), "line\n"); err == nil {
		t.Fatal("append without parent directory unexpectedly succeeded")
	}
}
