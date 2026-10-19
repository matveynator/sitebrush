package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"debug/macho"
	"encoding/binary"
	"flag"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMacOSSDKArchiveIsStableAndPreservesSymlinks(t *testing.T) {
	directory := t.TempDir()
	sdk := filepath.Join(directory, "MacOSX14.4.sdk")
	if err := os.Mkdir(sdk, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sdk, "header.h"), []byte("header"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("header.h", filepath.Join(sdk, "current.h")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	first, second := filepath.Join(directory, "first.tar.gz"), filepath.Join(directory, "second.tar.gz")
	if err := archiveMacOSSDK(sdk, first); err != nil {
		t.Fatal(err)
	}
	if err := archiveMacOSSDK(sdk, second); err != nil {
		t.Fatal(err)
	}
	firstBytes, err := os.ReadFile(first)
	if err != nil {
		t.Fatal(err)
	}
	secondBytes, err := os.ReadFile(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstBytes, secondBytes) {
		t.Fatal("unchanged SDK produced a different Docker cache key")
	}
	compressed, err := gzip.NewReader(bytes.NewReader(firstBytes))
	if err != nil {
		t.Fatal(err)
	}
	defer compressed.Close()
	reader := tar.NewReader(compressed)
	for {
		header, err := reader.Next()
		if err != nil {
			t.Fatalf("SDK symlink was not preserved: %v", err)
		}
		if header.Name == "MacOSX14.4.sdk/current.h" {
			if header.Typeflag != tar.TypeSymlink || header.Linkname != "header.h" {
				t.Fatalf("invalid SDK link: %+v", header)
			}
			break
		}
	}
}

func TestOSXCrossSDKPreparationNamesArchiveFromSDKSettings(t *testing.T) {
	directory := t.TempDir()
	sdk := filepath.Join(directory, "MacOSX.sdk")
	if err := os.Mkdir(sdk, 0o700); err != nil {
		t.Fatal(err)
	}
	settings := []byte(`<?xml version="1.0" encoding="UTF-8"?><plist version="1.0"><dict><key>Version</key><string>14.4</string></dict></plist>`)
	if err := os.WriteFile(filepath.Join(sdk, "SDKSettings.plist"), settings, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("SDKSettings.plist", filepath.Join(sdk, "settings-link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	archivePath := filepath.Join(directory, "runner-sdk.tar.gz")
	if err := archiveMacOSSDK(sdk, archivePath); err != nil {
		t.Fatal(err)
	}
	tarballs := filepath.Join(directory, "tarballs")
	command := exec.Command("bash", "-s", "--", archivePath, filepath.Join(directory, "staging"), tarballs)
	command.Stdin = strings.NewReader(prepareOSXCrossSDKScript)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("prepare OSXCross SDK: %v\n%s", err, output)
	}
	prepared, err := os.Open(filepath.Join(tarballs, "MacOSX14.4.sdk.tar.gz"))
	if err != nil {
		t.Fatal(err)
	}
	defer prepared.Close()
	compressed, err := gzip.NewReader(prepared)
	if err != nil {
		t.Fatal(err)
	}
	defer compressed.Close()
	reader := tar.NewReader(compressed)
	foundSettings, foundLink := false, false
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		switch header.Name {
		case "MacOSX14.4.sdk/SDKSettings.plist":
			foundSettings = true
		case "MacOSX14.4.sdk/settings-link":
			foundLink = header.Typeflag == tar.TypeSymlink && header.Linkname == "SDKSettings.plist"
		}
	}
	if !foundSettings || !foundLink {
		t.Fatalf("prepared SDK missing settings or symlink: settings=%v link=%v", foundSettings, foundLink)
	}
}

func TestProjectGoVersionRequiresPinnedRelease(t *testing.T) {
	for _, fixture := range []struct{ directive, expected string }{
		{"go 1.27.2\n", "1.27.2"},
		{"module example\n\ngo 1.28.3 // patch\n", "1.28.3"},
		{"go 1.27\n", ""}, {"go 1.27.2rc1\n", ""}, {"go 1.27.;echo\n", ""}, {"module example\n", ""},
	} {
		directory := t.TempDir()
		if err := os.WriteFile(filepath.Join(directory, "go.mod"), []byte(fixture.directive), 0o600); err != nil {
			t.Fatal(err)
		}
		version, err := projectGoVersion(directory)
		if fixture.expected == "" && err == nil {
			t.Fatalf("accepted %q", fixture.directive)
		}
		if fixture.expected != "" && (err != nil || version != fixture.expected) {
			t.Fatalf("version=%s, err=%v", version, err)
		}
	}
}

func TestMacOSStagesRejectPartialPublicationAndHostCGO(t *testing.T) {
	for _, stage := range []string{"build", "package"} {
		if err := validateMacOSStage(stage, modeDesktopApp, buildTargetFilter{goos: "darwin", goarch: "universal"}); err != nil {
			t.Fatal(err)
		}
		if err := validateMacOSStage(stage, modeAll, buildTargetFilter{}); err == nil {
			t.Fatal("partial stage accepted for a complete release")
		}
		if err := validateMacOSStage(stage, modeDesktopApp, buildTargetFilter{goos: "linux"}); err == nil {
			t.Fatal("macOS stage accepted for Linux")
		}
	}
	if err := validateMacOSStage("unknown", modeDesktopApp, buildTargetFilter{goos: "darwin"}); err == nil {
		t.Fatal("unknown stage accepted")
	}
	if err := buildGoBinary(t.TempDir(), "unused", buildRequest{cgoEnabled: "1"}); err == nil {
		t.Fatal("host CGO compilation accepted")
	}
}

func TestMacOSDockerBuildPinsToolchainAndCompilesBothArchitectures(t *testing.T) {
	dockerfile := macOSDesktopDockerfile("arm64", "1.28.3")
	for _, fragment := range []string{"go1.28.3.linux-arm64.tar.gz", osxcrossCommit, "BUILD_FLAVOR=llvm", "ENABLE_ARCHS=\"x86_64 arm64\"", "OSX_VERSION_MIN=13.0", "GOTOOLCHAIN=local"} {
		if !strings.Contains(dockerfile, fragment) {
			t.Fatalf("builder missing %s", fragment)
		}
	}
	script := macOSDesktopDockerScript("/workspace/binaries/check/desktop-app", "sitebrush", "123")
	for _, fragment := range []string{"for target in amd64 arm64", "CGO_ENABLED=1 GOOS=darwin", "MACOSX_DEPLOYMENT_TARGET=13.0", "main.CompileVersion=123", "lipo -create", "-verify_arch x86_64 arm64"} {
		if !strings.Contains(script, fragment) {
			t.Fatalf("compilation missing %s", fragment)
		}
	}
}

func writeUniversalFixture(t *testing.T, filename string, secondCPU macho.Cpu) {
	t.Helper()
	encoded := make([]byte, 4096*3)
	binary.BigEndian.PutUint32(encoded, 0xcafebabe)
	binary.BigEndian.PutUint32(encoded[4:], 2)
	for index, cpu := range []macho.Cpu{macho.CpuAmd64, secondCPU} {
		offset := 4096 * (index + 1)
		header := encoded[8+index*20:]
		binary.BigEndian.PutUint32(header, uint32(cpu))
		binary.BigEndian.PutUint32(header[8:], uint32(offset))
		binary.BigEndian.PutUint32(header[12:], 56)
		binary.BigEndian.PutUint32(header[16:], 12)
		binary.LittleEndian.PutUint32(encoded[offset:], 0xfeedfacf)
		binary.LittleEndian.PutUint32(encoded[offset+4:], uint32(cpu))
		binary.LittleEndian.PutUint32(encoded[offset+12:], uint32(macho.TypeExec))
		binary.LittleEndian.PutUint32(encoded[offset+16:], 1)
		binary.LittleEndian.PutUint32(encoded[offset+20:], 24)
		binary.LittleEndian.PutUint32(encoded[offset+32:], 0x32)
		binary.LittleEndian.PutUint32(encoded[offset+36:], 24)
		binary.LittleEndian.PutUint32(encoded[offset+40:], 1)
		binary.LittleEndian.PutUint32(encoded[offset+44:], 13<<16)
	}
	if err := os.WriteFile(filename, encoded, 0o700); err != nil {
		t.Fatal(err)
	}
}

func TestMacOSUniversalVerificationRejectsMissingArchitecture(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "universal")
	writeUniversalFixture(t, filename, macho.CpuArm64)
	if err := verifyMacOSUniversalBinary(filename); err != nil {
		t.Fatal(err)
	}
	encoded, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	binary.LittleEndian.PutUint32(encoded[4096+44:], 12<<16)
	if err := os.WriteFile(filename, encoded, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := verifyMacOSUniversalBinary(filename); err == nil {
		t.Fatal("unsupported minimum OS accepted")
	}
	writeUniversalFixture(t, filename, macho.Cpu386)
	if err := verifyMacOSUniversalBinary(filename); err == nil {
		t.Fatal("universal binary without arm64 accepted")
	}
	if err := os.WriteFile(filename, []byte("invalid"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := verifyMacOSUniversalBinary(filename); err == nil {
		t.Fatal("non Mach-O binary accepted")
	}
}

func TestMacOSPackageStageConsumesExistingBinaryWithoutCompilation(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("DMG packaging runs on macOS")
	}
	commands := t.TempDir()
	writeCommandStub(t, commands, "go", "exit 99")
	writeCommandStub(t, commands, "cp", `if [ "$1" = "-R" ]; then exit 0; fi; for argument in "$@"; do destination="$argument"; done; printf binary > "$destination"`)
	writeCommandStub(t, commands, "sips", `previous=""; for argument in "$@"; do if [ "$previous" = "--out" ]; then printf icon > "$argument"; fi; previous="$argument"; done`)
	writeCommandStub(t, commands, "iconutil", `previous=""; for argument in "$@"; do if [ "$previous" = "-o" ]; then printf icon > "$argument"; fi; previous="$argument"; done`)
	writeCommandStub(t, commands, "hdiutil", `previous=""; size=""; for argument in "$@"; do if [ "$previous" = "-size" ]; then size="$argument"; fi; previous="$argument"; destination="$argument"; done; test -n "$size"; printf dmg > "$destination"`)
	writeCommandStub(t, commands, "ln", "exit 0")
	t.Setenv("PATH", commands)
	outputRoot := t.TempDir()
	desktopDirectory := filepath.Join(outputRoot, "123", "desktop-app")
	if err := os.MkdirAll(desktopDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	writeUniversalFixture(t, filepath.Join(desktopDirectory, "sitebrush_darwin_universal_desktop"), macho.CpuArm64)
	previousArgs, previousFlags := os.Args, flag.CommandLine
	defer func() { os.Args, flag.CommandLine = previousArgs, previousFlags }()
	flag.CommandLine = flag.NewFlagSet("macos-package-test", flag.ContinueOnError)
	os.Args = []string{"crosscompile", "-mode", "desktop-app", "-os", "darwin", "-macos-stage", "package", "-version", "123", "-output-dir", outputRoot}
	main()
	if err := verifyNonEmptyFile(filepath.Join(desktopDirectory, "sitebrush_darwin_universal_desktop.dmg")); err != nil {
		t.Fatal(err)
	}
}
