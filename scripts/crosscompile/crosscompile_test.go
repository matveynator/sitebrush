package main

import (
	"flag"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestServerAppTargets(t *testing.T) {
	t.Parallel()

	targets := serverAppTargets()
	if len(targets) != 13 {
		t.Fatalf("expected 13 server-app targets, got %d", len(targets))
	}

	if targets[0].goos != "linux" || targets[0].goarch != "amd64" {
		t.Fatalf("unexpected first target: %+v", targets[0])
	}
	if targets[len(targets)-1].goos != "windows" || targets[len(targets)-1].goarch != "arm64" {
		t.Fatalf("unexpected last target: %+v", targets[len(targets)-1])
	}
	foundNetBSD := false
	for _, target := range targets {
		if target.goos == "netbsd" && target.goarch == "amd64" {
			foundNetBSD = true
			break
		}
	}
	if !foundNetBSD {
		t.Fatal("server-app targets should include NetBSD")
	}
}

func TestFilteredServerAppTargets(t *testing.T) {
	t.Parallel()

	targets := filteredServerAppTargets(serverAppTargets(), buildTargetFilter{goos: "linux", goarch: "amd64"})
	if len(targets) != 1 {
		t.Fatalf("filtered targets = %d, want 1: %#v", len(targets), targets)
	}
	if targets[0].goos != "linux" || targets[0].goarch != "amd64" {
		t.Fatalf("filtered target = %#v, want linux/amd64", targets[0])
	}
}

func TestFilteredServerAppTargetsCanSelectOneOS(t *testing.T) {
	t.Parallel()

	targets := filteredServerAppTargets(serverAppTargets(), buildTargetFilter{goos: "openbsd"})
	if len(targets) != 2 {
		t.Fatalf("filtered targets = %d, want openbsd amd64 and arm64: %#v", len(targets), targets)
	}
	for _, target := range targets {
		if target.goos != "openbsd" {
			t.Fatalf("filtered target = %#v, want only openbsd", target)
		}
	}
}

func TestEffectiveBuildModeUsesServerAppForImplicitFilteredBuilds(t *testing.T) {
	t.Parallel()

	got := effectiveBuildMode(modeAll, buildTargetFilter{goos: "linux", goarch: "amd64"}, false)
	if got != modeServerApp {
		t.Fatalf("effectiveBuildMode() = %q, want %q", got, modeServerApp)
	}
}

func TestEffectiveBuildModeKeepsExplicitAll(t *testing.T) {
	t.Parallel()

	got := effectiveBuildMode(modeAll, buildTargetFilter{goos: "linux", goarch: "amd64"}, true)
	if got != modeAll {
		t.Fatalf("effectiveBuildMode() = %q, want %q", got, modeAll)
	}
}

func TestServerAppArtifactName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		programName string
		goos        string
		goarch      string
		want        string
	}{
		{name: "linux", programName: "sitebrush", goos: "linux", goarch: "amd64", want: "sitebrush_linux_amd64"},
		{name: "windows", programName: "sitebrush", goos: "windows", goarch: "arm64", want: "sitebrush_windows_arm64.exe"},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			got := serverAppArtifactName(testCase.programName, testCase.goos, testCase.goarch)
			if got != testCase.want {
				t.Fatalf("serverAppArtifactName() = %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestDesktopArtifactName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		programName string
		goos        string
		goarch      string
		variant     string
		want        string
	}{
		{name: "linux gtk40", programName: "sitebrush", goos: "linux", goarch: "amd64", variant: "gtk40", want: "sitebrush_linux_amd64_desktop_gtk40"},
		{name: "darwin universal", programName: "sitebrush", goos: "darwin", goarch: "universal", want: "sitebrush_darwin_universal_desktop"},
		{name: "windows", programName: "sitebrush", goos: "windows", goarch: "amd64", want: "sitebrush_windows_amd64_desktop.exe"},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			got := desktopArtifactName(testCase.programName, testCase.goos, testCase.goarch, testCase.variant)
			if got != testCase.want {
				t.Fatalf("desktopArtifactName() = %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestLinuxDesktopBaseImage(t *testing.T) {
	t.Parallel()

	if got, want := linuxDesktopBaseImage("gtk40"), "ubuntu:22.04"; got != want {
		t.Fatalf("linuxDesktopBaseImage(gtk40) = %q, want %q", got, want)
	}
	if got, want := linuxDesktopBaseImage("gtk41"), "ubuntu:24.04"; got != want {
		t.Fatalf("linuxDesktopBaseImage(gtk41) = %q, want %q", got, want)
	}
}

func TestLinuxDesktopDockerImage(t *testing.T) {
	t.Parallel()

	got := linuxDesktopDockerImage("amd64", "gtk40")
	if want := "sitebrush-crosscompile:linux-amd64-gtk40-go1.26.6-v1"; got != want {
		t.Fatalf("linuxDesktopDockerImage() = %q, want %q", got, want)
	}
}

func TestLinuxDesktopDockerfile(t *testing.T) {
	t.Parallel()

	dockerfile := linuxDesktopDockerfile("amd64", "gtk40")
	if !strings.Contains(dockerfile, "FROM --platform=linux/amd64 ubuntu:22.04") {
		t.Fatalf("linuxDesktopDockerfile() missing base image")
	}
	if !strings.Contains(dockerfile, `SHELL ["/bin/bash", "-o", "pipefail", "-c"]`) {
		t.Fatalf("linuxDesktopDockerfile() missing pipefail shell")
	}
	if !strings.Contains(dockerfile, "libwebkit2gtk-4.0-dev") {
		t.Fatalf("linuxDesktopDockerfile() missing gtk40 webkit package")
	}
	if !strings.Contains(dockerfile, "go1.26.6.linux-amd64.tar.gz") {
		t.Fatalf("linuxDesktopDockerfile() missing Go tarball URL")
	}
}

func TestLinuxDesktopDockerScript(t *testing.T) {
	t.Parallel()

	script := linuxDesktopDockerScript("/work/dist/sitebrush_linux_amd64_desktop_gtk40", "sitebrush_linux_amd64_desktop_gtk40", "amd64", "gtk40", "151")
	if !strings.Contains(script, "sitebrush_linux_amd64_desktop_gtk40.zip") {
		t.Fatalf("linuxDesktopDockerScript() missing zip target")
	}
	if strings.Contains(script, "apt-get") || strings.Contains(script, "go.dev/dl") {
		t.Fatalf("linuxDesktopDockerScript() should use cached builder image dependencies")
	}
}

func TestWindowsDesktopDockerImage(t *testing.T) {
	t.Parallel()

	got := windowsDesktopDockerImage("arm64")
	if want := "sitebrush-crosscompile:windows-arm64-go1.26.6-v1"; got != want {
		t.Fatalf("windowsDesktopDockerImage() = %q, want %q", got, want)
	}
}

func TestWindowsDesktopDockerfile(t *testing.T) {
	t.Parallel()

	dockerfile := windowsDesktopDockerfile("amd64")
	if !strings.Contains(dockerfile, "mingw-w64") {
		t.Fatalf("windowsDesktopDockerfile() missing mingw package")
	}
	if !strings.Contains(dockerfile, `SHELL ["/bin/bash", "-o", "pipefail", "-c"]`) {
		t.Fatalf("windowsDesktopDockerfile() missing pipefail shell")
	}
	if !strings.Contains(dockerfile, "go1.26.6.linux-amd64.tar.gz") {
		t.Fatalf("windowsDesktopDockerfile() missing Go tarball URL")
	}
	if !strings.Contains(dockerfile, "go install github.com/akavel/rsrc@latest") {
		t.Fatalf("windowsDesktopDockerfile() missing rsrc install")
	}
}

func TestWindowsArm64DesktopDockerfile(t *testing.T) {
	t.Parallel()

	dockerfile := windowsDesktopDockerfile("arm64")
	if !strings.Contains(dockerfile, `LLVM_MINGW_VERSION="20260505"`) {
		t.Fatalf("windowsDesktopDockerfile() missing llvm-mingw archive")
	}
	if strings.Contains(dockerfile, "\nLLVM_MINGW_VERSION=") {
		t.Fatalf("windowsDesktopDockerfile() emits shell assignment as Dockerfile instruction")
	}
	if !strings.Contains(dockerfile, "aarch64-w64-mingw32-gcc") {
		t.Fatalf("windowsDesktopDockerfile() missing arm64 cross compiler")
	}
}

func TestWindowsDesktopDockerScript(t *testing.T) {
	t.Parallel()

	script := windowsDesktopDockerScript("/work/dist/sitebrush_windows_amd64_desktop.exe", "sitebrush_windows_amd64_desktop.exe", "amd64", "151")
	if !strings.Contains(script, "x86_64-w64-mingw32-gcc") {
		t.Fatalf("windowsDesktopDockerScript() missing amd64 cross compiler")
	}
	if !strings.Contains(script, `trap 'rm -f "zz_sitebrush_icon_windows_amd64.syso"' EXIT`) {
		t.Fatalf("windowsDesktopDockerScript() does not clean generated syso")
	}
	if strings.Contains(script, "apt-get") || strings.Contains(script, "go.dev/dl") || strings.Contains(script, "go install") {
		t.Fatalf("windowsDesktopDockerScript() should use cached builder image dependencies")
	}
}

func TestDockerVolumeName(t *testing.T) {
	t.Parallel()

	got := dockerVolumeName("sitebrush-crosscompile:linux-amd64-gtk40-go1.26.6-v1", "gomod")
	want := "sitebrush-crosscompile-linux-amd64-gtk40-go1.26.6-v1-gomod"
	if got != want {
		t.Fatalf("dockerVolumeName() = %q, want %q", got, want)
	}
}

func TestNormalizeDockerArchitecture(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		architecture string
		want         string
	}{
		{architecture: "x86_64", want: "amd64"},
		{architecture: "aarch64", want: "arm64"},
		{architecture: "amd64", want: "amd64"},
		{architecture: "arm64", want: "arm64"},
	}
	for _, testCase := range testCases {
		if got := normalizeDockerArchitecture(testCase.architecture); got != testCase.want {
			t.Fatalf("normalizeDockerArchitecture(%q) = %q, want %q", testCase.architecture, got, testCase.want)
		}
	}
}

func TestDockerPlatformProbeArgs(t *testing.T) {
	t.Parallel()

	got := strings.Join(dockerPlatformProbeArgs("linux/arm64", "sitebrush-crosscompile:linux-arm64-gtk40-go1.26.6-v1"), " ")
	want := "run --rm --pull never --platform linux/arm64 sitebrush-crosscompile:linux-arm64-gtk40-go1.26.6-v1 true"
	if got != want {
		t.Fatalf("dockerPlatformProbeArgs() = %q, want %q", got, want)
	}
}

func TestDockerWorkspacePath(t *testing.T) {
	t.Parallel()

	repoRoot := filepath.Join(string(filepath.Separator), "Users", "matvey", "codex", "sitebrush")
	hostPath := filepath.Join(repoRoot, "binaries", "152", "desktop-app", "sitebrush_linux_amd64_desktop_gtk40")
	got, err := dockerWorkspacePath(repoRoot, hostPath)
	if err != nil {
		t.Fatalf("dockerWorkspacePath() error = %v", err)
	}
	if want := "/workspace/binaries/152/desktop-app/sitebrush_linux_amd64_desktop_gtk40"; got != want {
		t.Fatalf("dockerWorkspacePath() = %q, want %q", got, want)
	}
}

func TestDockerWorkspacePathRejectsOutsidePath(t *testing.T) {
	t.Parallel()

	repoRoot := filepath.Join(string(filepath.Separator), "Users", "matvey", "codex", "sitebrush")
	hostPath := filepath.Join(string(filepath.Separator), "Users", "matvey", "codex", "other", "binary")
	if _, err := dockerWorkspacePath(repoRoot, hostPath); err == nil {
		t.Fatalf("dockerWorkspacePath() accepted path outside repo")
	}
}

func TestVerifyBuiltDesktopArtifact(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	artifactPath := filepath.Join(dir, "sitebrush_linux_amd64_desktop_gtk40")
	if err := os.WriteFile(artifactPath+".zip", []byte("zip"), 0o644); err != nil {
		t.Fatalf("write zip: %v", err)
	}
	if err := verifyBuiltDesktopArtifact(artifactPath); err != nil {
		t.Fatalf("verifyBuiltDesktopArtifact() error = %v", err)
	}
}

func TestVerifyBuiltDesktopArtifactRejectsMissingZip(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	artifactPath := filepath.Join(dir, "sitebrush_linux_amd64_desktop_gtk40")
	if err := verifyBuiltDesktopArtifact(artifactPath); err == nil {
		t.Fatalf("verifyBuiltDesktopArtifact() accepted missing zip")
	}
}

func TestCleanupDesktopBuildIntermediates(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sitebrush_linux_amd64_desktop_gtk40"), []byte("binary"), 0o644); err != nil {
		t.Fatalf("write raw binary: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sitebrush_linux_amd64_desktop_gtk40.zip"), []byte("zip"), 0o644); err != nil {
		t.Fatalf("write zip: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sitebrush_darwin_universal_desktop.dmg"), []byte("dmg"), 0o644); err != nil {
		t.Fatalf("write dmg: %v", err)
	}
	appDir := filepath.Join(dir, "sitebrush.app")
	if err := os.MkdirAll(filepath.Join(appDir, "Contents", "MacOS"), 0o755); err != nil {
		t.Fatalf("create app bundle: %v", err)
	}
	if err := os.WriteFile(filepath.Join(appDir, "Contents", "MacOS", "sitebrush"), []byte("app"), 0o755); err != nil {
		t.Fatalf("write app bundle binary: %v", err)
	}

	if err := cleanupDesktopBuildIntermediates(dir); err != nil {
		t.Fatalf("cleanupDesktopBuildIntermediates() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "sitebrush_linux_amd64_desktop_gtk40")); !os.IsNotExist(err) {
		t.Fatalf("expected raw desktop artifact to be removed, err=%v", err)
	}
	if _, err := os.Stat(appDir); !os.IsNotExist(err) {
		t.Fatalf("expected desktop app bundle to be removed, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "sitebrush_linux_amd64_desktop_gtk40.zip")); err != nil {
		t.Fatalf("expected desktop zip to stay, err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "sitebrush_darwin_universal_desktop.dmg")); err != nil {
		t.Fatalf("expected desktop dmg to stay, err=%v", err)
	}
}

func TestWriteMD5SumsFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sitebrush_linux_amd64"), []byte("server"), 0o644); err != nil {
		t.Fatalf("write server artifact: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sitebrush_linux_amd64.zip"), []byte("desktop"), 0o644); err != nil {
		t.Fatalf("write desktop artifact: %v", err)
	}

	if err := writeMD5SumsFile(dir); err != nil {
		t.Fatalf("writeMD5SumsFile() error = %v", err)
	}

	content, err := os.ReadFile(filepath.Join(dir, "MD5SUMS"))
	if err != nil {
		t.Fatalf("read MD5SUMS: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 checksum lines, got %d: %q", len(lines), string(content))
	}
	if !strings.Contains(lines[0], "sitebrush_linux_amd64") {
		t.Fatalf("missing checksum line for server artifact: %q", lines[0])
	}
	if !strings.Contains(lines[1], "sitebrush_linux_amd64.zip") {
		t.Fatalf("missing checksum line for desktop artifact: %q", lines[1])
	}
}

func TestSanitizePathSegment(t *testing.T) {
	t.Parallel()

	got := sanitizePathSegment(`../v1.2.3 release /beta`)
	want := "v1.2.3_release__beta"
	if got != want {
		t.Fatalf("sanitizePathSegment() = %q, want %q", got, want)
	}
}

func TestShellQuote(t *testing.T) {
	t.Parallel()

	if got, want := shellQuote("plain"), "'plain'"; got != want {
		t.Fatalf("shellQuote() = %q, want %q", got, want)
	}
	if got, want := shellQuote("a'b"), "'a'\"'\"'b'"; got != want {
		t.Fatalf("shellQuote() = %q, want %q", got, want)
	}
}

func TestPSQuote(t *testing.T) {
	t.Parallel()

	if got, want := psQuote("plain"), "'plain'"; got != want {
		t.Fatalf("psQuote() = %q, want %q", got, want)
	}
	if got, want := psQuote("a'b"), "'a''b'"; got != want {
		t.Fatalf("psQuote() = %q, want %q", got, want)
	}
}

func TestUpdateLatestSymlink(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("symlink test is skipped on Windows")
	}

	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "binaries"), 0o755); err != nil {
		t.Fatalf("mkdir binaries: %v", err)
	}

	if err := updateLatestSymlink(filepath.Join(root, "binaries"), "123"); err != nil {
		t.Fatalf("updateLatestSymlink() error = %v", err)
	}

	linkPath := filepath.Join(root, "binaries", "latest")
	target, err := os.Readlink(linkPath)
	if err != nil {
		t.Fatalf("Readlink() error = %v", err)
	}
	if target != "123" {
		t.Fatalf("latest symlink = %q, want %q", target, "123")
	}

	if err := updateLatestSymlink(filepath.Join(root, "binaries"), "124"); err != nil {
		t.Fatalf("second updateLatestSymlink() error = %v", err)
	}
	target, err = os.Readlink(linkPath)
	if err != nil {
		t.Fatalf("Readlink() error after second update = %v", err)
	}
	if target != "124" {
		t.Fatalf("latest symlink after update = %q, want %q", target, "124")
	}
}

func TestRemoteSyncDirectoryIsExactDestination(t *testing.T) {
	t.Parallel()

	if got, want := remoteSyncDirectory("/var/lib/sitebrush/storage/chroot/sitebrush.com/download/"), "/var/lib/sitebrush/storage/chroot/sitebrush.com/download"; got != want {
		t.Fatalf("remoteSyncDirectory() = %q, want %q", got, want)
	}
	if got, want := remoteSyncDirectory("  /srv/releases/sitebrush  "), "/srv/releases/sitebrush"; got != want {
		t.Fatalf("remoteSyncDirectory() changed exact destination: got %q, want %q", got, want)
	}
	if got, want := remoteSyncDirectory("/"), "/"; got != want {
		t.Fatalf("remoteSyncDirectory() root path = %q, want %q", got, want)
	}
}

func TestRsyncDirectoriesCopyContentsIntoExactDestination(t *testing.T) {
	t.Parallel()

	if got, want := rsyncSourceDirectory(filepath.Join("repo", "binaries")), filepath.Join("repo", "binaries")+string(filepath.Separator); got != want {
		t.Fatalf("rsyncSourceDirectory() = %q, want %q", got, want)
	}
	if got, want := rsyncRemoteDirectory("root@sitebrush.com", "/var/lib/sitebrush/storage/chroot/sitebrush.com/download"), "root@sitebrush.com:/var/lib/sitebrush/storage/chroot/sitebrush.com/download/"; got != want {
		t.Fatalf("rsyncRemoteDirectory() = %q, want %q", got, want)
	}
}

func TestParseSyncDestination(t *testing.T) {
	t.Parallel()

	destination, err := parseSyncDestination(" root@sitebrush.com = /var/lib/sitebrush/storage/chroot/sitebrush.com/download/ ")
	if err != nil {
		t.Fatalf("parseSyncDestination() error = %v", err)
	}
	if destination.host != "root@sitebrush.com" {
		t.Fatalf("sync target host = %q, want root@sitebrush.com", destination.host)
	}
	if destination.base != "/var/lib/sitebrush/storage/chroot/sitebrush.com/download/" {
		t.Fatalf("sync target base = %q, want remote download directory", destination.base)
	}
}

func TestSyncPublicationDestinationsIncludesRepeatedTargets(t *testing.T) {
	t.Parallel()

	targets := syncDestinationFlags{}
	if err := targets.Set("root@sitebrush.com=/var/lib/sitebrush/storage/chroot/sitebrush.com/download/"); err != nil {
		t.Fatalf("first target Set() error = %v", err)
	}
	if err := targets.Set("root@sitebrush.ru=/var/lib/sitebrush/storage/chroot/sitebrush.ru/download/"); err != nil {
		t.Fatalf("second target Set() error = %v", err)
	}

	destinations := syncPublicationDestinations(targets)
	if len(destinations) != 2 {
		t.Fatalf("syncPublicationDestinations() returned %d targets, want 2", len(destinations))
	}
	if destinations[0].host != "root@sitebrush.com" || destinations[1].host != "root@sitebrush.ru" {
		t.Fatalf("unexpected destinations: %+v", destinations)
	}
}

func TestDefaultVersionLabelUsesEnvironment(t *testing.T) {
	t.Setenv("GITHUB_RUN_NUMBER", "987")
	got := defaultVersionLabel(t.TempDir())
	if got != "987" {
		t.Fatalf("defaultVersionLabel() = %q, want %q", got, "987")
	}
}

func TestFindRepoRoot(t *testing.T) {
	t.Parallel()

	root, err := findRepoRoot()
	if err != nil {
		t.Fatalf("findRepoRoot() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("repo root does not contain go.mod: %v", err)
	}
	if filepath.Base(root) != "sitebrush" {
		t.Fatalf("unexpected repo root base: %s", filepath.Base(root))
	}
	if runtime.GOOS == "windows" {
		t.Skip("symlink behavior is verified on POSIX systems only")
	}
}

func TestBuildServerArtifactsWithStubCompiler(t *testing.T) {
	binDir := t.TempDir()
	stubGo := filepath.Join(binDir, "go")
	if err := os.WriteFile(stubGo, []byte("#!/bin/sh\nwhile [ \"$#\" -gt 0 ]; do\n  if [ \"$1\" = \"-o\" ]; then shift; printf binary > \"$1\"; fi\n  shift\ndone\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
	outputDir := t.TempDir()
	if err := buildServerAppArtifacts(t.TempDir(), outputDir, "sitebrush", "test", buildTargetFilter{goos: "linux", goarch: "amd64"}); err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(outputDir, "server-app", "sitebrush_linux_amd64")
	if err := verifyNonEmptyFile(artifact); err != nil {
		t.Fatal(err)
	}
	if err := verifyNonEmptyFile(filepath.Join(outputDir, "server-app", "MD5SUMS")); err != nil {
		t.Fatal(err)
	}
	if err := buildServerAppArtifacts(t.TempDir(), t.TempDir(), "sitebrush", "test", buildTargetFilter{goos: "haiku"}); err == nil {
		t.Fatal("empty target set succeeded")
	}
}

func TestDesktopBuildTargetSelectionErrors(t *testing.T) {
	filtered := buildTargetFilter{goos: "freebsd"}
	if built, err := buildLinuxDesktopArtifacts(t.TempDir(), t.TempDir(), "sitebrush", "test", desktopBuildOptions{targetFilter: filtered}); err != nil || built {
		t.Fatalf("non-Linux target = %v, %v", built, err)
	}
	if built, err := buildWindowsDesktopArtifacts(t.TempDir(), t.TempDir(), "sitebrush", "test", desktopBuildOptions{targetFilter: filtered}); err != nil || built {
		t.Fatalf("non-Windows target = %v, %v", built, err)
	}
	if err := buildDesktopAppArtifacts(t.TempDir(), t.TempDir(), "sitebrush", "test", desktopBuildOptions{targetFilter: filtered}); err == nil {
		t.Fatal("unsupported target set succeeded")
	}
	if matches := (buildTargetFilter{goos: "windows"}).matchesOS("linux"); matches {
		t.Fatal("OS filter unexpectedly matched")
	}
	if got := (buildTargetFilter{goos: "windows", goarch: "arm64"}).label(); got != "os=windows arch=arm64" {
		t.Fatalf("filter label=%q", got)
	}
	if got := (buildTargetFilter{}).label(); got != "all targets" {
		t.Fatalf("empty filter label=%q", got)
	}
}

func TestSyncDestinationFlagString(t *testing.T) {
	var noTargets *syncDestinationFlags
	if noTargets.String() != "" {
		t.Fatal("nil sync target string should be empty")
	}
	targets := syncDestinationFlags{{host: "one", base: "/a"}, {host: "two", base: "/b"}}
	if got := targets.String(); got != "one=/a,two=/b" {
		t.Fatalf("sync target string=%q", got)
	}
}

func TestDesktopArtifactBuildOrchestrationWithStubCommands(t *testing.T) {
	commandDirectory := t.TempDir()
	writeCommandStub(t, commandDirectory, "go", `last=""; previous=""; for argument in "$@"; do if [ "$previous" = "-o" ]; then last="$argument"; fi; previous="$argument"; done; printf binary > "$last"`)
	writeCommandStub(t, commandDirectory, "docker", `exit 0`)
	writeCommandStub(t, commandDirectory, "lipo", `last=""; previous=""; for argument in "$@"; do if [ "$previous" = "-output" ]; then last="$argument"; fi; previous="$argument"; done; if [ -n "$last" ]; then printf binary > "$last"; fi`)
	writeCommandStub(t, commandDirectory, "cp", `if [ "$1" = "-R" ]; then exit 0; fi; last=""; for argument in "$@"; do last="$argument"; done; printf binary > "$last"`)
	writeCommandStub(t, commandDirectory, "sips", `previous=""; for argument in "$@"; do if [ "$previous" = "--out" ]; then printf icon > "$argument"; fi; previous="$argument"; done`)
	writeCommandStub(t, commandDirectory, "iconutil", `previous=""; for argument in "$@"; do if [ "$previous" = "-o" ]; then printf icon > "$argument"; fi; previous="$argument"; done`)
	writeCommandStub(t, commandDirectory, "hdiutil", `for argument in "$@"; do last="$argument"; done; printf dmg > "$last"`)
	writeCommandStub(t, commandDirectory, "ln", `exit 0`)
	t.Setenv("PATH", commandDirectory)

	root := t.TempDir()
	desktopDir := filepath.Join(root, "desktop")
	if err := os.MkdirAll(desktopDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := buildMacOSDesktopArtifacts(root, desktopDir, "sitebrush", "test"); err != nil {
		t.Fatal(err)
	}
	if err := verifyNonEmptyFile(filepath.Join(desktopDir, "sitebrush_darwin_universal_desktop.dmg")); err != nil {
		t.Fatal(err)
	}

	for _, target := range []struct{ os, arch, variant string }{{"linux", "amd64", "gtk40"}, {"windows", "arm64", ""}} {
		name := desktopArtifactName("sitebrush", target.os, target.arch, target.variant)
		if err := os.WriteFile(filepath.Join(desktopDir, name+".zip"), []byte("archive"), 0o600); err != nil {
			t.Fatal(err)
		}
		if target.os == "linux" {
			if err := buildLinuxDesktopArtifactInDocker(root, desktopDir, "sitebrush", "test", target.arch, target.variant, desktopBuildOptions{}); err != nil {
				t.Fatal(err)
			}
		} else if err := buildWindowsDesktopArtifactInDocker(root, desktopDir, "sitebrush", "test", target.arch, desktopBuildOptions{}); err != nil {
			t.Fatal(err)
		}
	}
}

func writeCommandStub(t *testing.T, directory, name, body string) {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\nset -eu\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
}


func TestSecurityBoundaryReleaseSyncRejectsOptionAndPathInjection(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{
		"-oProxyCommand=owned=/srv/releases",
		"root@host with-space=/srv/releases",
		"root@host=/srv/releases\nowned",
		"root@host=relative/path",
		"root@host=",
	} {
		if _, err := parseSyncDestination(raw); err == nil {
			t.Fatalf("SECURITY: unsafe release sync destination accepted: %q", raw)
		}
	}

	for _, host := range []string{"root@sitebrush.com", "sitebrush.ru", "192.0.2.10"} {
		if !validSyncHost(host) {
			t.Fatalf("valid sync host rejected: %q", host)
		}
	}
	for _, host := range []string{"-host", "host/name", "host:22", "host\nowned"} {
		if validSyncHost(host) {
			t.Fatalf("SECURITY: unsafe sync host accepted: %q", host)
		}
	}
}

func TestSecurityBoundaryLatestSymlinkRejectsTraversalTarget(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("symlink test is skipped on Windows")
	}

	root := t.TempDir()
	for _, version := range []string{"../owned", "../../tmp/owned", "/tmp/owned", "release/child", " "} {
		if err := updateLatestSymlink(root, version); err == nil {
			t.Fatalf("SECURITY: latest symlink accepted unsafe release target %q", version)
		}
	}
	if _, err := os.Lstat(filepath.Join(root, "latest")); !os.IsNotExist(err) {
		t.Fatalf("SECURITY: rejected version still created latest symlink: %v", err)
	}
}

func TestSecurityBoundaryArtifactChecksumChangesAfterBinaryTampering(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	artifact := filepath.Join(dir, "sitebrush_linux_amd64")
	if err := os.WriteFile(artifact, []byte("trusted-release"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeMD5SumsFile(dir); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(dir, "MD5SUMS"))
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(artifact, []byte("tampered-release"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeMD5SumsFile(dir); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(filepath.Join(dir, "MD5SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) == string(after) {
		t.Fatal("SECURITY: release checksum did not change after artifact tampering")
	}
}

func TestDesktopBuildWrappersWithStubDocker(t *testing.T) {
	commandDirectory := t.TempDir()
	writeCommandStub(t, commandDirectory, "docker", "exit 0")
	t.Setenv("PATH", commandDirectory)

	repoRoot := t.TempDir()
	desktopDir := filepath.Join(repoRoot, "desktop")
	if err := os.MkdirAll(desktopDir, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, variant := range []string{"gtk40", "gtk41"} {
		name := desktopArtifactName("sitebrush", "linux", "amd64", variant)
		if err := os.WriteFile(filepath.Join(desktopDir, name+".zip"), []byte("linux archive"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	built, err := buildLinuxDesktopArtifacts(
		repoRoot,
		desktopDir,
		"sitebrush",
		"test",
		desktopBuildOptions{targetFilter: buildTargetFilter{goos: "linux", goarch: "amd64"}},
	)
	if err != nil || !built {
		t.Fatalf("linux wrapper built=%v err=%v", built, err)
	}

	windowsName := desktopArtifactName("sitebrush", "windows", "amd64", "")
	if err := os.WriteFile(filepath.Join(desktopDir, windowsName+".zip"), []byte("windows archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	built, err = buildWindowsDesktopArtifacts(
		repoRoot,
		desktopDir,
		"sitebrush",
		"test",
		desktopBuildOptions{targetFilter: buildTargetFilter{goos: "windows", goarch: "amd64"}},
	)
	if err != nil || !built {
		t.Fatalf("windows wrapper built=%v err=%v", built, err)
	}
}

func TestDesktopBuildOrchestratorWithStubDocker(t *testing.T) {
	commandDirectory := t.TempDir()
	writeCommandStub(t, commandDirectory, "docker", "exit 0")
	t.Setenv("PATH", commandDirectory)

	repoRoot := t.TempDir()
	outputDir := filepath.Join(repoRoot, "output")
	desktopDir := filepath.Join(outputDir, "desktop-app")
	if err := os.MkdirAll(desktopDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, variant := range []string{"gtk40", "gtk41"} {
		name := desktopArtifactName("sitebrush", "linux", "amd64", variant)
		if err := os.WriteFile(filepath.Join(desktopDir, name+".zip"), []byte("archive"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	err := buildDesktopAppArtifacts(
		repoRoot,
		outputDir,
		"sitebrush",
		"test",
		desktopBuildOptions{targetFilter: buildTargetFilter{goos: "linux", goarch: "amd64"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyNonEmptyFile(filepath.Join(desktopDir, "MD5SUMS")); err != nil {
		t.Fatal(err)
	}
}


func TestCrosscompileMainServerOnlyWithStubCompiler(t *testing.T) {
	oldArgs := os.Args
	oldFlags := flag.CommandLine
	defer func() {
		os.Args = oldArgs
		flag.CommandLine = oldFlags
	}()

	repoRoot, err := findRepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	outputRoot, err := os.MkdirTemp(repoRoot, ".crosscompile-main-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(outputRoot)
	relativeOutput, err := filepath.Rel(repoRoot, outputRoot)
	if err != nil {
		t.Fatal(err)
	}

	commandDirectory := t.TempDir()
	writeCommandStub(t, commandDirectory, "go", `last=""; previous=""; for argument in "$@"; do if [ "$previous" = "-o" ]; then last="$argument"; fi; previous="$argument"; done; if [ -n "$last" ]; then mkdir -p "$(dirname "$last")"; printf binary > "$last"; fi`)
	originalPath := os.Getenv("PATH")
	t.Setenv("PATH", commandDirectory+string(os.PathListSeparator)+originalPath)

	flag.CommandLine = flag.NewFlagSet("crosscompile-test", flag.ContinueOnError)
	os.Args = []string{
		"crosscompile",
		"-mode", "server-app",
		"-os", "linux",
		"-arch", "amd64",
		"-version", "security-test",
		"-output-dir", relativeOutput,
	}
	main()

	artifact := filepath.Join(outputRoot, "security-test", "server-app", "sitebrush_linux_amd64")
	if err := verifyNonEmptyFile(artifact); err != nil {
		t.Fatalf("main did not produce server artifact: %v", err)
	}
	latest, err := os.Readlink(filepath.Join(outputRoot, "latest"))
	if err != nil {
		t.Fatal(err)
	}
	if latest != "security-test" {
		t.Fatalf("latest target=%q", latest)
	}
}

func TestSyncArtifactsUsesOnlyValidatedDestinationWithStubCommands(t *testing.T) {
	repoRoot := t.TempDir()
	outputRoot := filepath.Join(repoRoot, "binaries")
	if err := os.MkdirAll(outputRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	commandDirectory := t.TempDir()
	logPath := filepath.Join(commandDirectory, "commands.log")
	stub := `printf '%s
' "$0 $*" >> "$COMMAND_LOG"`
	writeCommandStub(t, commandDirectory, "ssh", stub)
	writeCommandStub(t, commandDirectory, "rsync", stub)
	t.Setenv("PATH", commandDirectory)
	t.Setenv("COMMAND_LOG", logPath)

	destination, err := parseSyncDestination("root@sitebrush.com=/srv/sitebrush/releases/")
	if err != nil {
		t.Fatal(err)
	}
	if err := syncArtifacts(repoRoot, outputRoot, "123", destination.host, destination.base); err != nil {
		t.Fatal(err)
	}
	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(logged)
	if !strings.Contains(text, "ssh root@sitebrush.com") || !strings.Contains(text, "rsync -avP") {
		t.Fatalf("sync commands=%q", text)
	}
}

func TestCrosscompileVersionAndPlatformHelperFallbacks(t *testing.T) {
	commandDirectory := t.TempDir()
	writeCommandStub(t, commandDirectory, "git", `if [ "$1" = "rev-list" ]; then exit 0; fi; if [ "$1" = "describe" ]; then printf 'release-test
'; exit 0; fi; exit 1`)
	t.Setenv("PATH", commandDirectory)
	t.Setenv("GITHUB_RUN_NUMBER", "")
	if got := defaultVersionLabel(t.TempDir()); got != "release-test" {
		t.Fatalf("defaultVersionLabel fallback=%q", got)
	}

	writeCommandStub(t, commandDirectory, "pkg-config", `if [ "$2" = "webkit2gtk-4.0" ]; then exit 0; fi; exit 1`)
	if variant, ok := linuxDesktopVariant(); !ok || variant != "gtk40" {
		t.Fatalf("linux desktop variant=%q ok=%v", variant, ok)
	}

	writeCommandStub(t, commandDirectory, "docker", `if [ "$1" = "info" ]; then printf 'x86_64\n'; exit 0; fi; exit 1`)
	platform, err := dockerNativePlatform(t.TempDir())
	if err != nil || platform != "linux/amd64" {
		t.Fatalf("docker native platform=%q err=%v", platform, err)
	}
}
