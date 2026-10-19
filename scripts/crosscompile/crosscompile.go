package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/md5"
	"crypto/sha256"
	"debug/macho"
	_ "embed"
	"encoding/base64"
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
	"strconv"
	"strings"
	"time"

	"github.com/matveynator/sitebrush/v2/pkg/appupdate"
)

type buildMode string

const (
	modeAll        buildMode = "all"
	modeServerApp  buildMode = "server-app"
	modeDesktopApp buildMode = "desktop-app"
)

const (
	dockerBuilderImageVersion = "v1"
	dockerWorkspaceRoot       = "/workspace"
	windowsDesktopPlatform    = "linux/amd64"
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
	goVersion           string
	macOSSDK            string
	macOSStage          string
	rebuildDockerImages bool
	targetFilter        buildTargetFilter
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
		programName         = flag.String("program", "sitebrush", "program name used in binary names")
		versionFlag         = flag.String("version", "", "version folder under binaries/; defaults to git rev-list count, then GITHUB_RUN_NUMBER, then git describe")
		signUpdates         = flag.Bool("sign-updates", false, "sign the complete server matrix; automatic for official -sync destinations")
		releaseKeyFile      = flag.String("release-key", "", "private signing key file; defaults to the SiteBrush user configuration directory")
		outputRoot          = flag.String("output-dir", "binaries", "root output directory for generated artifacts")
		modeFlag            = flag.String("mode", string(modeAll), "build scope: all, server-app, or desktop-app")
		targetOS            = flag.String("os", "", "optional GOOS target filter, for example linux")
		targetArch          = flag.String("arch", "", "optional GOARCH target filter, for example amd64")
		rebuildDockerImages = flag.Bool("rebuild-docker-images", false, "rebuild cached Docker builder images before Docker-based desktop builds")
		macOSSDK            = flag.String("macos-sdk", "", "macOS SDK tar archive; defaults to the installed Xcode SDK on macOS")
		macOSStage          = flag.String("macos-stage", "all", "macOS desktop stage: all, build, or package")
		syncTargets         syncDestinationFlags
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

	if err := validateMacOSStage(*macOSStage, mode, targetFilter); err != nil {
		fatalf("macOS stage: %v", err)
	}
	goVersion, err := projectGoVersion(repoRoot)
	if err != nil {
		fatalf("resolve Go version: %v", err)
	}
	if keyText := strings.TrimSpace(os.Getenv("SITEBRUSH_NETCHAN_TLS_PUBLIC_KEY_SITEBRUSH_COM")); keyText != "" {
		publicKey, err := base64.StdEncoding.DecodeString(keyText)
		if err != nil || len(publicKey) != ed25519.PublicKeySize {
			fatalf("SITEBRUSH_NETCHAN_TLS_PUBLIC_KEY_SITEBRUSH_COM must be a base64 Ed25519 public key")
		}
	}

	version := strings.TrimSpace(*versionFlag)
	if version == "" {
		version = defaultVersionLabel(repoRoot)
	}
	version = sanitizePathSegment(version)
	if version == "" {
		fatalf("resolved empty version label")
	}

	// Official publication is validated before any expensive desktop/Docker work.
	signServerUpdates := *signUpdates || (mode != modeDesktopApp && officialReleasePublication(syncTargets))
	var signingKey ed25519.PrivateKey
	if signServerUpdates {
		if mode == modeDesktopApp || targetFilter.active() || *programName != "sitebrush" {
			fatalf("signed updates require the complete SiteBrush server matrix")
		}
		trusted, err := appupdate.TrustedKey()
		if err != nil {
			fatalf("release trust anchor: %v", err)
		}
		signingKey, err = loadReleaseSigningKey(*releaseKeyFile, os.Getenv("SITEBRUSH_RELEASE_PRIVATE_KEY"), trusted)
		if err != nil {
			fatalf("release signing key: %v", err)
		}
		if number, err := strconv.ParseUint(version, 10, 64); err != nil || number == 0 {
			fatalf("signed update version must be numeric")
		}
	}

	artifactsRoot := *outputRoot
	if !filepath.IsAbs(artifactsRoot) {
		artifactsRoot = filepath.Join(repoRoot, artifactsRoot)
	}
	outputDir := filepath.Join(artifactsRoot, version)
	// Packaging consumes the binaries transferred from the Docker build stage.
	if *macOSStage != "package" {
		if err := os.RemoveAll(outputDir); err != nil {
			fatalf("clean output dir: %v", err)
		}
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
			goVersion:           goVersion,
			macOSSDK:            *macOSSDK,
			macOSStage:          *macOSStage,
			rebuildDockerImages: *rebuildDockerImages,
			targetFilter:        targetFilter,
		}); err != nil {
			fatalf("desktop-app build: %v", err)
		}
	}

	if signServerUpdates {
		commit, err := runOutput(repoRoot, "git", "rev-parse", "HEAD")
		if err != nil {
			fatalf("resolve release commit: %v", err)
		}
		if err := appupdate.Publish(filepath.Join(outputDir, "server-app"), version, strings.TrimSpace(commit), signingKey); err != nil {
			fatalf("sign update manifest: %v", err)
		}
	}

	// Intermediate binaries must never replace the published latest build.
	if *macOSStage == "build" {
		fmt.Println("macOS Docker compilation complete; run -macos-stage package on macOS")
		return
	}
	if err := updateLatestSymlink(artifactsRoot, version); err != nil {
		fatalf("update latest symlink: %v", err)
	}

	destinations := syncPublicationDestinations(syncTargets)
	for _, destination := range destinations {
		if err := syncArtifacts(repoRoot, artifactsRoot, version, destination.host, destination.base); err != nil {
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
			ldflags:    releaseLinkerFlags(version),
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
	if options.targetFilter.matches("darwin", "universal") && (runtime.GOOS == "darwin" || options.targetFilter.goos == "darwin") {
		if err := buildMacOSDesktopArtifacts(repoRoot, desktopDir, programName, version, options); err != nil {
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

	if options.macOSStage == "build" {
		return nil
	}
	if err := cleanupDesktopBuildIntermediates(desktopDir); err != nil {
		return err
	}
	return writeMD5SumsFile(desktopDir)
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
	checkedArchitecture := ""
	for _, target := range matchingTargets {
		if target.goarch == checkedArchitecture {
			continue
		}
		builderImage := linuxDesktopDockerImage(target.goarch, target.variant, options.goVersion)
		if err := ensureDockerPlatformSupport(repoRoot, "linux/"+target.goarch, builderImage); err != nil {
			return false, err
		}
		checkedArchitecture = target.goarch
	}

	for _, target := range matchingTargets {
		if err := buildLinuxDesktopArtifactInDocker(repoRoot, desktopDir, programName, version, target.goarch, target.variant, options); err != nil {
			return false, err
		}
	}

	return true, nil
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
	probeImage := windowsDesktopDockerImage(matchingArchitectures[0], options.goVersion)
	if err := ensureDockerPlatformSupport(repoRoot, windowsDesktopPlatform, probeImage); err != nil {
		return false, err
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
	dockerImage := linuxDesktopDockerImage(goarch, variant, options.goVersion)
	if err := ensureDockerBuilderImage(repoRoot, dockerPlatform, dockerImage, linuxDesktopDockerfile(goarch, variant, options.goVersion), options.rebuildDockerImages); err != nil {
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
	dockerImage := windowsDesktopDockerImage(goarch, options.goVersion)
	if err := ensureDockerBuilderImage(repoRoot, windowsDesktopPlatform, dockerImage, windowsDesktopDockerfile(goarch, options.goVersion), options.rebuildDockerImages); err != nil {
		return err
	}
	dockerScript := windowsDesktopDockerScript(containerArtifactPath, artifactName, goarch, version)
	fmt.Printf("build windows/%s -> %s\n", goarch, artifactPath)
	if err := runDockerShellScript(repoRoot, windowsDesktopPlatform, dockerImage, dockerScript, nil); err != nil {
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

func linuxDesktopDockerImage(goarch, variant, goVersion string) string {
	return fmt.Sprintf("sitebrush-crosscompile:linux-%s-%s-go%s-%s", goarch, variant, goVersion, dockerBuilderImageVersion)
}

func windowsDesktopDockerImage(goarch, goVersion string) string {
	return fmt.Sprintf("sitebrush-crosscompile:windows-%s-go%s-%s", goarch, goVersion, dockerBuilderImageVersion)
}

func linuxDesktopDockerfile(goarch, variant, goVersion string) string {
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
`, goarch, linuxDesktopBaseImage(variant), webkitPackage, goVersion, goarch)
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
  -ldflags %s \
  -o %s
chmod +x %s
(
  cd %s
  zip -q -X %s %s
)
`, shellQuote(artifactDir), goarch, shellQuote(strings.Join(releaseLinkerFlags(version), " ")), shellQuote(artifactPath), shellQuote(artifactPath), shellQuote(artifactDir), shellQuote(artifactName+".zip"), shellQuote(artifactName))
}

func windowsDesktopDockerfile(goarch, goVersion string) string {
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
`, installPackages, installCrossCompiler, goVersion)
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
  -ldflags %s \
  -o %s

(
  cd %s
  zip -q -X %s %s
)
`, resourceArch, goarch, goarch, shellQuote(artifactDir), goarch, cc, cxx, shellQuote("-H windowsgui "+strings.Join(releaseLinkerFlags(version), " ")), shellQuote(artifactPath), shellQuote(artifactDir), shellQuote(artifactName+".zip"), shellQuote(artifactName))
}

func releaseLinkerFlags(version string) []string {
	flags := []string{"-s", "-w", "-X", "main.CompileVersion=" + version}
	if publicKey := strings.TrimSpace(os.Getenv("SITEBRUSH_NETCHAN_TLS_PUBLIC_KEY_SITEBRUSH_COM")); publicKey != "" {
		flags = append(flags, "-X", "main.sitebrushComNetChanTLSPublicKey="+publicKey)
	}
	return flags
}

func buildGoBinary(repoRoot, outputPath string, request buildRequest) error {
	if request.cgoEnabled != "0" {
		return errors.New("CGO compilation is restricted to desktop Docker builders")
	}
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

	// Keep one staged copy of the app so the runner has room for hdiutil's image.
	if err := os.Rename(appDir, filepath.Join(stagingDir, filepath.Base(appDir))); err != nil {
		return "", err
	}
	if err := runCommand(desktopDir, "ln", "-s", "/Applications", filepath.Join(stagingDir, "Applications")); err != nil {
		return "", err
	}

	dmgPath := filepath.Join(desktopDir, fmt.Sprintf("%s_darwin_universal_desktop.dmg", programName))
	if err := os.RemoveAll(dmgPath); err != nil {
		return "", err
	}
	imageSizeMiB, err := macOSDMGSizeMiB(stagingDir)
	if err != nil {
		return "", err
	}
	if err := runCommand(desktopDir, "hdiutil", "create", "-volname", fmt.Sprintf("%s %s", programName, version), "-srcfolder", stagingDir, "-size", strconv.FormatInt(imageSizeMiB, 10)+"m", "-ov", "-format", "UDZO", dmgPath); err != nil {
		return "", fmt.Errorf("create macOS DMG: %w", err)
	}
	return dmgPath, nil
}

// hdiutil's automatic sizing can undercount a cross-compiled universal app.
// Allow room for both the app and filesystem metadata inside the image.
func macOSDMGSizeMiB(stagingDir string) (int64, error) {
	var contentBytes int64
	err := filepath.WalkDir(stagingDir, func(filename string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		information, err := entry.Info()
		if err != nil {
			return err
		}
		contentBytes += information.Size()
		return nil
	})
	if err != nil {
		return 0, err
	}
	return contentBytes/(1<<20)*2 + 128, nil
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
  <string>13.0</string>
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
	version = strings.TrimSpace(version)
	if version == "" || sanitizePathSegment(version) != version {
		return fmt.Errorf("invalid release version path %q", version)
	}
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
	if !validSyncHost(host) {
		return syncDestination{}, errors.New("sync target host is invalid")
	}
	if !validRemoteSyncBase(base) {
		return syncDestination{}, errors.New("sync target remote base is invalid")
	}
	return syncDestination{host: host, base: base}, nil
}

func validSyncHost(host string) bool {
	if host == "" || strings.HasPrefix(host, "-") || len(host) > 255 {
		return false
	}
	for _, character := range host {
		if character >= 'a' && character <= 'z' ||
			character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' ||
			character == '.' || character == '-' || character == '_' || character == '@' {
			continue
		}
		return false
	}
	return true
}

func validRemoteSyncBase(base string) bool {
	if base == "" || !strings.HasPrefix(base, "/") || len(base) > 4096 {
		return false
	}
	for _, character := range base {
		if character < ' ' || character == 0x7f {
			return false
		}
	}
	return true
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
	if version, err := runOutput(repoRoot, "git", "rev-list", "--count", "HEAD"); err == nil {
		version = strings.TrimSpace(version)
		if version != "" {
			return version
		}
	}
	if version := strings.TrimSpace(os.Getenv("GITHUB_RUN_NUMBER")); version != "" {
		return version
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
		// Module archives are immutable and Go owns their download locks. Sharing
		// them avoids downloading the same source for every target and SDK image.
		"-v", "sitebrush-crosscompile-gomod:/root/go/pkg/mod",
		"-w", dockerWorkspaceRoot,
	}
	for key, value := range extraEnv {
		args = append(args, "-e", key+"="+value)
	}
	args = append(args, image, "bash", "-lc", shellScript)
	return runCommand(repoRoot, "docker", args...)
}

func ensureDockerPlatformSupport(repoRoot, platform, builderImage string) error {
	goarch := strings.TrimPrefix(platform, "linux/")
	exists, err := dockerImageExists(repoRoot, builderImage)
	if err != nil {
		return err
	}
	if exists {
		works, err := dockerPlatformWorks(repoRoot, platform, builderImage)
		if err != nil {
			return err
		}
		if works {
			return nil
		}
		if err := installDockerPlatformEmulation(repoRoot, platform, goarch); err != nil {
			return err
		}
		works, err = dockerPlatformWorks(repoRoot, platform, builderImage)
		if err != nil {
			return err
		}
		if !works {
			return fmt.Errorf("docker emulation registration completed, but cached builder %s still cannot run as %s", builderImage, platform)
		}
		return nil
	}

	nativePlatform, err := dockerNativePlatform(repoRoot)
	if err != nil {
		return err
	}
	if nativePlatform == platform {
		return nil
	}
	return installDockerPlatformEmulation(repoRoot, platform, goarch)
}

func installDockerPlatformEmulation(repoRoot, platform, goarch string) error {
	fmt.Printf("register docker emulation for %s\n", platform)
	if err := runCommand(repoRoot, "docker", "run", "--privileged", "--rm", "tonistiigi/binfmt", "--install", goarch); err != nil {
		return fmt.Errorf("register docker emulation for %s: %w", platform, err)
	}
	return nil
}

func dockerPlatformWorks(repoRoot, platform, builderImage string) (bool, error) {
	fmt.Printf("check docker execution for %s using %s\n", platform, builderImage)
	cmd := exec.Command("docker", dockerPlatformProbeArgs(platform, builderImage)...)
	cmd.Dir = repoRoot
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	err := runDockerProbe(cmd, 30*time.Second)
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return false, err
	}
	return err == nil, nil
}

func dockerPlatformProbeArgs(platform, builderImage string) []string {
	return []string{"run", "--rm", "--pull", "never", "--platform", platform, builderImage, "true"}
}

func dockerNativePlatform(repoRoot string) (string, error) {
	fmt.Println("check Docker daemon architecture")
	cmd := exec.Command("docker", "info", "--format", "{{.Architecture}}")
	cmd.Dir = repoRoot
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = os.Stderr
	err := runDockerProbe(cmd, 30*time.Second)
	architecture := output.String()
	if err != nil {
		return "", fmt.Errorf("resolve Docker daemon architecture: %w", err)
	}
	return "linux/" + normalizeDockerArchitecture(strings.TrimSpace(architecture)), nil
}

func normalizeDockerArchitecture(architecture string) string {
	switch architecture {
	case "x86_64":
		return "amd64"
	case "aarch64":
		return "arm64"
	default:
		return architecture
	}
}

func ensureDockerBuilderImage(repoRoot, platform, image, dockerfile string, rebuild bool) error {
	if !rebuild {
		exists, err := dockerImageExists(repoRoot, image)
		if err != nil {
			return err
		}
		if exists {
			fmt.Printf("reuse docker builder image %s\n", image)
			return nil
		}
	}

	fmt.Printf("build docker builder image %s\n", image)
	return runDockerBuild(repoRoot, platform, image, dockerfile)
}

func dockerImageExists(repoRoot, image string) (bool, error) {
	fmt.Printf("check docker builder image %s\n", image)
	cmd := exec.Command("docker", "image", "inspect", image)
	cmd.Dir = repoRoot
	err := runDockerProbe(cmd, 30*time.Second)
	if errors.Is(err, os.ErrDeadlineExceeded) {
		return false, err
	}
	return err == nil, nil
}

// Readiness probes never pull or compile. A stalled daemon must fail before
// emulation registration or publication; actual builds stream their progress.
func runDockerProbe(cmd *exec.Cmd, timeout time.Duration) error {
	cmd.WaitDelay = time.Second
	if err := cmd.Start(); err != nil {
		return err
	}
	finished := make(chan error, 1)
	go func() { finished <- cmd.Wait() }()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-finished:
		return err
	case <-timer.C:
		_ = cmd.Process.Kill()
		<-finished
		return fmt.Errorf("Docker check %q did not respond within %s; check Docker Desktop and docker info: %w", strings.Join(cmd.Args[1:], " "), timeout, os.ErrDeadlineExceeded)
	}
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

// Local and GitHub publication use the same commit-count identity. A new
// committed release advances the version regardless of the build machine.
func officialReleasePublication(destinations []syncDestination) bool {
	for _, destination := range destinations {
		host := destination.host
		if separator := strings.LastIndex(host, "@"); separator >= 0 {
			host = host[separator+1:]
		}
		if host == "sitebrush.com" || host == "sitebrush.ru" {
			return true
		}
	}
	return false
}

func loadReleaseSigningKey(filename, encoded string, trusted ed25519.PublicKey) (ed25519.PrivateKey, error) {
	if strings.TrimSpace(encoded) == "" {
		if filename == "" {
			configuration, err := os.UserConfigDir()
			if err != nil {
				return nil, err
			}
			filename = filepath.Join(configuration, "sitebrush", "release-ed25519.key")
		}
		file, err := os.Open(filename)
		if err != nil {
			return nil, fmt.Errorf("set SITEBRUSH_RELEASE_PRIVATE_KEY or provision %s: %w", filename, err)
		}
		payload, readErr := io.ReadAll(io.LimitReader(file, 4097))
		closeErr := file.Close()
		if readErr != nil {
			return nil, readErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if len(payload) > 4096 {
			return nil, errors.New("release key file exceeds size limit")
		}
		encoded = string(payload)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encoded))
	if err != nil || len(decoded) != ed25519.PrivateKeySize {
		return nil, errors.New("expected a base64 Ed25519 private signing key")
	}
	key := ed25519.PrivateKey(decoded)
	if !key.Public().(ed25519.PublicKey).Equal(trusted) || !ed25519.NewKeyFromSeed(key.Seed()).Equal(key) {
		return nil, errors.New("release key does not match the embedded public trust anchor")
	}
	return key, nil
}

// macOS SDK ownership and Docker compilation stages.

const osxcrossCommit = "27d21e4977c9751d01199c7a226a6faf494c3dd9"

//go:embed prepare_osxcross_sdk.sh
var prepareOSXCrossSDKScript string

// The module directive owns the compiler version for CI and every Docker image.
// Keeping it as a per-build value avoids a mutable process-wide configuration.
func projectGoVersion(repoRoot string) (string, error) {
	moduleBytes, err := os.ReadFile(filepath.Join(repoRoot, "go.mod"))
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(moduleBytes), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "go" {
			continue
		}
		version := fields[1]
		parts := strings.Split(version, ".")
		if len(parts) != 3 {
			return "", errors.New("go.mod must pin a complete Go release, including the patch version")
		}
		for _, part := range parts {
			if part == "" || strings.Trim(part, "0123456789") != "" {
				return "", errors.New("invalid Go release in go.mod")
			}
		}
		return version, nil
	}
	return "", errors.New("missing Go directive in go.mod")
}

func validateMacOSStage(stage string, mode buildMode, filter buildTargetFilter) error {
	if stage == "all" {
		return nil
	}
	if stage != "build" && stage != "package" {
		return errors.New("expected all, build, or package")
	}
	if mode != modeDesktopApp || filter.goos != "darwin" || (filter.goarch != "" && filter.goarch != "universal") {
		return errors.New("separate macOS stages require -mode desktop-app -os darwin and optional -arch universal")
	}
	return nil
}

func buildMacOSDesktopArtifacts(repoRoot, desktopDir, programName, version string, options desktopBuildOptions) error {
	stage := options.macOSStage
	if stage == "" {
		stage = "all"
	}
	if stage != "build" && runtime.GOOS != "darwin" {
		return errors.New("DMG packaging requires macOS; use -macos-stage build for Docker compilation on Linux")
	}
	if stage != "package" {
		if err := buildMacOSDesktopInDocker(repoRoot, desktopDir, programName, version, options); err != nil {
			return err
		}
	}
	if stage == "build" {
		return nil
	}
	universalBinary := filepath.Join(desktopDir, desktopArtifactName(programName, "darwin", "universal", ""))
	if err := verifyMacOSUniversalBinary(universalBinary); err != nil {
		return err
	}
	if err := os.Chmod(universalBinary, 0o755); err != nil {
		return err
	}
	dmgPath, err := packageMacOSDesktopDMG(desktopDir, programName, universalBinary, version)
	if err != nil {
		return err
	}
	fmt.Printf("created %s\n", dmgPath)
	return verifyNonEmptyFile(dmgPath)
}

func buildMacOSDesktopInDocker(repoRoot, desktopDir, programName, version string, options desktopBuildOptions) error {
	if !commandExists("docker") {
		return errors.New("docker is required to build macOS desktop binaries")
	}
	archivePath, cleanup, err := macOSSDKArchive(repoRoot, options.macOSSDK)
	if err != nil {
		return err
	}
	defer cleanup()
	archive, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	digest := sha256.New()
	_, copyErr := io.Copy(digest, archive)
	closeErr := archive.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		return err
	}
	// Both the SDK and the toolchain revision are part of the image identity.
	goarch := runtime.GOARCH
	if goarch != "arm64" {
		goarch = "amd64"
	}
	platform := "linux/" + goarch
	image := fmt.Sprintf("sitebrush-crosscompile:darwin-%s-go%s-sdk%x-%s-rt18-%s", goarch, options.goVersion, digest.Sum(nil)[:12], osxcrossCommit[:12], dockerBuilderImageVersion)
	if err := ensureDockerPlatformSupport(repoRoot, platform, image); err != nil {
		return err
	}
	exists, err := dockerImageExists(repoRoot, image)
	if err != nil {
		return err
	}
	if options.rebuildDockerImages || !exists {
		if err := buildMacOSDockerImage(repoRoot, platform, image, archivePath, macOSDesktopDockerfile(goarch, options.goVersion)); err != nil {
			return err
		}
	}
	containerDirectory, err := dockerWorkspacePath(repoRoot, desktopDir)
	if err != nil {
		return err
	}
	if err := runDockerShellScript(repoRoot, platform, image, macOSDesktopDockerScript(containerDirectory, programName, version), nil); err != nil {
		return err
	}
	return verifyMacOSUniversalBinary(filepath.Join(desktopDir, desktopArtifactName(programName, "darwin", "universal", "")))
}

func macOSSDKArchive(repoRoot, suppliedPath string) (string, func(), error) {
	if suppliedPath != "" {
		archivePath, err := filepath.Abs(suppliedPath)
		if err != nil {
			return "", nil, err
		}
		if err := verifyNonEmptyFile(archivePath); err != nil {
			return "", nil, fmt.Errorf("macOS SDK archive: %w", err)
		}
		return archivePath, func() {}, nil
	}
	if runtime.GOOS != "darwin" {
		return "", nil, errors.New("-macos-sdk must identify a packaged Xcode SDK on non-macOS hosts")
	}
	sdkPath, err := runOutput(repoRoot, "xcrun", "--sdk", "macosx", "--show-sdk-path")
	if err != nil {
		return "", nil, fmt.Errorf("locate Xcode SDK: %w", err)
	}
	sdkPath, err = filepath.EvalSymlinks(strings.TrimSpace(sdkPath))
	if err != nil {
		return "", nil, err
	}
	temporaryDirectory, err := os.MkdirTemp("", "sitebrush-macos-sdk-")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(temporaryDirectory) }
	archivePath := filepath.Join(temporaryDirectory, filepath.Base(sdkPath)+".tar.gz")
	if err := archiveMacOSSDK(sdkPath, archivePath); err != nil {
		cleanup()
		return "", nil, err
	}
	return archivePath, cleanup, nil
}

// Canonical metadata and a timestamp-free gzip header keep the SDK cache key
// stable across repeated builds and exclude host-specific extended attributes.
func archiveMacOSSDK(sdkPath, archivePath string) error {
	archive, err := os.Create(archivePath)
	if err != nil {
		return err
	}
	compressed := gzip.NewWriter(archive)
	writer := tar.NewWriter(compressed)
	walkErr := filepath.WalkDir(sdkPath, func(filename string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		information, err := entry.Info()
		if err != nil {
			return err
		}
		linkTarget := ""
		if information.Mode()&os.ModeSymlink != 0 {
			linkTarget, err = os.Readlink(filename)
			if err != nil {
				return err
			}
		}
		header, err := tar.FileInfoHeader(information, linkTarget)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(filepath.Dir(sdkPath), filename)
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(relative)
		header.Uid, header.Gid = 0, 0
		header.Uname, header.Gname = "", ""
		header.AccessTime, header.ChangeTime = time.Time{}, time.Time{}
		if err := writer.WriteHeader(header); err != nil {
			return err
		}
		if !information.Mode().IsRegular() {
			return nil
		}
		source, err := os.Open(filename)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(writer, source)
		return errors.Join(copyErr, source.Close())
	})
	return errors.Join(walkErr, writer.Close(), compressed.Close(), archive.Close())
}

func macOSDesktopDockerfile(goarch, goVersion string) string {
	return fmt.Sprintf(`FROM --platform=linux/%s ubuntu:24.04
SHELL ["/bin/bash", "-o", "pipefail", "-c"]
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y ca-certificates curl git clang llvm lld cmake make g++ patch python3 xz-utils libssl-dev libxml2-dev zlib1g-dev && rm -rf /var/lib/apt/lists/*
RUN curl -fsSL "https://go.dev/dl/go%s.linux-%s.tar.gz" | tar -C /usr/local -xz
RUN git clone https://github.com/tpoechtrager/osxcross.git /opt/osxcross && cd /opt/osxcross && git checkout --detach %s
ENV PATH="/usr/lib/llvm-18/bin:/usr/local/go/bin:${PATH}"
COPY sdk.tar.gz /opt/sdk.tar.gz
COPY prepare_osxcross_sdk.sh /opt/prepare_osxcross_sdk.sh
RUN bash /opt/prepare_osxcross_sdk.sh /opt/sdk.tar.gz /opt/sdk /opt/osxcross/tarballs && \
    cd /opt/osxcross && UNATTENDED=1 BUILD_FLAVOR=llvm ENABLE_ARCHS="x86_64 arm64" OSX_VERSION_MIN=13.0 TARGET_DIR=/opt/osxcross/target ./build.sh && \
    rm -rf /opt/sdk /opt/sdk.tar.gz /opt/osxcross/tarballs/*
ENV PATH="/opt/osxcross/target/bin:${PATH}" GOTOOLCHAIN=local
# WebView's @available checks need compiler-rt's Darwin version-check builtins.
# Build the upstream implementation, pinned by digest, for both target CPUs.
RUN curl -fsSL https://raw.githubusercontent.com/llvm/llvm-project/llvmorg-18.1.8/compiler-rt/lib/builtins/os_version_check.c -o /opt/osxcross/os_version_check.c && \
    echo "8ed75fa38482b18f17797c1530739688b6939b7c6495b2ffc1361114b1184c1c  /opt/osxcross/os_version_check.c" | sha256sum -c - && \
    for architecture in x86_64 arm64; do \
      compiler=(/opt/osxcross/target/bin/"$architecture"-apple-darwin*-clang); \
      "${compiler[0]}" -c -O2 -fPIC -mmacosx-version-min=13.0 /opt/osxcross/os_version_check.c -o "/tmp/version-$architecture.o"; \
      llvm-ar rcs "/tmp/runtime-$architecture.a" "/tmp/version-$architecture.o"; \
    done && \
    runtime_directory="$(clang -print-resource-dir)/lib/darwin" && mkdir -p "$runtime_directory" && \
    lipo -create /tmp/runtime-x86_64.a /tmp/runtime-arm64.a -output "$runtime_directory/libclang_rt.osx.a" && \
    rm /tmp/version-*.o /tmp/runtime-*.a
`, goarch, goVersion, goarch, osxcrossCommit)
}

func macOSDesktopDockerScript(artifactDirectory, programName, version string) string {
	return fmt.Sprintf(`set -euo pipefail
mkdir -p %s
for target in amd64 arm64; do
  compiler_arch=x86_64
  if [ "$target" = arm64 ]; then compiler_arch=arm64; fi
  compiler=()
  for compiler_path in /opt/osxcross/target/bin/"$compiler_arch"-apple-darwin*-clang; do
    if [[ "$compiler_path" != *-cmake-clang ]]; then compiler+=("$compiler_path"); fi
  done
  test "${#compiler[@]}" -eq 1
  CGO_ENABLED=1 GOOS=darwin GOARCH="$target" CC="${compiler[0]}" CXX="${compiler[0]}++" \
    MACOSX_DEPLOYMENT_TARGET=13.0 GOFLAGS="-trimpath -buildvcs=false" \
    go build -tags desktop -ldflags %s -o %s"${target}_desktop" .
done
lipo -create %s %s -output %s
lipo %s -verify_arch x86_64 arm64
chmod +x %s
`, shellQuote(artifactDirectory), shellQuote(strings.Join(releaseLinkerFlags(version), " ")), shellQuote(path.Join(artifactDirectory, programName+"_darwin_")),
		shellQuote(path.Join(artifactDirectory, desktopArtifactName(programName, "darwin", "amd64", ""))),
		shellQuote(path.Join(artifactDirectory, desktopArtifactName(programName, "darwin", "arm64", ""))),
		shellQuote(path.Join(artifactDirectory, desktopArtifactName(programName, "darwin", "universal", ""))),
		shellQuote(path.Join(artifactDirectory, desktopArtifactName(programName, "darwin", "universal", ""))),
		shellQuote(path.Join(artifactDirectory, desktopArtifactName(programName, "darwin", "universal", ""))))
}

// Docker only receives a bounded build context containing the Dockerfile and SDK.
// The SDK remains a local build input and is excluded from release assembly.
func buildMacOSDockerImage(repoRoot, platform, image, archivePath, dockerfile string) error {
	buildDirectory, err := os.MkdirTemp("", "sitebrush-macos-builder-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(buildDirectory)
	if err := os.WriteFile(filepath.Join(buildDirectory, "Dockerfile"), []byte(dockerfile), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(buildDirectory, "prepare_osxcross_sdk.sh"), []byte(prepareOSXCrossSDKScript), 0o600); err != nil {
		return err
	}
	source, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer source.Close()
	destination, err := os.Create(filepath.Join(buildDirectory, "sdk.tar.gz"))
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(destination, source)
	if err := errors.Join(copyErr, destination.Close()); err != nil {
		return err
	}
	command := exec.Command("docker", "build", "--platform", platform, "-t", image, buildDirectory)
	command.Dir = repoRoot
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	return command.Run()
}

func verifyMacOSUniversalBinary(filename string) error {
	binary, err := macho.OpenFat(filename)
	if err != nil {
		return fmt.Errorf("read macOS universal binary: %w", err)
	}
	defer binary.Close()
	amd64, arm64 := false, false
	for _, architecture := range binary.Arches {
		if err := verifyMacOSMinimumVersion(architecture.File); err != nil {
			return err
		}
		switch architecture.Cpu {
		case macho.CpuAmd64:
			amd64 = true
		case macho.CpuArm64:
			arm64 = true
		}
	}
	if !amd64 || !arm64 {
		return errors.New("macOS universal binary must contain amd64 and arm64")
	}
	return nil
}

func verifyMacOSMinimumVersion(binary *macho.File) error {
	for _, load := range binary.Loads {
		encoded := load.Raw()
		if len(encoded) < 16 {
			continue
		}
		command := binary.ByteOrder.Uint32(encoded)
		versionOffset := 0
		switch command {
		case 0x32: // LC_BUILD_VERSION carries the platform before the minimum OS.
			if len(encoded) < 24 || binary.ByteOrder.Uint32(encoded[8:]) != 1 {
				continue
			}
			versionOffset = 12
		case 0x24: // LC_VERSION_MIN_MACOSX is emitted by older external linkers.
			versionOffset = 8
		default:
			continue
		}
		if binary.ByteOrder.Uint32(encoded[versionOffset:]) != 13<<16 {
			return errors.New("macOS binary must declare the supported minimum OS 13.0")
		}
		return nil
	}
	return errors.New("macOS binary does not declare its minimum OS")
}
