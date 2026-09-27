package main

import (
	"flag"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCrossCompileInputValidationAndFailurePaths(t *testing.T) {
	t.Parallel()

	var destinations syncDestinationFlags
	if err := destinations.Set("missing-separator"); err == nil {
		t.Fatal("malformed sync destination was accepted")
	}
	if got := (&syncDestinationFlags{}).String(); got != "" {
		t.Fatalf("empty destination string = %q", got)
	}
	if got := (&destinations).String(); got != "" {
		t.Fatalf("failed Set changed destinations: %q", got)
	}

	for _, testCase := range []struct {
		filter buildTargetFilter
		label  string
	}{
		{filter: buildTargetFilter{}, label: "all targets"},
		{filter: buildTargetFilter{goos: "linux"}, label: "os=linux"},
		{filter: buildTargetFilter{goarch: "arm64"}, label: "arch=arm64"},
		{filter: buildTargetFilter{goos: "windows", goarch: "arm64"}, label: "os=windows arch=arm64"},
	} {
		if got := testCase.filter.label(); got != testCase.label {
			t.Fatalf("filter label = %q, want %q", got, testCase.label)
		}
	}
	if (buildTargetFilter{goos: "linux"}).matches("windows", "amd64") {
		t.Fatal("OS mismatch matched target")
	}
	if !(buildTargetFilter{goarch: "amd64"}).matchesOS("windows") {
		t.Fatal("architecture-only filter rejected an OS")
	}

	if _, err := dockerWorkspacePath("/repo", "/repo/../outside"); err == nil {
		t.Fatal("docker workspace accepted an escaping path")
	}
	if got, err := dockerWorkspacePath("/repo", "/repo"); err != nil || got != dockerWorkspaceRoot {
		t.Fatalf("docker workspace root = %q, %v", got, err)
	}
	if got := dockerVolumeName("Registry/Builder:latest", "cache"); got != "Registry-Builder-latest-cache" {
		t.Fatalf("docker volume name = %q", got)
	}
}

func TestCrossCompileArtifactAndCommandErrors(t *testing.T) {
	t.Parallel()

	directory := t.TempDir()
	if err := verifyNonEmptyFile(filepath.Join(directory, "missing")); err == nil {
		t.Fatal("missing artifact was accepted")
	}
	emptyPath := filepath.Join(directory, "empty")
	if err := os.WriteFile(emptyPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifyNonEmptyFile(emptyPath); err == nil {
		t.Fatal("empty artifact was accepted")
	}
	directoryPath := filepath.Join(directory, "directory")
	if err := os.Mkdir(directoryPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := verifyNonEmptyFile(directoryPath); err == nil {
		t.Fatal("directory was accepted as an artifact")
	}
	if _, err := fileMD5(filepath.Join(directory, "missing")); err == nil {
		t.Fatal("missing file received a checksum")
	}
	if err := writeMD5SumsFile(filepath.Join(directory, "missing")); err == nil {
		t.Fatal("checksum generation accepted a missing directory")
	}
	if err := cleanupDesktopBuildIntermediates(filepath.Join(directory, "missing")); err == nil {
		t.Fatal("cleanup accepted a missing directory")
	}

	binDirectory := t.TempDir()
	stubPath := filepath.Join(binDirectory, "failing-command")
	if err := os.WriteFile(stubPath, []byte("#!/bin/sh\nexit 17\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := runCommand(directory, stubPath); err == nil {
		t.Fatal("runCommand hid command failure")
	}
	if _, err := runOutput(directory, stubPath); err == nil {
		t.Fatal("runOutput hid command failure")
	}
	if commandExists(filepath.Join(binDirectory, "missing-command")) {
		t.Fatal("commandExists reported a missing command")
	}
	if !commandExists(stubPath) {
		t.Fatal("commandExists missed an executable")
	}
}

func TestCrossCompileBuildSelectionFailsClosedWithoutDocker(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	options := desktopBuildOptions{targetFilter: buildTargetFilter{goos: "linux", goarch: "amd64"}}
	if built, err := buildLinuxDesktopArtifacts(t.TempDir(), t.TempDir(), "sitebrush", "test", options); built || err == nil {
		t.Fatalf("Linux desktop build without Docker = built %t, err %v", built, err)
	}
	options.targetFilter.goos = "windows"
	if built, err := buildWindowsDesktopArtifacts(t.TempDir(), t.TempDir(), "sitebrush", "test", options); built || err == nil {
		t.Fatalf("Windows desktop build without Docker = built %t, err %v", built, err)
	}
	if built, err := buildLinuxDesktopArtifacts(t.TempDir(), t.TempDir(), "sitebrush", "test", desktopBuildOptions{targetFilter: buildTargetFilter{goos: "freebsd"}}); built || err != nil {
		t.Fatalf("non-Linux selection = built %t, err %v", built, err)
	}
}

func TestCrossCompileVersionAndToolSelection(t *testing.T) {
	t.Setenv("GITHUB_RUN_NUMBER", "")
	binDirectory := t.TempDir()
	gitPath := filepath.Join(binDirectory, "git")
	if err := os.WriteFile(gitPath, []byte("#!/bin/sh\nif [ \"$1\" = \"rev-list\" ]; then printf '42\\n'; else exit 1; fi\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDirectory)
	if got := defaultVersionLabel(t.TempDir()); got != "42" {
		t.Fatalf("default version from git = %q", got)
	}
	if got := sanitizePathSegment("...///"); got != "" {
		t.Fatalf("unsafe version was not rejected: %q", got)
	}
	if runtime.GOOS != "windows" {
		if got, ok := linuxDesktopVariant(); ok || got != "" {
			t.Fatalf("desktop variant without pkg-config = %q, %t", got, ok)
		}
	}
	if got := strings.TrimSpace(remoteSyncDirectory(" /srv/releases/// ")); got != "/srv/releases" {
		t.Fatalf("remote sync directory = %q", got)
	}
}

func TestCrossCompileDockerCommandFailures(t *testing.T) {
	binDirectory := t.TempDir()
	dockerPath := filepath.Join(binDirectory, "docker")
	if err := os.WriteFile(dockerPath, []byte("#!/bin/sh\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDirectory)
	if dockerImageExists(t.TempDir(), "missing") {
		t.Fatal("failed Docker inspect reported an image")
	}
	if _, err := dockerNativePlatform(t.TempDir()); err == nil {
		t.Fatal("failed Docker info was accepted")
	}
	if err := ensureDockerPlatformSupport(t.TempDir(), "linux/arm64", "missing"); err == nil {
		t.Fatal("Docker emulation failure was hidden")
	}
	if err := installDockerPlatformEmulation(t.TempDir(), "linux/arm64", "arm64"); err == nil {
		t.Fatal("binfmt registration failure was hidden")
	}
}

func TestCrossCompileMainBuildsFilteredServerArtifact(t *testing.T) {
	binDirectory := t.TempDir()
	goPath := filepath.Join(binDirectory, "go")
	if err := os.WriteFile(goPath, []byte("#!/bin/sh\nwhile [ \"$#\" -gt 0 ]; do if [ \"$1\" = \"-o\" ]; then shift; printf binary > \"$1\"; fi; shift; done\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDirectory)
	previousArguments := os.Args
	previousFlagSet := flag.CommandLine
	defer func() {
		os.Args = previousArguments
		flag.CommandLine = previousFlagSet
	}()
	flag.CommandLine = flag.NewFlagSet("crosscompile-test", flag.ContinueOnError)
	os.Args = []string{"crosscompile", "-mode=server-app", "-os=linux", "-arch=amd64", "-version=coverage-test", "-output-dir=.crosscompile-coverage-output"}
	main()
	repoRoot, err := findRepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(repoRoot, ".crosscompile-coverage-output", "coverage-test", "server-app", "sitebrush_linux_amd64")
	if err := verifyNonEmptyFile(artifact); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(repoRoot, ".crosscompile-coverage-output")); err != nil {
		t.Fatal(err)
	}
}

func TestCrossCompileArtifactOrchestrationRejectsInfrastructureFailures(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	filePath := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(filePath, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := buildServerAppArtifacts(t.TempDir(), filePath, "sitebrush", "test", buildTargetFilter{}); err == nil {
		t.Fatal("server build accepted a file as output directory")
	}
	if err := buildDesktopAppArtifacts(t.TempDir(), filePath, "sitebrush", "test", desktopBuildOptions{}); err == nil {
		t.Fatal("desktop build accepted a file as output directory")
	}
	if err := buildLinuxDesktopArtifactInDocker(t.TempDir(), t.TempDir(), "sitebrush", "test", "amd64", "gtk40", desktopBuildOptions{}); err == nil {
		t.Fatal("Linux Docker build hid missing Docker")
	}
	if err := buildWindowsDesktopArtifactInDocker(t.TempDir(), t.TempDir(), "sitebrush", "test", "amd64", desktopBuildOptions{}); err == nil {
		t.Fatal("Windows Docker build hid missing Docker")
	}
	if _, err := packageMacOSDesktopDMG(t.TempDir(), "sitebrush", filepath.Join(t.TempDir(), "missing"), "test"); err == nil {
		t.Fatal("DMG packaging accepted a missing binary")
	}
	if err := createMacOSAppBundle(filepath.Join(t.TempDir(), "sitebrush.app"), filepath.Join(t.TempDir(), "missing"), "test"); err == nil {
		t.Fatal("app bundle copied a missing binary")
	}
	if _, err := createMacOSIcon(t.TempDir()); err == nil {
		t.Fatal("icon generation hid missing conversion tools")
	}
	if err := updateLatestSymlink(filepath.Join(t.TempDir(), "binaries"), "../escape"); err == nil {
		t.Fatal("unsafe release version was accepted")
	}
	if err := syncArtifacts(t.TempDir(), t.TempDir(), "test", "host", ""); err == nil {
		t.Fatal("empty sync destination was accepted")
	}
	if err := runDockerShellScript(t.TempDir(), "linux/amd64", "missing", "true", nil); err == nil {
		t.Fatal("missing Docker shell execution was hidden")
	}
}
