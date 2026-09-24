package serviceinstall

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServiceInstallFilesystemAndCommandHelpers(t *testing.T) {
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "sitebrush-source")
	if err := os.WriteFile(sourcePath, []byte("binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	destinationPath := filepath.Join(directory, "bin", "sitebrush")
	plan := installPlan{Options: Options{BinaryPath: sourcePath}, BinaryPath: destinationPath, WorkingDir: filepath.Join(directory, "data")}
	if err := prepareInstallFilesystem(plan); err != nil {
		t.Fatal(err)
	}
	installedBinary, err := os.ReadFile(destinationPath)
	if err != nil || string(installedBinary) != "binary" {
		t.Fatalf("installed binary = %q, %v", installedBinary, err)
	}
	installedInfo, err := os.Stat(destinationPath)
	if err != nil || installedInfo.Mode().Perm() != 0o755 {
		t.Fatalf("installed mode = %v, %v", installedInfo, err)
	}
	if err := copyExecutable(filepath.Join(directory, "missing"), filepath.Join(directory, "missing-destination")); err == nil {
		t.Fatal("missing source binary was copied")
	}
	if err := copyExecutable(sourcePath, filepath.Join(directory, "missing-directory", "binary")); err == nil {
		t.Fatal("copy into missing destination directory succeeded")
	}
	if err := prepareInstallFilesystem(installPlan{BinaryPath: destinationPath, WorkingDir: filepath.Join(directory, "data"), Options: Options{BinaryPath: destinationPath}}); err != nil {
		t.Fatalf("already-installed binary should not be copied: %v", err)
	}
	blockerPath := filepath.Join(directory, "blocker")
	if err := os.WriteFile(blockerPath, []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := prepareInstallFilesystem(installPlan{BinaryPath: filepath.Join(blockerPath, "bin"), WorkingDir: directory}); err == nil {
		t.Fatal("filesystem preparation ignored binary directory failure")
	}
	if got := windowsCommandLine([]string{`C:\Program Files\sitebrush.exe`, `name "quoted"`}); got != `"C:\\Program Files\\sitebrush.exe" "name \"quoted\""` {
		t.Fatalf("Windows command line = %q", got)
	}
	if got := expectedServicePath("systemd", "sitebrush"); got != "/etc/systemd/system/sitebrush.service" {
		t.Fatalf("systemd service path = %q", got)
	}
	if got := expectedServicePath("Windows Service", "sitebrush"); got != "Windows Service: sitebrush" {
		t.Fatalf("Windows service path = %q", got)
	}
	if got := expectedServiceMetadataPath("systemd", "sitebrush"); !strings.HasSuffix(got, "sitebrush.service.sitebrush.json") {
		t.Fatalf("service metadata path = %q", got)
	}
	if err := writeFile(filepath.Join(directory, "nested", "file"), "content", 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(filepath.Join(directory, "blocker", "file"), "content", 0o600); err == nil {
		t.Fatal("writeFile ignored parent path error")
	}
	if err := os.Mkdir(filepath.Join(directory, "remove-directory"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "remove-directory", "child"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := removeFileIfExists(filepath.Join(directory, "remove-directory")); err == nil {
		t.Fatal("removeFileIfExists removed a directory")
	}
	if err := removeFileIfExists(filepath.Join(directory, "nested", "file")); err != nil {
		t.Fatal(err)
	}
	if err := removeFileIfExists(filepath.Join(directory, "nested", "missing")); err != nil {
		t.Fatal(err)
	}
	if err := removeAllIfExists(filepath.Join(directory, "nested")); err != nil {
		t.Fatal(err)
	}
	if installOptionOrDefault("  value ", "fallback") != "  value " || installOptionOrDefault(" ", "fallback") != "fallback" {
		t.Fatal("install option fallback failed")
	}
}

func TestServiceInstallRuntimeHelpers(t *testing.T) {
	probe := newRuntimeProbe()
	if probe.goos == "" || probe.goarch == "" || probe.lookPath == nil || probe.commandRuns == nil || detectOSVersion() == "" {
		t.Fatalf("runtime probe incomplete: %+v", probe)
	}
	if output, err := runCommand(context.Background(), os.Args[0], "-test.run=TestServiceInstallCommandHelperProcess"); err != nil || !strings.Contains(output, "PASS") {
		t.Fatalf("runCommand success=%q err=%v", output, err)
	}
	if _, err := runCommand(context.Background(), "sitebrush-test-command-that-does-not-exist"); err == nil {
		t.Fatal("missing command unexpectedly succeeded")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := runCommand(canceled, os.Args[0], "-test.run=TestServiceInstallCommandHelperProcess"); err == nil {
		t.Fatal("runCommand ignored cancellation")
	}
	for _, manager := range []string{"OpenRC", "SysV init", "runit", "Upstart", "rc.d", "rc.d/rcctl"} {
		if expectedServicePath(manager, "service") == "service" {
			t.Errorf("service path missing for %s", manager)
		}
	}
	if defaultStoragePath() == "" || defaultInstalledBinaryPath("sitebrush") == "" {
		t.Fatal("default install paths are empty")
	}
}

func TestServiceInstallCommandHelperProcess(t *testing.T) {}

func TestServiceInstallCommandAndPlatformHelperBranches(t *testing.T) {
	probe := fakeProbe("linux", nil, nil)
	var commands []string
	probe.commandRuns = func(_ context.Context, name string, args ...string) (string, error) {
		commands = append(commands, strings.Join(append([]string{name}, args...), " "))
		if name == "fail" {
			return "", errors.New("failed")
		}
		return "ok", nil
	}
	executed, err := runInstallCommands(context.Background(), probe, [][]string{{}, {"one", "arg"}, {"two"}})
	if err != nil || len(executed) != 2 || len(commands) != 2 {
		t.Fatalf("run install commands = %#v %#v %v", executed, commands, err)
	}
	executed, err = runServiceCommands(context.Background(), probe, []serviceCommand{{Args: []string{"fail"}, AllowFailure: true}, {Args: []string{"two"}}})
	if err != nil || len(executed) != 2 {
		t.Fatalf("allowed command failure = %#v, %v", executed, err)
	}
	if _, err := runServiceCommands(context.Background(), probe, []serviceCommand{{Args: []string{"fail"}}}); err == nil {
		t.Fatal("required command failure was ignored")
	}
	if expectedServicePath("unknown", "custom") != "custom" || !strings.Contains(expectedServicePath("Upstart", "sitebrush"), "sitebrush.conf") || !strings.Contains(expectedServicePath("launchd", "sitebrush"), "net.sitebrush.sitebrush.plist") {
		t.Fatal("service manager path selection failed")
	}
	if !strings.Contains(formatBytes(1024), "KB") || formatBytes(10) != "10 B" {
		t.Fatal("service installer byte formatting failed")
	}
	if !strings.Contains(diskSpaceSummary("/", cliText{DiskSpaceFreeOf: "free of", DiskSpaceUnknown: "unknown"}), "free of") {
		t.Fatal("disk space summary was not formatted")
	}
	if got := firstUsefulVersionLine("NAME=Linux\nPRETTY_NAME=\"Example Linux\"\n"); got != "Example Linux" || firstUsefulVersionLine("NAME=Linux") != "unknown" {
		t.Fatalf("OS version selection = %q", got)
	}
	if color := (cliTheme{}).success("ok"); color != "ok" {
		t.Fatalf("plain success color = %q", color)
	}
}

func TestServiceMetadataRoundTripAndResultOutput(t *testing.T) {
	servicePath := filepath.Join(t.TempDir(), "service")
	plan := installPlan{ServiceName: servicePath, BinaryPath: "/opt/sitebrush", WorkingDir: "/srv/sitebrush", Options: Options{Port: "8080", StoragePath: "/srv/data", DBType: "sqlite", DBPath: "/srv/data/site.db"}}
	if err := writeServiceMetadata("custom", plan, "ru-RU"); err != nil {
		t.Fatal(err)
	}
	metadata, ok := readServiceMetadata("custom", servicePath)
	if !ok || metadata.Language != "ru" || metadata.Port != "8080" || metadata.DBPath != "/srv/data/site.db" {
		t.Fatalf("metadata=%+v, ok=%v", metadata, ok)
	}
	options := applyStoredServiceMetadata(Options{ServiceName: servicePath, Port: "80"}, "custom")
	if options.Port != "8080" || options.StoragePath != "/srv/data" || options.Language != "ru" {
		t.Fatalf("restored options=%+v", options)
	}
	if _, ok := readServiceMetadata("custom", servicePath+"-missing"); ok {
		t.Fatal("missing metadata was read")
	}
	if err := os.WriteFile(expectedServiceMetadataPath("custom", servicePath+"-broken"), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok := readServiceMetadata("custom", servicePath+"-broken"); ok {
		t.Fatal("invalid metadata was read")
	}
	if err := removeServiceMetadata("custom", servicePath); err != nil {
		t.Fatal(err)
	}
	if _, ok := readServiceMetadata("custom", servicePath); ok {
		t.Fatal("removed metadata remains")
	}
	if err := writeServiceMetadata("custom", installPlan{ServiceName: servicePath + "-defaults"}, ""); err != nil {
		t.Fatal(err)
	}
	defaults, ok := readServiceMetadata("custom", servicePath+"-defaults")
	if !ok || defaults.Language != "en" || defaults.Port != "80,443" || defaults.DBType != "sqlite" {
		t.Fatalf("default metadata=%+v, ok=%v", defaults, ok)
	}

	var output strings.Builder
	result := Result{OS: "linux", OSVersion: "test", Arch: "amd64", InitSystem: "systemd", BinaryPath: "/opt/sitebrush", ServicePath: "/etc/sitebrush.service", Commands: []string{"systemctl enable sitebrush"}}
	printResult(&output, result, "en")
	if !strings.Contains(output.String(), result.ServicePath) || !strings.Contains(output.String(), result.Commands[0]) {
		t.Fatalf("install output=%q", output.String())
	}
	output.Reset()
	printUninstallResult(&output, result, "en")
	if !strings.Contains(output.String(), result.BinaryPath) || !strings.Contains(output.String(), result.Commands[0]) {
		t.Fatalf("uninstall output=%q", output.String())
	}
	printResult(nil, result, "en")
	printUninstallResult(nil, result, "en")
}
