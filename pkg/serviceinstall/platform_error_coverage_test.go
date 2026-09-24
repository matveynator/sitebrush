package serviceinstall

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServiceManagersPropagateRequiredCommandFailures(t *testing.T) {
	root := t.TempDir()
	for _, directory := range []string{
		"etc/systemd/system", "etc/init.d", "etc/service", "etc/sv",
		"etc/init", "Library/LaunchDaemons", "usr/local/etc/rc.d", "etc/rc.d",
		"etc",
	} {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(directory)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "etc", "rc.conf"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	sentinel := errors.New("required command failed")
	probe := runtimeProbe{
		goos: "linux",
		filesystemRoot: root,
		lookPath: func(string) (string, error) { return "/test/tool", nil },
		fileExists: func(string) bool { return false },
		dirExists: func(path string) bool {
			info, err := os.Stat(path)
			return err == nil && info.IsDir()
		},
		commandRuns: func(context.Context, string, ...string) (string, error) {
			return "", sentinel
		},
	}
	plan := installPlan{
		ServiceName: "sitebrush-test",
		BinaryPath: "/opt/sitebrush",
		WorkingDir: "/srv/sitebrush",
		ExecArgs: []string{"/opt/sitebrush", "-port", "8080"},
	}

	installers := []struct {
		name string
		fn   func(context.Context, runtimeProbe, installPlan) (Result, error)
	}{
		{"systemd", installSystemd},
		{"openrc", installOpenRC},
		{"runit", installRunit},
		{"upstart", installUpstart},
		{"sysv", installSysVInit},
		{"launchd", installLaunchd},
		{"freebsd", installFreeBSDRcD},
		{"openbsd", installOpenBSDRcD},
		{"netbsd", installNetBSDRcD},
	}
	for _, tc := range installers {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.fn(context.Background(), probe, plan)
			if err == nil {
				t.Fatalf("%s installer hid required command failure", tc.name)
			}
		})
	}

	windowsProbe := probe
	windowsProbe.goos = "windows"
	if _, err := installWindowsService(context.Background(), windowsProbe, plan); err == nil {
		t.Fatal("Windows installer hid command failure")
	}
}

func TestServiceManagerFallbackAndAlternateBranches(t *testing.T) {
	root := t.TempDir()
	for _, directory := range []string{"etc/init", "service", "etc/sv", "etc", "usr/local/etc/rc.d"} {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(directory)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "etc", "rc.conf"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	plan := installPlan{
		ServiceName: "sitebrush-test",
		BinaryPath: "/opt/sitebrush",
		WorkingDir: "/srv/sitebrush",
		ExecArgs: []string{"/opt/sitebrush"},
	}

	t.Run("upstart restart falls back to start", func(t *testing.T) {
		var commands []string
		probe := runtimeProbe{
			filesystemRoot: root,
			commandRuns: func(_ context.Context, name string, args ...string) (string, error) {
				command := strings.Join(append([]string{name}, args...), " ")
				commands = append(commands, command)
				if strings.Contains(command, " restart ") {
					return "", errors.New("restart failed")
				}
				return "ok", nil
			},
		}
		if _, err := installUpstart(context.Background(), probe, plan); err != nil {
			t.Fatal(err)
		}
		foundStart := false
		for _, command := range commands {
			if strings.Contains(command, " start ") {
				foundStart = true
			}
		}
		if !foundStart {
			t.Fatal("Upstart restart failure did not use start fallback")
		}
	})

	t.Run("runit uses slash-service fallback", func(t *testing.T) {
		probe := runtimeProbe{
			filesystemRoot: root,
			dirExists: func(path string) bool {
				return path == filepath.Join(root, "service")
			},
			commandRuns: func(context.Context, string, ...string) (string, error) { return "ok", nil },
		}
		result, err := installRunit(context.Background(), probe, plan)
		if err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(root, "service", plan.ServiceName)
		if _, err := os.Lstat(link); err != nil {
			t.Fatalf("runit fallback link missing: %v; result=%#v", err, result)
		}
		if _, err := uninstallRunit(context.Background(), probe, plan); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("freebsd without sysrc updates rc.conf", func(t *testing.T) {
		probe := runtimeProbe{
			filesystemRoot: root,
			lookPath: func(string) (string, error) { return "", errors.New("missing") },
			commandRuns: func(context.Context, string, ...string) (string, error) { return "ok", nil },
		}
		if _, err := installFreeBSDRcD(context.Background(), probe, plan); err != nil {
			t.Fatal(err)
		}
		body, err := os.ReadFile(filepath.Join(root, "etc", "rc.conf"))
		if err != nil || !strings.Contains(string(body), plan.ServiceName+"_enable") {
			t.Fatalf("rc.conf enable missing: %q %v", body, err)
		}
		if _, err := uninstallFreeBSDRcD(context.Background(), probe, plan); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("windows existing service uses config branch", func(t *testing.T) {
		var commands []string
		probe := runtimeProbe{
			commandRuns: func(_ context.Context, name string, args ...string) (string, error) {
				commands = append(commands, strings.Join(append([]string{name}, args...), " "))
				return "ok", nil
			},
		}
		if _, err := installWindowsService(context.Background(), probe, plan); err != nil {
			t.Fatal(err)
		}
		foundConfig := false
		for _, command := range commands {
			if strings.Contains(command, "sc.exe config ") {
				foundConfig = true
			}
		}
		if !foundConfig {
			t.Fatalf("existing Windows service was not configured: %#v", commands)
		}
	})
}

func TestServiceInstallFilesystemAndMetadataFailureBranches(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	if err := os.WriteFile(source, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := copyExecutable(filepath.Join(root, "missing"), filepath.Join(root, "dest")); err == nil {
		t.Fatal("copyExecutable accepted missing source")
	}
	destinationDirectory := filepath.Join(root, "directory-destination")
	if err := os.Mkdir(destinationDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := copyExecutable(source, destinationDirectory); err == nil {
		t.Fatal("copyExecutable accepted directory destination")
	}

	blockedParent := filepath.Join(root, "blocked")
	if err := os.WriteFile(blockedParent, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := prepareInstallFilesystem(installPlan{
		BinaryPath: filepath.Join(blockedParent, "sitebrush"),
		WorkingDir: filepath.Join(root, "work"),
	}); err == nil {
		t.Fatal("prepareInstallFilesystem hid binary parent failure")
	}

	servicePath := filepath.Join(root, "service")
	if err := writeServiceMetadata("custom", installPlan{
		ServiceName: servicePath,
		BinaryPath: source,
		WorkingDir: root,
	}, "en"); err != nil {
		t.Fatal(err)
	}
	metadataPath := expectedServiceMetadataPath("custom", servicePath)
	if err := os.Chmod(metadataPath, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := removeServiceMetadata("custom", servicePath); err != nil {
		t.Fatal(err)
	}
	if err := removeServiceMetadata("custom", servicePath); err != nil {
		t.Fatal(err)
	}
}
