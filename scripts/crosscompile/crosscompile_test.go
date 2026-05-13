package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestServerAppTargets(t *testing.T) {
	t.Parallel()

	targets := serverAppTargets()
	if len(targets) != 11 {
		t.Fatalf("expected 11 server-app targets, got %d", len(targets))
	}

	if targets[0].goos != "linux" || targets[0].goarch != "amd64" {
		t.Fatalf("unexpected first target: %+v", targets[0])
	}
	if targets[len(targets)-1].goos != "windows" || targets[len(targets)-1].goarch != "arm64" {
		t.Fatalf("unexpected last target: %+v", targets[len(targets)-1])
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
	if want := "sitebrush-crosscompile:linux-amd64-gtk40-go1.25.0-v1"; got != want {
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
	if !strings.Contains(dockerfile, "go1.25.0.linux-amd64.tar.gz") {
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
	if want := "sitebrush-crosscompile:windows-arm64-go1.25.0-v1"; got != want {
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
	if !strings.Contains(dockerfile, "go1.25.0.linux-amd64.tar.gz") {
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

	got := dockerVolumeName("sitebrush-crosscompile:linux-amd64-gtk40-go1.25.0-v1", "gomod")
	want := "sitebrush-crosscompile-linux-amd64-gtk40-go1.25.0-v1-gomod"
	if got != want {
		t.Fatalf("dockerVolumeName() = %q, want %q", got, want)
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
	if err := os.WriteFile(artifactPath, []byte("binary"), 0o644); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
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
	if err := os.WriteFile(artifactPath, []byte("binary"), 0o644); err != nil {
		t.Fatalf("write artifact: %v", err)
	}
	if err := verifyBuiltDesktopArtifact(artifactPath); err == nil {
		t.Fatalf("verifyBuiltDesktopArtifact() accepted missing zip")
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
