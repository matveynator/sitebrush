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
	if len(targets) != 12 {
		t.Fatalf("expected 12 server-app targets, got %d", len(targets))
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

func TestLinuxDesktopDockerImage(t *testing.T) {
	t.Parallel()

	if got, want := linuxDesktopDockerImage("gtk40"), "ubuntu:22.04"; got != want {
		t.Fatalf("linuxDesktopDockerImage(gtk40) = %q, want %q", got, want)
	}
	if got, want := linuxDesktopDockerImage("gtk41"), "ubuntu:24.04"; got != want {
		t.Fatalf("linuxDesktopDockerImage(gtk41) = %q, want %q", got, want)
	}
}

func TestLinuxDesktopDockerScript(t *testing.T) {
	t.Parallel()

	script := linuxDesktopDockerScript("/work/dist/sitebrush_linux_amd64_desktop_gtk40", "sitebrush_linux_amd64_desktop_gtk40", "amd64", "gtk40", "151")
	if !strings.Contains(script, "libwebkit2gtk-4.0-dev") {
		t.Fatalf("linuxDesktopDockerScript() missing gtk40 webkit package")
	}
	if !strings.Contains(script, "go1.25.0.linux-amd64.tar.gz") {
		t.Fatalf("linuxDesktopDockerScript() missing Go tarball URL")
	}
	if !strings.Contains(script, "sitebrush_linux_amd64_desktop_gtk40.zip") {
		t.Fatalf("linuxDesktopDockerScript() missing zip target")
	}
}

func TestWindowsDesktopDockerScript(t *testing.T) {
	t.Parallel()

	script := windowsDesktopDockerScript("/work/dist/sitebrush_windows_amd64_desktop.exe", "sitebrush_windows_amd64_desktop.exe", "amd64", "151")
	if !strings.Contains(script, "mingw-w64") {
		t.Fatalf("windowsDesktopDockerScript() missing mingw package")
	}
	if !strings.Contains(script, "go1.25.0.linux-amd64.tar.gz") {
		t.Fatalf("windowsDesktopDockerScript() missing Go tarball URL")
	}
	if !strings.Contains(script, "x86_64-w64-mingw32-gcc") {
		t.Fatalf("windowsDesktopDockerScript() missing amd64 cross compiler")
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
