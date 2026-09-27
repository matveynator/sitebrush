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

func TestCrossCompileSelectsDesktopVariantAndDockerPlatformSafely(t *testing.T) {
	binDirectory := t.TempDir()
	pkgConfigPath := filepath.Join(binDirectory, "pkg-config")
	if err := os.WriteFile(pkgConfigPath, []byte("#!/bin/sh\ncase \"$PKG_VARIANT:$2\" in\ngtk40:webkit2gtk-4.0|gtk41:webkit2gtk-4.1) exit 0;;\n*) exit 1;;\nesac\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDirectory)
	for _, testCase := range []struct {
		variant string
		want    string
		found   bool
	}{
		{variant: "gtk40", want: "gtk40", found: true},
		{variant: "gtk41", want: "gtk41", found: true},
		{variant: "none", want: "", found: false},
	} {
		t.Run(testCase.variant, func(t *testing.T) {
			t.Setenv("PKG_VARIANT", testCase.variant)
			got, found := linuxDesktopVariant()
			if got != testCase.want || found != testCase.found {
				t.Fatalf("linux desktop variant = %q, %t; want %q, %t", got, found, testCase.want, testCase.found)
			}
		})
	}

	dockerPath := filepath.Join(binDirectory, "docker")
	if err := os.WriteFile(dockerPath, []byte("#!/bin/sh\nif [ \"$1\" = image ] && [ \"$2\" = inspect ]; then\n  if [ \"$DOCKER_MODE\" = image ]; then exit 0; fi\n  exit 1\nfi\nif [ \"$1\" = info ]; then\n  printf 'x86_64\\n'\n  exit 0\nfi\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, testCase := range []struct {
		name     string
		mode     string
		platform string
	}{
		{name: "cached builder", mode: "image", platform: "linux/arm64"},
		{name: "native daemon", mode: "missing", platform: "linux/amd64"},
		{name: "emulation registration", mode: "missing", platform: "linux/arm64"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Setenv("DOCKER_MODE", testCase.mode)
			if err := ensureDockerPlatformSupport(t.TempDir(), testCase.platform, "builder:latest"); err != nil {
				t.Fatalf("ensure Docker platform support: %v", err)
			}
		})
	}
}

func TestCrossCompilePackagesAndCleansDesktopArtifactsWithoutLeakingPaths(t *testing.T) {
	binDirectory := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDirectory, "cp"), []byte("#!/bin/sh\nlast=\"\"\nfor argument do last=\"$argument\"; done\ncase \"$1\" in\n-R) /bin/mkdir -p \"$last\";;\n*) printf binary > \"$last\";;\nesac\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDirectory, "ln"), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDirectory, "hdiutil"), []byte("#!/bin/sh\nlast=\"\"\nfor argument do last=\"$argument\"; done\nprintf dmg > \"$last\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDirectory)

	desktopDirectory := t.TempDir()
	binaryPath := filepath.Join(desktopDirectory, "binary")
	if err := os.WriteFile(binaryPath, []byte("binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	bundlePath := filepath.Join(desktopDirectory, "sitebrush.app")
	if err := createMacOSAppBundle(bundlePath, binaryPath, "safe-version"); err != nil {
		t.Fatalf("create macOS app bundle: %v", err)
	}
	if _, err := os.Stat(filepath.Join(bundlePath, "Contents", "MacOS", "sitebrush")); err != nil {
		t.Fatalf("app bundle executable missing: %v", err)
	}
	dmgPath, err := packageMacOSDesktopDMG(desktopDirectory, "sitebrush", binaryPath, "safe-version")
	if err != nil || dmgPath == "" {
		t.Fatalf("package macOS DMG = %q, %v", dmgPath, err)
	}
	if _, err := os.Stat(dmgPath); err != nil {
		t.Fatalf("DMG artifact missing: %v", err)
	}

	cleanupDirectory := t.TempDir()
	if err := os.Mkdir(filepath.Join(cleanupDirectory, "sitebrush.app"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"artifact.zip", "artifact.dmg", "MD5SUMS", "intermediate.tmp"} {
		if err := os.WriteFile(filepath.Join(cleanupDirectory, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := cleanupDesktopBuildIntermediates(cleanupDirectory); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(cleanupDirectory, "intermediate.tmp")); !os.IsNotExist(err) {
		t.Fatalf("intermediate desktop file remains, stat error=%v", err)
	}
	if _, err := os.Stat(filepath.Join(cleanupDirectory, "sitebrush.app")); !os.IsNotExist(err) {
		t.Fatalf("desktop bundle was not cleaned, stat error=%v", err)
	}

	latestRoot := filepath.Join(t.TempDir(), "binaries")
	if err := updateLatestSymlink(latestRoot, "release-42"); err != nil {
		t.Fatal(err)
	}
	latestTarget, err := os.Readlink(filepath.Join(latestRoot, "latest"))
	if err != nil || latestTarget != "release-42" {
		t.Fatalf("latest symlink = %q, %v", latestTarget, err)
	}
}

func TestCrossCompileSuccessfulExternalToolBranchesUseExpectedArguments(t *testing.T) {
	binDirectory := t.TempDir()
	for _, command := range []string{"sips", "iconutil", "go", "rsync", "ssh", "docker"} {
		commandPath := filepath.Join(binDirectory, command)
		var script string
		switch command {
		case "sips":
			script = "#!/bin/sh\nout=\"\"\nwhile [ $# -gt 0 ]; do if [ \"$1\" = --out ]; then shift; out=\"$1\"; fi; shift; done\nprintf png > \"$out\"\n"
		case "iconutil":
			script = "#!/bin/sh\nout=\"\"\nwhile [ $# -gt 0 ]; do if [ \"$1\" = -o ]; then shift; out=\"$1\"; fi; shift; done\nprintf icns > \"$out\"\n"
		case "go":
			script = "#!/bin/sh\nout=\"\"\nwhile [ $# -gt 0 ]; do if [ \"$1\" = -o ]; then shift; out=\"$1\"; fi; shift; done\nprintf binary > \"$out\"\n"
		case "rsync":
			script = "#!/bin/sh\nexit 0\n"
		case "ssh", "docker":
			script = "#!/bin/sh\nexit 0\n"
		}
		if err := os.WriteFile(commandPath, []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", binDirectory)

	iconDirectory := t.TempDir()
	iconPath, err := createMacOSIcon(iconDirectory)
	if err != nil {
		t.Fatalf("create macOS icon: %v", err)
	}
	if _, err := os.Stat(iconPath); err != nil {
		t.Fatalf("generated icon missing: %v", err)
	}

	outputPath := filepath.Join(t.TempDir(), "sitebrush")
	if err := buildGoBinary(t.TempDir(), outputPath, buildRequest{
		goos: "linux", goarch: "amd64", cgoEnabled: "0",
		tags: []string{"desktop"}, ldflags: []string{"-s", "-w"}, extraEnv: map[string]string{"TEST_RELEASE": "1"},
	}); err != nil {
		t.Fatalf("build Go binary: %v", err)
	}
	if err := verifyNonEmptyFile(outputPath); err != nil {
		t.Fatal(err)
	}

	if err := syncArtifacts(t.TempDir(), t.TempDir(), "release-42", "build.example", "/srv/releases"); err != nil {
		t.Fatalf("sync artifacts: %v", err)
	}
	if err := runDockerShellScript(t.TempDir(), "linux/amd64", "builder:latest", "true", map[string]string{"RELEASE": "42"}); err != nil {
		t.Fatalf("run Docker shell script: %v", err)
	}
	if err := ensureDockerBuilderImage(t.TempDir(), "linux/amd64", "builder:latest", "FROM scratch", true); err != nil {
		t.Fatalf("build Docker image: %v", err)
	}

	for _, testCase := range []struct {
		filter buildTargetFilter
		want   int
	}{
		{filter: buildTargetFilter{goos: "plan9"}, want: 0},
		{filter: buildTargetFilter{goarch: "arm64"}, want: 6},
	} {
		if got := len(filteredServerAppTargets(serverAppTargets(), testCase.filter)); got != testCase.want {
			t.Fatalf("filtered target count = %d, want %d", got, testCase.want)
		}
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

func TestCrossCompileMainBuildsEveryServerTargetAndPublishesChecksums(t *testing.T) {
	binDirectory := t.TempDir()
	goPath := filepath.Join(binDirectory, "go")
	if err := os.WriteFile(goPath, []byte("#!/bin/sh\noutput=\"\"\nwhile [ $# -gt 0 ]; do if [ \"$1\" = -o ]; then shift; output=\"$1\"; fi; shift; done\nprintf release-binary > \"$output\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDirectory)
	previousArguments := os.Args
	previousFlagSet := flag.CommandLine
	defer func() {
		os.Args = previousArguments
		flag.CommandLine = previousFlagSet
	}()
	outputRoot := ".crosscompile-all-server-output"
	flag.CommandLine = flag.NewFlagSet("crosscompile-test", flag.ContinueOnError)
	os.Args = []string{"crosscompile", "-mode=server-app", "-version=coverage-all", "-output-dir=" + outputRoot}
	main()
	repoRoot, err := findRepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(filepath.Join(repoRoot, outputRoot))
	serverDirectory := filepath.Join(repoRoot, outputRoot, "coverage-all", "server-app")
	if err := verifyNonEmptyFile(filepath.Join(serverDirectory, "sitebrush_linux_amd64")); err != nil {
		t.Fatal(err)
	}
	if err := verifyNonEmptyFile(filepath.Join(serverDirectory, "sitebrush_windows_arm64.exe")); err != nil {
		t.Fatal(err)
	}
	if err := verifyNonEmptyFile(filepath.Join(serverDirectory, "MD5SUMS")); err != nil {
		t.Fatal(err)
	}
	latestTarget, err := os.Readlink(filepath.Join(repoRoot, outputRoot, "latest"))
	if err != nil || latestTarget != "coverage-all" {
		t.Fatalf("latest release target = %q, %v", latestTarget, err)
	}
}

func TestCrossCompileDockerDesktopBuildsProduceOnlyExpectedArtifacts(t *testing.T) {
	binDirectory := t.TempDir()
	dockerPath := filepath.Join(binDirectory, "docker")
	dockerScript := `#!/bin/sh
host_root=""
script=""
previous=""
for argument do
  if [ "$previous" = "-v" ]; then host_root="${argument%%:/workspace}"; fi
  if [ "$previous" = "-lc" ]; then script="$argument"; fi
  previous="$argument"
done
if [ "$1" = image ] || [ "$1" = info ]; then
  if [ "$1" = info ]; then printf 'x86_64\n'; fi
  exit 0
fi
if [ -n "$script" ]; then
	if printf '%s' "$script" | /usr/bin/grep -q 'GOOS=linux'; then
	/bin/mkdir -p "$TEST_ARTIFACT_ROOT"
	printf desktop-archive > "$TEST_ARTIFACT_ROOT/sitebrush_linux_amd64_desktop_gtk40.zip"
	printf desktop-archive > "$TEST_ARTIFACT_ROOT/sitebrush_linux_amd64_desktop_gtk41.zip"
	else
	/bin/mkdir -p "$TEST_ARTIFACT_ROOT"
	printf desktop-archive > "$TEST_ARTIFACT_ROOT/sitebrush_windows_amd64_desktop.exe.zip"
  fi
fi
exit 0
`
	if err := os.WriteFile(dockerPath, []byte(dockerScript), 0o700); err != nil {
		t.Fatal(err)
	}
	originalPath := os.Getenv("PATH")
	t.Setenv("PATH", binDirectory+string(os.PathListSeparator)+originalPath)
	repoRoot := t.TempDir()
	outputDirectory := filepath.Join(repoRoot, "desktop-linux")
	t.Setenv("TEST_ARTIFACT_ROOT", outputDirectory)
	built, err := buildLinuxDesktopArtifacts(repoRoot, outputDirectory, "sitebrush", "security-test", desktopBuildOptions{targetFilter: buildTargetFilter{goos: "linux", goarch: "amd64"}})
	if err != nil || !built {
		t.Fatalf("Linux desktop build = %t, %v", built, err)
	}
	for _, variant := range []string{"gtk40", "gtk41"} {
		artifact := filepath.Join(outputDirectory, desktopArtifactName("sitebrush", "linux", "amd64", variant)+".zip")
		if err := verifyNonEmptyFile(artifact); err != nil {
			t.Fatalf("Linux %s artifact: %v", variant, err)
		}
	}

	outputDirectory = filepath.Join(repoRoot, "desktop-windows")
	t.Setenv("TEST_ARTIFACT_ROOT", outputDirectory)
	built, err = buildWindowsDesktopArtifacts(repoRoot, outputDirectory, "sitebrush", "security-test", desktopBuildOptions{targetFilter: buildTargetFilter{goos: "windows", goarch: "amd64"}})
	if err != nil || !built {
		t.Fatalf("Windows desktop build = %t, %v", built, err)
	}
	if err := verifyNonEmptyFile(filepath.Join(outputDirectory, "sitebrush_windows_amd64_desktop.exe.zip")); err != nil {
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
