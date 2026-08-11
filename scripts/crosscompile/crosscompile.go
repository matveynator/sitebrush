package main

import (
	"crypto/md5"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

type buildMode string

const (
	modeAll        buildMode = "all"
	modeServerApp  buildMode = "server-app"
	modeDesktopApp buildMode = "desktop-app"
)

const (
	dockerBuilderImageVersion = "v1"
	dockerGoVersion           = "1.26.5"
	dockerWorkspaceRoot       = "/workspace"
	llvmMingwVersion          = "20260505"
)

type serverAppTarget struct {
	goos   string
	goarch string
}

type buildTargetFilter struct {
	goos   string
	goarch string
}

type desktopBuildOptions struct {
	rebuildDockerImages    bool
	installDockerEmulation bool
	targetFilter           buildTargetFilter
}

type buildRequest struct {
	goos       string
	goarch     string
	cgoEnabled string
	tags       []string
	ldflags    []string
	extraEnv   map[string]string
}

type syncDestination struct {
	host string
	base string
}

type syncDestinationFlags []syncDestination

func (flags *syncDestinationFlags) String() string {
	if flags == nil || len(*flags) == 0 {
		return ""
	}
	parts := make([]string, 0, len(*flags))
	for _, destination := range *flags {
		parts = append(parts, destination.host+"="+destination.base)
	}
	return strings.Join(parts, ",")
}

func (flags *syncDestinationFlags) Set(value string) error {
	destination, err := parseSyncDestination(value)
	if err != nil {
		return err
	}
	*flags = append(*flags, destination)
	return nil
}

func main() {
	var (
		programName            = flag.String("program", "sitebrush", "program name used in binary names")
		versionFlag            = flag.String("version", "", "version folder under binaries/; defaults to GITHUB_RUN_NUMBER, then git rev-list count, then git describe")
		outputRoot             = flag.String("output-dir", "binaries", "root output directory for generated artifacts")
		modeFlag               = flag.String("mode", string(modeAll), "build scope: all, server-app, or desktop-app")
		targetOS               = flag.String("os", "", "optional GOOS target filter, for example linux")
		targetArch             = flag.String("arch", "", "optional GOARCH target filter, for example amd64")
		rebuildDockerImages    = flag.Bool("rebuild-docker-images", false, "rebuild cached Docker builder images before Docker-based desktop builds")
		installDockerEmulation = flag.Bool("install-docker-emulation", false, "register missing foreign-architecture Docker emulation on the host using a privileged binfmt container")
		syncTargets            syncDestinationFlags
	)
	flag.Var(&syncTargets, "sync", "optional repeatable publication target in host=/remote/base format, for example root@sitebrush.com=/var/lib/sitebrush/storage/chroot/sitebrush.com/download")
	flag.Parse()

	targetFilter := buildTargetFilter{
		goos:   strings.TrimSpace(*targetOS),
		goarch: strings.TrimSpace(*targetArch),
	}
	mode := buildMode(*modeFlag)
	if mode == "portable" {
		mode = modeServerApp
	}
	if mode == "desktop" {
		mode = modeDesktopApp
	}
	mode = effectiveBuildMode(mode, targetFilter, flagWasSet("mode"))
	if mode != modeAll && mode != modeServerApp && mode != modeDesktopApp {
		fatalf("invalid -mode %q", *modeFlag)
	}

	repoRoot, err := findRepoRoot()
	if err != nil {
		fatalf("resolve repo root: %v", err)
	}

	version := strings.TrimSpace(*versionFlag)
	if version == "" {
		version = defaultVersionLabel(repoRoot)
	}
	version = sanitizePathSegment(version)
	if version == "" {
		fatalf("resolved empty version label")
	}

	outputDir := filepath.Join(repoRoot, *outputRoot, version)
	if err := os.RemoveAll(outputDir); err != nil {
		fatalf("clean output dir: %v", err)
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		fatalf("create output dir: %v", err)
	}

	fmt.Printf("repo: %s\n", repoRoot)
	fmt.Printf("program: %s\n", *programName)
	fmt.Printf("version: %s\n", version)
	fmt.Printf("output: %s\n", outputDir)
	if targetFilter.active() {
		fmt.Printf("target filter: %s\n", targetFilter.label())
	}

	if mode == modeAll || mode == modeServerApp {
		if err := buildServerAppArtifacts(repoRoot, outputDir, *programName, version, targetFilter); err != nil {
			fatalf("server-app build: %v", err)
		}
	}

	if mode == modeAll || mode == modeDesktopApp {
		if err := buildDesktopAppArtifacts(repoRoot, outputDir, *programName, version, desktopBuildOptions{
			rebuildDockerImages:    *rebuildDockerImages,
			installDockerEmulation: *installDockerEmulation,
			targetFilter:           targetFilter,
		}); err != nil {
			fatalf("desktop-app build: %v", err)
		}
	}

	if err := updateLatestSymlink(filepath.Join(repoRoot, *outputRoot), version); err != nil {
		fatalf("update latest symlink: %v", err)
	}

	destinations := syncPublicationDestinations(syncTargets)
	for _, destination := range destinations {
		if err := syncArtifacts(repoRoot, filepath.Join(repoRoot, *outputRoot), version, destination.host, destination.base); err != nil {
			fatalf("sync artifacts to %s: %v", destination.host, err)
		}
	}

	fmt.Println("done")
}

func buildServerAppArtifacts(repoRoot, outputDir, programName, version string, targetFilter buildTargetFilter) error {
	serverAppDir := filepath.Join(outputDir, "server-app")
	if err := os.MkdirAll(serverAppDir, 0o755); err != nil {
		return err
	}

	fmt.Println("== server-app builds ==")
	targets := filteredServerAppTargets(serverAppTargets(), targetFilter)
	if len(targets) == 0 {
		return fmt.Errorf("no server-app targets match %s", targetFilter.label())
	}
	for _, target := range targets {
		artifactName := serverAppArtifactName(programName, target.goos, target.goarch)
		artifactPath := filepath.Join(serverAppDir, artifactName)
		fmt.Printf("build %s/%s -> %s\n", target.goos, target.goarch, artifactPath)
		if err := buildGoBinary(repoRoot, artifactPath, buildRequest{
			goos:       target.goos,
			goarch:     target.goarch,
			cgoEnabled: "0",
			tags:       []string{"netgo", "osusergo"},
			ldflags:    []string{"-s", "-w", "-X", "main.CompileVersion=" + version},
		}); err != nil {
			return err
		}
	}

	return writeMD5SumsFile(serverAppDir)
}

func buildDesktopAppArtifacts(repoRoot, outputDir, programName, version string, options desktopBuildOptions) error {
	desktopDir := filepath.Join(outputDir, "desktop-app")
	if err := os.MkdirAll(desktopDir, 0o755); err != nil {
		return err
	}

	builtAny := false
	if options.targetFilter.matches("darwin", "universal") && runtime.GOOS == "darwin" {
		if err := buildMacOSDesktopArtifacts(repoRoot, desktopDir, programName, version); err != nil {
			return err
		}
		builtAny = true
	} else if options.targetFilter.matches("darwin", "universal") {
		fmt.Printf("skip macOS desktop build on host %s\n", runtime.GOOS)
	}

	linuxBuilt, err := buildLinuxDesktopArtifacts(repoRoot, desktopDir, programName, version, options)
	if err != nil {
		return err
	}
	builtAny = builtAny || linuxBuilt
	windowsBuilt, err := buildWindowsDesktopArtifacts(repoRoot, desktopDir, programName, version, options)
	if err != nil {
		return err
	}
	builtAny = builtAny || windowsBuilt
	if !builtAny {
		return fmt.Errorf("no desktop-app targets match %s", options.targetFilter.label())
	}

	if err := cleanupDesktopBuildIntermediates(desktopDir); err != nil {
		return err
	}
	return writeMD5SumsFile(desktopDir)
}

func buildMacOSDesktopArtifacts(repoRoot, desktopDir, programName, version string) error {
	fmt.Println("== macOS desktop builds ==")

	amd64Binary := filepath.Join(desktopDir, desktopArtifactName(programName, "darwin", "amd64", ""))
	if err := buildGoBinary(repoRoot, amd64Binary, buildRequest{
		goos:       "darwin",
		goarch:     "amd64",
		cgoEnabled: "1",
		tags:       []string{"desktop"},
		ldflags:    []string{"-s", "-w", "-X", "main.CompileVersion=" + version},
		extraEnv: map[string]string{
			"MACOSX_DEPLOYMENT_TARGET": "10.13",
			"CC":                       "clang -arch x86_64",
			"CXX":                      "clang++ -arch x86_64",
			"CGO_CFLAGS":               "-arch x86_64 -mmacosx-version-min=10.13",
			"CGO_CXXFLAGS":             "-arch x86_64 -mmacosx-version-min=10.13",
			"CGO_LDFLAGS":              "-arch x86_64 -mmacosx-version-min=10.13",
		},
	}); err != nil {
		return err
	}

	arm64Binary := filepath.Join(desktopDir, desktopArtifactName(programName, "darwin", "arm64", ""))
	if err := buildGoBinary(repoRoot, arm64Binary, buildRequest{
		goos:       "darwin",
		goarch:     "arm64",
		cgoEnabled: "1",
		tags:       []string{"desktop"},
		ldflags:    []string{"-s", "-w", "-X", "main.CompileVersion=" + version},
		extraEnv: map[string]string{
			"MACOSX_DEPLOYMENT_TARGET": "11.0",
			"CC":                       "clang -arch arm64",
			"CXX":                      "clang++ -arch arm64",
			"CGO_CFLAGS":               "-arch arm64 -mmacosx-version-min=11.0",
			"CGO_CXXFLAGS":             "-arch arm64 -mmacosx-version-min=11.0",
			"CGO_LDFLAGS":              "-arch arm64 -mmacosx-version-min=11.0",
		},
	}); err != nil {
		return err
	}

	universalBinary := filepath.Join(desktopDir, desktopArtifactName(programName, "darwin", "universal", ""))
	if err := runCommand(repoRoot, "lipo", "-create", amd64Binary, arm64Binary, "-output", universalBinary); err != nil {
		return err
	}
	if err := runCommand(repoRoot, "lipo", universalBinary, "-verify_arch", "x86_64", "arm64"); err != nil {
		return err
	}
	if err := os.Chmod(universalBinary, 0o755); err != nil {
		return err
	}

	dmgPath, err := packageMacOSDesktopDMG(desktopDir, programName, universalBinary, version)
	if err != nil {
		return err
	}
	if dmgPath != "" {
		fmt.Printf("created %s\n", dmgPath)
	}
	return nil
}

func buildLinuxDesktopArtifacts(repoRoot, desktopDir, programName, version string, options desktopBuildOptions) (bool, error) {
	fmt.Println("== Linux desktop builds via docker ==")
	if !options.targetFilter.matchesOS("linux") {
		return false, nil
	}
	targets := []struct {
		goarch  string
		variant string
	}{
		{goarch: "amd64", variant: "gtk40"},
		{goarch: "amd64", variant: "gtk41"},
		{goarch: "arm64", variant: "gtk40"},
		{goarch: "arm64", variant: "gtk41"},
	}
	matchingTargets := make([]struct {
		goarch  string
		variant string
	}, 0, len(targets))
	for _, target := range targets {
		if !options.targetFilter.matches("linux", target.goarch) {
			continue
		}
		matchingTargets = append(matchingTargets, target)
	}
	if len(matchingTargets) == 0 {
		return false, nil
	}
	if !commandExists("docker") {
		return false, fmt.Errorf("docker is required to build Linux desktop variants")
	}
	for _, goarch := range []string{"amd64", "arm64"} {
		if !desktopTargetsIncludeArchitecture(matchingTargets, goarch) {
			continue
		}
		if err := ensureDockerPlatformSupport(repoRoot, "linux/"+goarch, options.installDockerEmulation); err != nil {
			return false, err
		}
	}

	for _, target := range matchingTargets {
		if err := buildLinuxDesktopArtifactInDocker(repoRoot, desktopDir, programName, version, target.goarch, target.variant, options); err != nil {
			return false, err
		}
	}

	return true, nil
}

func desktopTargetsIncludeArchitecture(targets []struct {
	goarch  string
	variant string
}, goarch string) bool {
	for _, target := range targets {
		if target.goarch == goarch {
			return true
		}
	}
	return false
}

func buildWindowsDesktopArtifacts(repoRoot, desktopDir, programName, version string, options desktopBuildOptions) (bool, error) {
	fmt.Println("== Windows desktop builds via docker ==")
	if !options.targetFilter.matchesOS("windows") {
		return false, nil
	}
	matchingArchitectures := make([]string, 0, 2)
	for _, goarch := range []string{"amd64", "arm64"} {
		if !options.targetFilter.matches("windows", goarch) {
			continue
		}
		matchingArchitectures = append(matchingArchitectures, goarch)
	}
	if len(matchingArchitectures) == 0 {
		return false, nil
	}
	if !commandExists("docker") {
		return false, fmt.Errorf("docker is required to build Windows desktop variants")
	}

	for _, goarch := range matchingArchitectures {
		if err := buildWindowsDesktopArtifactInDocker(repoRoot, desktopDir, programName, version, goarch, options); err != nil {
			return false, err
		}
	}
	return true, nil
}

func buildLinuxDesktopArtifactInDocker(repoRoot, desktopDir, programName, version, goarch, variant string, options desktopBuildOptions) error {
	artifactName := desktopArtifactName(programName, "linux", goarch, variant)
	artifactPath := filepath.Join(desktopDir, artifactName)
	containerArtifactPath, err := dockerWorkspacePath(repoRoot, artifactPath)
	if err != nil {
		return err
	}
	dockerPlatform := "linux/" + goarch
	dockerImage := linuxDesktopDockerImage(goarch, variant)
	if err := ensureDockerBuilderImage(repoRoot, dockerPlatform, dockerImage, linuxDesktopDockerfile(goarch, variant), options.rebuildDockerImages); err != nil {
		return err
	}
	dockerScript := linuxDesktopDockerScript(containerArtifactPath, artifactName, goarch, variant, version)
	fmt.Printf("build linux/%s %s -> %s\n", goarch, variant, artifactPath)
	if err := runDockerShellScript(repoRoot, dockerPlatform, dockerImage, dockerScript, nil); err != nil {
		return err
	}
	return verifyBuiltDesktopArtifact(artifactPath)
}

func buildWindowsDesktopArtifactInDocker(repoRoot, desktopDir, programName, version, goarch string, options desktopBuildOptions) error {
	artifactName := desktopArtifactName(programName, "windows", goarch, "")
	artifactPath := filepath.Join(desktopDir, artifactName)
	containerArtifactPath, err := dockerWorkspacePath(repoRoot, artifactPath)
	if err != nil {
		return err
	}
	dockerImage := windowsDesktopDockerImage(goarch)
	if err := ensureDockerBuilderImage(repoRoot, "linux/amd64", dockerImage, windowsDesktopDockerfile(goarch), options.rebuildDockerImages); err != nil {
		return err
	}
	dockerScript := windowsDesktopDockerScript(containerArtifactPath, artifactName, goarch, version)
	fmt.Printf("build windows/%s -> %s\n", goarch, artifactPath)
	if err := runDockerShellScript(repoRoot, "linux/amd64", dockerImage, dockerScript, nil); err != nil {
		return err
	}
	return verifyBuiltDesktopArtifact(artifactPath)
}

func linuxDesktopBaseImage(variant string) string {
	if variant == "gtk41" {
		return "ubuntu:24.04"
	}
	return "ubuntu:22.04"
}

func linuxDesktopDockerImage(goarch, variant string) string {
	return fmt.Sprintf("sitebrush-crosscompile:linux-%s-%s-go%s-%s", goarch, variant, dockerGoVersion, dockerBuilderImageVersion)
}

func windowsDesktopDockerImage(goarch string) string {
	return fmt.Sprintf("sitebrush-crosscompile:windows-%s-go%s-%s", goarch, dockerGoVersion, dockerBuilderImageVersion)
}

func linuxDesktopDockerfile(goarch, variant string) string {
	webkitPackage := "libwebkit2gtk-4.0-dev"
	if variant == "gtk41" {
		webkitPackage = "libwebkit2gtk-4.1-dev"
	}

	return fmt.Sprintf(`FROM --platform=linux/%s %s
SHELL ["/bin/bash", "-o", "pipefail", "-c"]
ENV DEBIAN_FRONTEND=noninteractive
RUN set -e; apt-get update; apt-get install -y ca-certificates curl build-essential pkg-config libgtk-3-dev %s zip tar; rm -rf /var/lib/apt/lists/*
RUN curl -fsSL "https://go.dev/dl/go%s.linux-%s.tar.gz" | tar -C /usr/local -xz
ENV PATH="/usr/local/go/bin:${PATH}"
RUN if ! pkg-config --exists webkit2gtk-4.0 && pkg-config --exists webkit2gtk-4.1; then \
  install -d /usr/local/lib/pkgconfig; \
  printf 'prefix=/usr\nexec_prefix=${prefix}\nlibdir=${exec_prefix}/lib\nincludedir=${prefix}/include\n\nName: webkit2gtk-4.0\nDescription: Compatibility shim for webkit2gtk-4.1\nVersion: 4.0\nRequires: webkit2gtk-4.1\nLibs:\nCflags:\n' > /usr/local/lib/pkgconfig/webkit2gtk-4.0.pc; \
fi
`, goarch, linuxDesktopBaseImage(variant), webkitPackage, dockerGoVersion, goarch)
}

func linuxDesktopDockerScript(artifactPath, artifactName, goarch, variant, version string) string {
	artifactDir := path.Dir(artifactPath)

	return fmt.Sprintf(`
set -euo pipefail
mkdir -p %s
GOFLAGS="-trimpath -buildvcs=false" \
CGO_ENABLED=1 \
GOOS=linux \
GOARCH=%s \
go build -tags "desktop" \
  -ldflags "-s -w -X main.CompileVersion=%s" \
  -o %s
chmod +x %s
(
  cd %s
  zip -q -X %s %s
)
`, shellQuote(artifactDir), goarch, shellQuote(version), shellQuote(artifactPath), shellQuote(artifactPath), shellQuote(artifactDir), shellQuote(artifactName+".zip"), shellQuote(artifactName))
}

func windowsDesktopDockerfile(goarch string) string {
	installPackages := "apt-get install -y ca-certificates curl build-essential mingw-w64 zip tar"
	installCrossCompiler := ""

	if goarch == "arm64" {
		installPackages = "apt-get install -y ca-certificates curl build-essential zip tar xz-utils"
		installCrossCompiler = fmt.Sprintf(`RUN set -e; \
  LLVM_MINGW_VERSION="%s"; \
  LLVM_MINGW_ARCHIVE="llvm-mingw-${LLVM_MINGW_VERSION}-ucrt-ubuntu-22.04-x86_64.tar.xz"; \
  LLVM_MINGW_URL="https://github.com/mstorsjo/llvm-mingw/releases/download/${LLVM_MINGW_VERSION}/${LLVM_MINGW_ARCHIVE}"; \
  curl -fsSL "$LLVM_MINGW_URL" -o "/tmp/$LLVM_MINGW_ARCHIVE"; \
  mkdir -p /opt/llvm-mingw; \
  tar -xf "/tmp/$LLVM_MINGW_ARCHIVE" -C /opt/llvm-mingw --strip-components=1; \
  export PATH="/opt/llvm-mingw/bin:$PATH"; \
  command -v aarch64-w64-mingw32-gcc; \
  command -v aarch64-w64-mingw32-g++
`, llvmMingwVersion)
	}

	return fmt.Sprintf(`
FROM --platform=linux/amd64 ubuntu:22.04
SHELL ["/bin/bash", "-o", "pipefail", "-c"]
ENV DEBIAN_FRONTEND=noninteractive
RUN set -e; apt-get update; %s; rm -rf /var/lib/apt/lists/*
%s
RUN curl -fsSL "https://go.dev/dl/go%s.linux-amd64.tar.gz" | tar -C /usr/local -xz
ENV PATH="/usr/local/go/bin:/root/go/bin:/opt/llvm-mingw/bin:${PATH}"
RUN go install github.com/akavel/rsrc@latest
`, installPackages, installCrossCompiler, dockerGoVersion)
}

func windowsDesktopDockerScript(artifactPath, artifactName, goarch, version string) string {
	resourceArch := "amd64"
	cc := "x86_64-w64-mingw32-gcc"
	cxx := "x86_64-w64-mingw32-g++"
	artifactDir := path.Dir(artifactPath)

	if goarch == "arm64" {
		resourceArch = "arm64"
		cc = "aarch64-w64-mingw32-gcc"
		cxx = "aarch64-w64-mingw32-g++"
	}

	return fmt.Sprintf(`
set -euo pipefail
rsrcBin="$(go env GOPATH)/bin/rsrc"
"$rsrcBin" -ico "web/static/sitebrush-logo.ico" -arch %s -o "zz_sitebrush_icon_windows_%s.syso"
trap 'rm -f "zz_sitebrush_icon_windows_%s.syso"' EXIT

mkdir -p %s

GOFLAGS="-trimpath -buildvcs=false" \
CGO_ENABLED=1 \
GOOS=windows \
GOARCH=%s \
CC=%s \
CXX=%s \
go build -tags "desktop" \
  -ldflags "-H windowsgui -s -w -X main.CompileVersion=%s" \
  -o %s

(
  cd %s
  zip -q -X %s %s
)
`, resourceArch, goarch, goarch, shellQuote(artifactDir), goarch, cc, cxx, shellQuote(version), shellQuote(artifactPath), shellQuote(artifactDir), shellQuote(artifactName+".zip"), shellQuote(artifactName))
}

func buildGoBinary(repoRoot, outputPath string, request buildRequest) error {
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return err
	}

	args := []string{"build"}
	if len(request.tags) > 0 {
		args = append(args, "-tags", strings.Join(request.tags, ","))
	}
	if len(request.ldflags) > 0 {
		args = append(args, "-ldflags", strings.Join(request.ldflags, " "))
	}
	args = append(args, "-o", outputPath)

	cmd := exec.Command("go", args...)
	cmd.Dir = repoRoot
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(),
		"GOOS="+request.goos,
		"GOARCH="+request.goarch,
		"CGO_ENABLED="+request.cgoEnabled,
		"GOFLAGS=-trimpath -buildvcs=false",
	)
	for key, value := range request.extraEnv {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	return cmd.Run()
}

func serverAppTargets() []serverAppTarget {
	return []serverAppTarget{
		{goos: "linux", goarch: "amd64"},
		{goos: "linux", goarch: "arm64"},
		{goos: "linux", goarch: "386"},
		{goos: "darwin", goarch: "amd64"},
		{goos: "darwin", goarch: "arm64"},
		{goos: "freebsd", goarch: "amd64"},
		{goos: "freebsd", goarch: "arm64"},
		{goos: "openbsd", goarch: "amd64"},
		{goos: "openbsd", goarch: "arm64"},
		{goos: "netbsd", goarch: "amd64"},
		{goos: "netbsd", goarch: "arm64"},
		{goos: "windows", goarch: "amd64"},
		{goos: "windows", goarch: "arm64"},
	}
}

func filteredServerAppTargets(targets []serverAppTarget, targetFilter buildTargetFilter) []serverAppTarget {
	if !targetFilter.active() {
		return targets
	}
	filteredTargets := make([]serverAppTarget, 0, len(targets))
	for _, target := range targets {
		if targetFilter.matches(target.goos, target.goarch) {
			filteredTargets = append(filteredTargets, target)
		}
	}
	return filteredTargets
}

func (targetFilter buildTargetFilter) active() bool {
	return targetFilter.goos != "" || targetFilter.goarch != ""
}

func (targetFilter buildTargetFilter) matches(goos, goarch string) bool {
	if targetFilter.goos != "" && targetFilter.goos != goos {
		return false
	}
	if targetFilter.goarch != "" && targetFilter.goarch != goarch {
		return false
	}
	return true
}

func (targetFilter buildTargetFilter) matchesOS(goos string) bool {
	return targetFilter.goos == "" || targetFilter.goos == goos
}

func (targetFilter buildTargetFilter) label() string {
	parts := make([]string, 0, 2)
	if targetFilter.goos != "" {
		parts = append(parts, "os="+targetFilter.goos)
	}
	if targetFilter.goarch != "" {
		parts = append(parts, "arch="+targetFilter.goarch)
	}
	if len(parts) == 0 {
		return "all targets"
	}
	return strings.Join(parts, " ")
}

func effectiveBuildMode(mode buildMode, targetFilter buildTargetFilter, modeWasSet bool) buildMode {
	if mode == modeAll && targetFilter.active() && !modeWasSet {
		return modeServerApp
	}
	return mode
}

func serverAppArtifactName(programName, goos, goarch string) string {
	if goos == "windows" {
		return fmt.Sprintf("%s_%s_%s.exe", programName, goos, goarch)
	}
	return fmt.Sprintf("%s_%s_%s", programName, goos, goarch)
}

func desktopArtifactName(programName, goos, goarch, variant string) string {
	if goos == "darwin" && goarch == "universal" {
		return fmt.Sprintf("%s_%s_%s_desktop", programName, goos, goarch)
	}
	if goos == "windows" {
		return fmt.Sprintf("%s_%s_%s_desktop.exe", programName, goos, goarch)
	}
	if variant != "" {
		return fmt.Sprintf("%s_%s_%s_desktop_%s", programName, goos, goarch, variant)
	}
	return fmt.Sprintf("%s_%s_%s_desktop", programName, goos, goarch)
}

func linuxDesktopVariant() (string, bool) {
	if !commandExists("pkg-config") {
		return "", false
	}
	if runCommand(".", "pkg-config", "--exists", "webkit2gtk-4.0") == nil {
		return "gtk40", true
	}
	if runCommand(".", "pkg-config", "--exists", "webkit2gtk-4.1") == nil {
		return "gtk41", true
	}
	return "", false
}

func packageMacOSDesktopDMG(desktopDir, programName, universalBinary, version string) (string, error) {
	appDir := filepath.Join(desktopDir, programName+".app")
	if err := os.RemoveAll(appDir); err != nil {
		return "", err
	}
	defer os.RemoveAll(appDir)

	stagingDir, err := os.MkdirTemp(desktopDir, "dmg-staging-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(stagingDir)

	if err := createMacOSAppBundle(appDir, universalBinary, version); err != nil {
		return "", err
	}

	if err := runCommand(desktopDir, "cp", "-R", appDir, stagingDir+"/"); err != nil {
		return "", err
	}
	if err := runCommand(desktopDir, "ln", "-s", "/Applications", filepath.Join(stagingDir, "Applications")); err != nil {
		return "", err
	}

	dmgPath := filepath.Join(desktopDir, fmt.Sprintf("%s_darwin_universal_desktop.dmg", programName))
	if err := os.RemoveAll(dmgPath); err != nil {
		return "", err
	}
	if err := runCommand(desktopDir, "hdiutil", "create", "-volname", fmt.Sprintf("%s %s", programName, version), "-srcfolder", stagingDir, "-ov", "-format", "UDZO", dmgPath); err != nil {
		fmt.Printf("warn: macOS DMG build failed, continuing without DMG: %v\n", err)
		return "", nil
	}
	return dmgPath, nil
}

func createMacOSAppBundle(appDir, universalBinary, version string) error {
	contentsDir := filepath.Join(appDir, "Contents")
	macOSDir := filepath.Join(contentsDir, "MacOS")
	resourcesDir := filepath.Join(contentsDir, "Resources")
	if err := os.MkdirAll(macOSDir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(resourcesDir, 0o755); err != nil {
		return err
	}

	appBinaryPath := filepath.Join(macOSDir, "sitebrush")
	if err := runCommand(filepath.Dir(appDir), "cp", universalBinary, appBinaryPath); err != nil {
		return err
	}
	if err := os.Chmod(appBinaryPath, 0o755); err != nil {
		return err
	}

	iconPath, err := createMacOSIcon(resourcesDir)
	if err != nil {
		fmt.Printf("warn: macOS icon build failed, continuing without custom icon: %v\n", err)
		iconPath = ""
	}

	iconSection := ""
	if iconPath != "" {
		iconSection = fmt.Sprintf("  <key>CFBundleIconFile</key>\n  <string>%s</string>\n", filepath.Base(iconPath))
	}

	infoPlistPath := filepath.Join(contentsDir, "Info.plist")
	infoPlist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleName</key>
  <string>sitebrush</string>
  <key>CFBundleDisplayName</key>
  <string>sitebrush</string>
  <key>CFBundleIdentifier</key>
  <string>net.zabiyaka.sitebrush</string>
  <key>CFBundleVersion</key>
  <string>%s</string>
  <key>CFBundleShortVersionString</key>
  <string>%s</string>
  <key>CFBundleExecutable</key>
  <string>sitebrush</string>
  <key>CFBundlePackageType</key>
  <string>APPL</string>
%s  <key>LSMinimumSystemVersion</key>
  <string>10.13</string>
</dict>
</plist>
`, version, version, iconSection)
	if err := os.WriteFile(infoPlistPath, []byte(infoPlist), 0o644); err != nil {
		return err
	}

	if commandExists("codesign") {
		appRootDir := filepath.Dir(appDir)
		if err := runCommand(appRootDir, "codesign", "--force", "--deep", "--sign", "-", appDir); err != nil {
			return err
		}
		if err := runCommand(appRootDir, "codesign", "--verify", "--deep", "--strict", appDir); err != nil {
			return err
		}
	}

	return nil
}

func createMacOSIcon(resourcesDir string) (string, error) {
	iconSetDir := filepath.Join(resourcesDir, "AppIcon.iconset")
	if err := os.RemoveAll(iconSetDir); err != nil {
		return "", err
	}
	if err := os.MkdirAll(iconSetDir, 0o755); err != nil {
		return "", err
	}

	iconSource := filepath.Join("web", "static", "sitebrush-logo.ico")
	iconPNG := filepath.Join(resourcesDir, "AppIcon-source.png")
	if err := runCommand(".", "sips", "-s", "format", "png", iconSource, "--out", iconPNG); err != nil {
		iconSource = filepath.Join("web", "static", "sitebrush-logo.png")
		if err := runCommand(".", "cp", iconSource, iconPNG); err != nil {
			return "", err
		}
	}

	for _, size := range []int{16, 32, 128} {
		smallPath := filepath.Join(iconSetDir, fmt.Sprintf("icon_%dx%d.png", size, size))
		if err := runCommand(".", "sips", "-z", fmt.Sprintf("%d", size), fmt.Sprintf("%d", size), iconPNG, "--out", smallPath); err != nil {
			return "", err
		}
		doublePath := filepath.Join(iconSetDir, fmt.Sprintf("icon_%dx%d@2x.png", size, size))
		if err := runCommand(".", "sips", "-z", fmt.Sprintf("%d", size*2), fmt.Sprintf("%d", size*2), iconPNG, "--out", doublePath); err != nil {
			return "", err
		}
	}
	if err := runCommand(".", "sips", "-z", "256", "256", iconPNG, "--out", filepath.Join(iconSetDir, "icon_256x256.png")); err != nil {
		return "", err
	}
	if err := runCommand(".", "sips", "-z", "512", "512", iconPNG, "--out", filepath.Join(iconSetDir, "icon_256x256@2x.png")); err != nil {
		return "", err
	}
	if err := runCommand(".", "sips", "-z", "512", "512", iconPNG, "--out", filepath.Join(iconSetDir, "icon_512x512.png")); err != nil {
		return "", err
	}
	if err := runCommand(".", "sips", "-z", "1024", "1024", iconPNG, "--out", filepath.Join(iconSetDir, "icon_512x512@2x.png")); err != nil {
		return "", err
	}
	iconPath := filepath.Join(resourcesDir, "AppIcon.icns")
	if err := runCommand(".", "iconutil", "-c", "icns", iconSetDir, "-o", iconPath); err != nil {
		return "", err
	}
	return iconPath, nil
}

func updateLatestSymlink(binaryRoot, version string) error {
	if err := os.MkdirAll(binaryRoot, 0o755); err != nil {
		return err
	}
	latestPath := filepath.Join(binaryRoot, "latest")
	if err := os.RemoveAll(latestPath); err != nil {
		return err
	}
	if err := os.Symlink(version, latestPath); err != nil {
		return err
	}
	fmt.Printf("updated %s -> %s\n", latestPath, version)
	return nil
}

func syncArtifacts(repoRoot, outputRoot, version, syncHost, syncBase string) error {
	remoteDirectory := remoteSyncDirectory(syncBase)
	if remoteDirectory == "" {
		return errors.New("remote sync directory is empty")
	}
	fmt.Printf("sync %s to %s:%s\n", outputRoot, syncHost, remoteDirectory)
	if err := runCommand(repoRoot, "ssh", syncHost, fmt.Sprintf("mkdir -p %s", shellQuote(remoteDirectory))); err != nil {
		return err
	}
	if err := runCommand(repoRoot, "rsync", "-avP", rsyncSourceDirectory(outputRoot), rsyncRemoteDirectory(syncHost, remoteDirectory)); err != nil {
		return err
	}
	fmt.Printf("synced latest -> %s via rsync\n", version)
	return nil
}

func syncPublicationDestinations(syncTargets []syncDestination) []syncDestination {
	destinations := make([]syncDestination, 0, len(syncTargets))
	destinations = append(destinations, syncTargets...)
	return destinations
}

func parseSyncDestination(value string) (syncDestination, error) {
	host, base, found := strings.Cut(value, "=")
	host = strings.TrimSpace(host)
	base = strings.TrimSpace(base)
	if !found {
		return syncDestination{}, errors.New("expected host=/remote/base")
	}
	if host == "" {
		return syncDestination{}, errors.New("sync target host is empty")
	}
	if base == "" {
		return syncDestination{}, errors.New("sync target remote base is empty")
	}
	return syncDestination{host: host, base: base}, nil
}

func flagWasSet(name string) bool {
	found := false
	flag.Visit(func(parsedFlag *flag.Flag) {
		if parsedFlag.Name == name {
			found = true
		}
	})
	return found
}

func remoteSyncDirectory(syncBase string) string {
	remoteDirectory := strings.TrimSpace(syncBase)
	for len(remoteDirectory) > 1 && strings.HasSuffix(remoteDirectory, "/") {
		remoteDirectory = strings.TrimSuffix(remoteDirectory, "/")
	}
	return remoteDirectory
}

func rsyncSourceDirectory(outputRoot string) string {
	return filepath.Clean(outputRoot) + string(filepath.Separator)
}

func rsyncRemoteDirectory(syncHost, remoteDirectory string) string {
	return syncHost + ":" + strings.TrimRight(remoteDirectory, "/") + "/"
}

func defaultVersionLabel(repoRoot string) string {
	if version := strings.TrimSpace(os.Getenv("GITHUB_RUN_NUMBER")); version != "" {
		return version
	}
	if version, err := runOutput(repoRoot, "git", "rev-list", "--count", "HEAD"); err == nil {
		version = strings.TrimSpace(version)
		if version != "" {
			return version
		}
	}
	if version, err := runOutput(repoRoot, "git", "describe", "--tags", "--always", "--dirty"); err == nil {
		version = strings.TrimSpace(version)
		if version != "" {
			return version
		}
	}
	return "dev"
}

func findRepoRoot() (string, error) {
	_, scriptPath, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("runtime.Caller failed")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(scriptPath), "..", ".."))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		return "", err
	}
	return root, nil
}

func runCommand(dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()
	return cmd.Run()
}

func runDockerShellScript(repoRoot, platform, image, shellScript string, extraEnv map[string]string) error {
	args := []string{
		"run",
		"--rm",
		"--platform", platform,
		"-v", repoRoot + ":" + dockerWorkspaceRoot,
		"-v", dockerVolumeName(image, "gocache") + ":/root/.cache/go-build",
		"-v", dockerVolumeName(image, "gomod") + ":/root/go/pkg/mod",
		"-w", dockerWorkspaceRoot,
	}
	for key, value := range extraEnv {
		args = append(args, "-e", key+"="+value)
	}
	args = append(args, image, "bash", "-lc", shellScript)
	return runCommand(repoRoot, "docker", args...)
}

func ensureDockerPlatformSupport(repoRoot, platform string, installEmulation bool) error {
	if dockerPlatformWorks(repoRoot, platform) {
		return nil
	}

	goarch := strings.TrimPrefix(platform, "linux/")
	if !installEmulation {
		return fmt.Errorf("docker cannot run %s containers; rerun with -install-docker-emulation to register %s binfmt support using a privileged container", platform, goarch)
	}
	fmt.Printf("register docker emulation for %s\n", platform)
	if err := runCommand(repoRoot, "docker", "run", "--privileged", "--rm", "tonistiigi/binfmt", "--install", goarch); err != nil {
		return fmt.Errorf("register docker emulation for %s: %w", platform, err)
	}
	if !dockerPlatformWorks(repoRoot, platform) {
		return fmt.Errorf("docker emulation registration completed, but %s containers still cannot run", platform)
	}
	return nil
}

func dockerPlatformWorks(repoRoot, platform string) bool {
	cmd := exec.Command("docker", "run", "--rm", "--platform", platform, "ubuntu:22.04", "true")
	cmd.Dir = repoRoot
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run() == nil
}

func ensureDockerBuilderImage(repoRoot, platform, image, dockerfile string, rebuild bool) error {
	if !rebuild && dockerImageExists(repoRoot, image) {
		fmt.Printf("reuse docker builder image %s\n", image)
		return nil
	}

	fmt.Printf("build docker builder image %s\n", image)
	return runDockerBuild(repoRoot, platform, image, dockerfile)
}

func dockerImageExists(repoRoot, image string) bool {
	cmd := exec.Command("docker", "image", "inspect", image)
	cmd.Dir = repoRoot
	return cmd.Run() == nil
}

func runDockerBuild(repoRoot, platform, image, dockerfile string) error {
	cmd := exec.Command("docker", "build", "--platform", platform, "-t", image, "-")
	cmd.Dir = repoRoot
	cmd.Stdin = strings.NewReader(dockerfile)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = os.Environ()
	return cmd.Run()
}

func dockerVolumeName(image, suffix string) string {
	var builder strings.Builder
	for _, r := range image {
		switch {
		case r >= 'a' && r <= 'z':
			builder.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			builder.WriteRune(r)
		case r >= '0' && r <= '9':
			builder.WriteRune(r)
		case r == '.', r == '_', r == '-':
			builder.WriteRune(r)
		default:
			builder.WriteRune('-')
		}
	}
	builder.WriteRune('-')
	builder.WriteString(suffix)
	return builder.String()
}

func dockerWorkspacePath(repoRoot, hostPath string) (string, error) {
	relativePath, err := filepath.Rel(repoRoot, hostPath)
	if err != nil {
		return "", err
	}
	if relativePath == "." {
		return dockerWorkspaceRoot, nil
	}
	if strings.HasPrefix(relativePath, ".."+string(filepath.Separator)) || relativePath == ".." || filepath.IsAbs(relativePath) {
		return "", fmt.Errorf("path %s is outside docker workspace %s", hostPath, repoRoot)
	}
	return path.Join(dockerWorkspaceRoot, filepath.ToSlash(relativePath)), nil
}

func verifyBuiltDesktopArtifact(artifactPath string) error {
	return verifyNonEmptyFile(artifactPath + ".zip")
}

func cleanupDesktopBuildIntermediates(desktopDir string) error {
	entries, err := os.ReadDir(desktopDir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() {
			if strings.HasSuffix(name, ".app") {
				if err := os.RemoveAll(filepath.Join(desktopDir, name)); err != nil {
					return err
				}
			}
			continue
		}
		if name == "MD5SUMS" {
			continue
		}
		if strings.HasSuffix(name, ".zip") || strings.HasSuffix(name, ".dmg") {
			continue
		}
		if err := os.Remove(filepath.Join(desktopDir, name)); err != nil {
			return err
		}
	}
	return nil
}

func writeMD5SumsFile(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	type checksumEntry struct {
		name string
		sum  string
	}
	checksums := make([]checksumEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == "MD5SUMS" {
			continue
		}
		sum, sumErr := fileMD5(filepath.Join(dir, name))
		if sumErr != nil {
			return sumErr
		}
		checksums = append(checksums, checksumEntry{name: name, sum: sum})
	}

	sort.Slice(checksums, func(left, right int) bool {
		return checksums[left].name < checksums[right].name
	})

	var builder strings.Builder
	for _, checksum := range checksums {
		builder.WriteString(checksum.sum)
		builder.WriteString("  ")
		builder.WriteString(checksum.name)
		builder.WriteString("\n")
	}

	return os.WriteFile(filepath.Join(dir, "MD5SUMS"), []byte(builder.String()), 0o644)
}

func fileMD5(filePath string) (string, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hash := md5.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

func verifyNonEmptyFile(filePath string) error {
	info, err := os.Stat(filePath)
	if err != nil {
		return fmt.Errorf("expected build artifact %s: %w", filePath, err)
	}
	if info.IsDir() {
		return fmt.Errorf("expected build artifact %s, got directory", filePath)
	}
	if info.Size() == 0 {
		return fmt.Errorf("expected build artifact %s to be non-empty", filePath)
	}
	return nil
}

func runOutput(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func shellQuote(input string) string {
	return "'" + strings.ReplaceAll(input, "'", `'"'"'`) + "'"
}

func psQuote(input string) string {
	return "'" + strings.ReplaceAll(input, "'", "''") + "'"
}

func sanitizePathSegment(input string) string {
	var builder strings.Builder
	for _, r := range input {
		switch {
		case r >= 'a' && r <= 'z':
			builder.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			builder.WriteRune(r)
		case r >= '0' && r <= '9':
			builder.WriteRune(r)
		case r == '.', r == '-', r == '_':
			builder.WriteRune(r)
		default:
			builder.WriteRune('_')
		}
	}
	return strings.Trim(builder.String(), "._")
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
