package main

import (
	"bufio"
	"bytes"
	"errors"
	"flag"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/matveynator/sitebrush/v2/pkg/buildconsole"
)

func publicationWizardSettings(t *testing.T) crosscompileSettings {
	t.Helper()
	return crosscompileSettings{program: "sitebrush", version: "123", outputRoot: t.TempDir(), mode: modeAll,
		options: desktopBuildOptions{goVersion: "1.27.3", macOSStage: "all"}}
}

func TestPublicationWizardStartsWithReviewAndEnterAccepts(t *testing.T) {
	t.Setenv("SITEBRUSH_RELEASE_PRIVATE_KEY", "")
	var output bytes.Buffer
	console, _ := buildconsole.New(&output, "never")
	plan, err := preparePublicationPlan(nil, publicationPlan{releaseKeyFile: "chosen.key"})
	if err != nil {
		t.Fatal(err)
	}
	settings := publicationWizardSettings(t)
	confirmed, _, plan, err := configureCrosscompile(console, bufio.NewReader(strings.NewReader("\n")), t.TempDir(), settings, plan)
	if err != nil || !confirmed || len(plan.targets) != 2 {
		t.Fatalf("Enter did not start with default destinations: %+v, %v", plan, err)
	}
	prompt := strings.Index(output.String(), "Enter: build and upload")
	for _, parameter := range []string{"Version:", "Mode:", "Output:", "CPU:", "Signing key:", "sitebrush.com ->", "sitebrush.ru ->", "Local settings:"} {
		if index := strings.Index(output.String(), parameter); index < 0 || index > prompt {
			t.Fatalf("parameter %q was not shown before input: %s", parameter, output.String())
		}
	}
	if strings.Contains(output.String(), "Upload to sitebrush") || strings.Contains(output.String(), "Edit SSH") {
		t.Fatal("unchanged settings still required individual confirmations")
	}
	confirmed, _, _, err = configureCrosscompile(console, bufio.NewReader(strings.NewReader("")), t.TempDir(), settings, plan)
	if err == nil || confirmed {
		t.Fatal("closed input started a build")
	}
}

func TestPublicationWizardRemembersUploadDestinations(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "sitebrush", "crosscompile.json")
	console, _ := buildconsole.New(io.Discard, "never")
	plan, err := preparePublicationPlan(nil, publicationPlan{configPath: filename})
	if err != nil {
		t.Fatal(err)
	}
	// Edit only on request, disable the defaults, then add another domain.
	reader := bufio.NewReader(strings.NewReader("e\n1\nno\ne\n2\nno\na\nEXAMPLE.COM\n\n\n\n\n"))
	confirmed, _, plan, err := configureCrosscompile(console, reader, t.TempDir(), publicationWizardSettings(t), plan)
	if err != nil || !confirmed || len(plan.targets) != 1 || plan.targets[0].Host != "root@example.com" {
		t.Fatalf("wizard: %+v, %v", plan, err)
	}
	if err := savePublicationConfig(filename, plan.saved, plan.releaseKeyFile); err != nil {
		t.Fatal(err)
	}
	configuration, err := loadPublicationConfig(filename)
	saved := configuration.Destinations
	if err != nil || len(saved) != 3 || saved[2].Domain != "example.com" || !saved[0].Disabled || !saved[1].Disabled {
		t.Fatalf("saved settings: %+v, %v", saved, err)
	}
	contents, _ := os.ReadFile(filename)
	if strings.Contains(string(contents), "restart") || strings.Contains(string(contents), "private_key") {
		t.Fatal("settings persisted service operations or secrets")
	}
	if information, _ := os.Stat(filename); runtime.GOOS != "windows" && information.Mode().Perm() != 0o600 {
		t.Fatal("settings are not private")
	}
	if information, _ := os.Stat(filepath.Dir(filename)); runtime.GOOS != "windows" && information.Mode().Perm() != 0o700 {
		t.Fatal("settings directory is not private")
	}
	plan, err = loadPublicationPlan(filename)
	if err != nil {
		t.Fatal(err)
	}
	plan, err = preparePublicationPlan(nil, plan)
	if err != nil || len(plan.targets) != 1 || plan.targets[0].Domain != "example.com" {
		t.Fatalf("remembered destination selection: %+v, %v", plan, err)
	}
}

func TestWizardExplicitTargetsDoNotAcquireOfficialDestinations(t *testing.T) {
	explicit := []syncDestination{{"deploy@example.com", "/srv/releases"}}
	plan, err := preparePublicationPlan(explicit, publicationPlan{saved: []publicationTarget{defaultPublicationTarget("sitebrush.com")}})
	if err != nil || len(plan.targets) != 1 || plan.targets[0].Host != "deploy@example.com" || plan.targets[0].Directory != "/srv/releases" {
		t.Fatalf("explicit targets changed: %+v, %v", plan, err)
	}
}

func TestWizardRemembersCustomSigningKeyPath(t *testing.T) {
	t.Setenv("SITEBRUSH_RELEASE_PRIVATE_KEY", "")
	console, _ := buildconsole.New(io.Discard, "never")
	filename := filepath.Join(t.TempDir(), "crosscompile.json")
	customKey := filepath.Join(t.TempDir(), "private keys", "release.key")
	plan, err := preparePublicationPlan(nil, publicationPlan{configPath: filename, releaseKeyFile: "previous-platform-path"})
	if err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(strings.NewReader("k\n" + customKey + "\n\n"))
	confirmed, _, plan, err := configureCrosscompile(console, reader, t.TempDir(), publicationWizardSettings(t), plan)
	if err != nil || !confirmed || plan.releaseKeyFile != customKey {
		t.Fatalf("custom key choice: %+v, %v", plan, err)
	}
	if err := savePublicationConfig(filename, plan.saved, plan.releaseKeyFile); err != nil {
		t.Fatal(err)
	}
	restored, err := loadPublicationPlan(filename)
	if err != nil || restored.releaseKeyFile != customKey {
		t.Fatalf("saved key path: %+v, %v", restored, err)
	}
	retained, err := configureReleaseKeyFile(console, bufio.NewReader(strings.NewReader("\n")), restored.releaseKeyFile)
	if err != nil || retained != customKey {
		t.Fatalf("Enter changed the remembered key path: %q, %v", retained, err)
	}
	if _, err := os.Stat(customKey); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("selecting a path created or replaced the private key")
	}
}

func TestWizardHasNoDefaultSigningKeyLocation(t *testing.T) {
	t.Setenv("SITEBRUSH_RELEASE_PRIVATE_KEY", "")
	var output bytes.Buffer
	console, _ := buildconsole.New(&output, "never")
	chosen, err := configureReleaseKeyFile(console, bufio.NewReader(strings.NewReader("\n")), "")
	if err != nil || chosen != "" {
		t.Fatalf("an unconfigured path acquired a default: %q, %v", chosen, err)
	}
	showSigningGuide(console, "")
	if !strings.Contains(output.String(), "not configured") || strings.Contains(output.String(), "release-ed25519.key") {
		t.Fatal(output.String())
	}
	if _, err := loadReleaseSigningKey("", "", nil); err == nil || !strings.Contains(err.Error(), "-release-key") {
		t.Fatalf("missing signing configuration did not fail explicitly: %v", err)
	}
	plan, err := preparePublicationPlan(nil, publicationPlan{})
	if err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(t.TempDir(), "chosen.key")
	confirmed, _, plan, err := configureCrosscompile(console, bufio.NewReader(strings.NewReader("\n"+key+"\n\n")), t.TempDir(), publicationWizardSettings(t), plan)
	if err != nil || !confirmed || plan.releaseKeyFile != key {
		t.Fatalf("missing key was not requested before accepting: %+v, %v", plan, err)
	}
}

func TestWizardSigningKeyPathSupportsRelativeAndHomePaths(t *testing.T) {
	console, _ := buildconsole.New(io.Discard, "never")
	homeDirectory, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	relativeKey, err := filepath.Abs(filepath.Join("private keys", "release.key"))
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []struct{ input, expected string }{
		{filepath.Join("private keys", "release.key"), relativeKey},
		{"~/private keys/release.key", filepath.Join(homeDirectory, "private keys", "release.key")},
		{"~", homeDirectory},
	} {
		chosen, err := configureReleaseKeyFile(console, bufio.NewReader(strings.NewReader(scenario.input+"\n")), "old-path")
		if err != nil || chosen != scenario.expected {
			t.Errorf("path %q -> %q, %v; want %q", scenario.input, chosen, err, scenario.expected)
		}
	}
	if _, err := configureReleaseKeyFile(console, bufio.NewReader(strings.NewReader("")), "previous.key"); err == nil {
		t.Fatal("closed input silently accepted the previous path")
	}
}

func TestWizardEditsBuildOptionsAndReturnsToReview(t *testing.T) {
	consoleOutput := new(bytes.Buffer)
	console, _ := buildconsole.New(consoleOutput, "never")
	repoRoot := t.TempDir()
	plan, err := preparePublicationPlan([]syncDestination{{"root@example.com", "/srv/download"}}, publicationPlan{})
	if err != nil {
		t.Fatal(err)
	}
	settings := publicationWizardSettings(t)
	reader := bufio.NewReader(strings.NewReader("b\n456\ncustombrush\nserver-app\nlinux\narm64\nnew artifacts\n\n\nyes\nno\n\n"))
	confirmed, edited, _, err := configureCrosscompile(console, reader, repoRoot, settings, plan)
	if err != nil || !confirmed {
		t.Fatalf("editing failed: %v", err)
	}
	if edited.version != "456" || edited.program != "custombrush" || edited.mode != modeServerApp || edited.filter != (buildTargetFilter{goos: "linux", goarch: "arm64"}) || edited.options.targetFilter != edited.filter || !edited.options.rebuildDockerImages || edited.outputRoot != filepath.Join(repoRoot, "new artifacts") {
		t.Fatalf("edited options did not reach the build configuration: %+v", edited)
	}
	if !strings.Contains(consoleOutput.String(), "1 server builds, 0 desktop packages") || strings.Count(consoleOutput.String(), "Build and upload settings") != 2 {
		t.Fatalf("edited build was not reviewed: %s", consoleOutput.String())
	}
	confirmed, _, _, err = configureCrosscompile(console, bufio.NewReader(strings.NewReader("b\n")), repoRoot, settings, plan)
	if err == nil || confirmed {
		t.Fatal("closed input during editing started a build")
	}
	confirmed, _, _, err = configureCrosscompile(console, bufio.NewReader(strings.NewReader("unknown\nq\n")), repoRoot, settings, plan)
	if err != nil || confirmed {
		t.Fatalf("explicit cancellation failed: %v", err)
	}
}

func TestOfficialWizardDomainRequiresSigningAfterSSHAddressEdit(t *testing.T) {
	for _, domain := range []string{"sitebrush.com", "sitebrush.ru"} {
		for _, host := range []string{"root@release-primary", "root@192.0.2.10"} {
			t.Run(domain+"/"+host, func(t *testing.T) {
				console, _ := buildconsole.New(io.Discard, "never")
				reader := bufio.NewReader(strings.NewReader("yes\n" + host + "\n\n"))
				target, selected, err := configurePublicationTarget(console, reader, defaultPublicationTarget(domain))
				if err != nil || !selected || target.Domain != domain || target.Host != host {
					t.Fatalf("edited destination: %+v, selected=%t, err=%v", target, selected, err)
				}
				plan := publicationPlan{targets: []publicationTarget{target}}
				if !plan.officialReleasePublication() {
					t.Fatal("editing the SSH contact disabled mandatory official signing")
				}
				if officialReleasePublication(plan.syncDestinations()) {
					t.Fatal("fixture must use a nonofficial SSH alias or IP")
				}
			})
		}
	}
	plan := publicationPlan{saved: []publicationTarget{defaultPublicationTarget("sitebrush.com")}}
	if plan.officialReleasePublication() {
		t.Fatal("an unselected saved domain triggered publication")
	}
	plan.targets = []publicationTarget{{Domain: "example.com", Host: "root@sitebrush.com", Directory: "/srv/download"}}
	if plan.officialReleasePublication() {
		t.Fatal("wizard publication identity was inferred from its SSH contact")
	}
}

func TestPublicationSettingsValidateUploadPathsAndIgnoreLegacyActions(t *testing.T) {
	for _, directory := range []string{"/", "/srv/../", "relative", "/srv/download/", "/srv/\ndownload"} {
		target := defaultPublicationTarget("example.com")
		target.Directory = directory
		if err := validatePublicationTarget(target); err == nil {
			t.Errorf("accepted unsafe upload directory %q", directory)
		}
	}
	target := defaultPublicationTarget("example.com")
	target.Host = "root@host; echo injected"
	if _, err := publicationProbeScript(target); err == nil {
		t.Fatal("accepted SSH address injection")
	}
	filename := filepath.Join(t.TempDir(), "legacy.json")
	if err := os.WriteFile(filename, []byte(`{"version":1,"destinations":[{"domain":"example.com","host":"root@example.com","directory":"/srv/download","service":"sitebrush-80","binary":"/usr/local/bin/sitebrush","storage":"/var/lib/sitebrush","restart":true}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	configuration, err := loadPublicationConfig(filename)
	saved := configuration.Destinations
	if err != nil || len(saved) != 1 || saved[0].Directory != "/srv/download" {
		t.Fatalf("legacy upload settings: %+v, %v", saved, err)
	}
	if err := savePublicationConfig(filename, saved, configuration.ReleaseKey); err != nil {
		t.Fatal(err)
	}
	contents, _ := os.ReadFile(filename)
	for _, obsolete := range []string{"service", "binary", "storage", "restart"} {
		if strings.Contains(string(contents), obsolete) {
			t.Fatalf("obsolete service setting persisted: %s", contents)
		}
	}
	script, err := publicationProbeScript(saved[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"service ", "rm ", ".sitebrush-update", "restart"} {
		if strings.Contains(script, action) {
			t.Fatalf("upload readiness contains a service action: %s", script)
		}
	}
}

func TestDisplayedBuildMatrix(t *testing.T) {
	var output bytes.Buffer
	console, _ := buildconsole.New(&output, "never")
	showBuildMatrix(console, modeAll, buildTargetFilter{}, "darwin", "all")
	if !strings.Contains(output.String(), "5 operating systems, 3 CPU architectures; 11 server builds, 7 desktop packages") {
		t.Fatal(output.String())
	}
	output.Reset()
	showBuildMatrix(console, modeServerApp, buildTargetFilter{goos: "linux", goarch: "arm64"}, "linux", "all")
	if !strings.Contains(output.String(), "1 server builds, 0 desktop packages") || strings.Contains(output.String(), "windows") {
		t.Fatal(output.String())
	}
}

func TestBuilderDockerfilesLeavePlatformToDockerCommand(t *testing.T) {
	for _, architecture := range []string{"amd64", "arm64"} {
		for _, dockerfile := range []string{linuxDesktopDockerfile(architecture, "gtk40", "1.27.3"),
			linuxDesktopDockerfile(architecture, "gtk41", "1.27.3"), windowsDesktopDockerfile(architecture, "1.27.3"), macOSDesktopDockerfile(architecture, "1.27.3")} {
			if strings.Contains(dockerfile, "FROM --platform=") {
				t.Fatal("Dockerfile contains a constant FROM platform")
			}
		}
	}
}

func TestFailedUploadStopsBeforeFollowingDestination(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix command fixtures")
	}
	commands := t.TempDir()
	log := filepath.Join(commands, "commands.log")
	writeCommandStub(t, commands, "ssh", `printf 'ssh %s\n' "$*" >> "$PUBLICATION_LOG"`)
	writeCommandStub(t, commands, "rsync", `printf 'rsync %s\n' "$*" >> "$PUBLICATION_LOG"; exit 23`)
	t.Setenv("PATH", commands)
	t.Setenv("PUBLICATION_LOG", log)
	console, _ := buildconsole.New(io.Discard, "never")
	err := publishArtifacts(t.TempDir(), t.TempDir(), "123", []syncDestination{{"root@first.example", "/srv/releases"}, {"root@second.example", "/srv/releases"}}, console)
	logged, _ := os.ReadFile(log)
	if err == nil || !strings.Contains(err.Error(), "upload to root@first.example failed") || strings.Contains(string(logged), "second.example") || strings.Contains(string(logged), "service") {
		t.Fatalf("publication continued after failure: %v, %s", err, logged)
	}
}

func TestCompileFailureNeverUploads(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix command fixtures")
	}
	oldFlags, oldArguments := flag.CommandLine, os.Args
	defer func() { flag.CommandLine, os.Args = oldFlags, oldArguments }()
	commands := t.TempDir()
	log := filepath.Join(commands, "commands.log")
	writeCommandStub(t, commands, "go", `printf 'go\n' >> "$PUBLICATION_LOG"; exit 12`)
	writeCommandStub(t, commands, "git", `printf '0123456789012345678901234567890123456789\n'`)
	writeCommandStub(t, commands, "ssh", `printf 'ssh\n' >> "$PUBLICATION_LOG"`)
	writeCommandStub(t, commands, "rsync", `printf 'rsync\n' >> "$PUBLICATION_LOG"`)
	t.Setenv("PATH", commands)
	t.Setenv("PUBLICATION_LOG", log)
	t.Setenv("SITEBRUSH_RELEASE_PRIVATE_KEY", "")
	flag.CommandLine = flag.NewFlagSet("crosscompile-test", flag.ContinueOnError)
	os.Args = []string{"crosscompile", "-mode=server-app", "-os=linux", "-arch=amd64", "-version=123", "-output-dir=" + t.TempDir(), "-sync=root@example.com=/srv/releases"}
	if err := runCrosscompile(); err == nil {
		t.Fatal("compiler error was hidden")
	}
	logged, _ := os.ReadFile(log)
	if string(logged) != "go\n" {
		t.Fatalf("compile failure reached deployment: %s", logged)
	}
}

func TestSigningFailurePreservesExistingOutputAndNeverCompiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix command fixtures")
	}
	oldFlags, oldArguments := flag.CommandLine, os.Args
	defer func() { flag.CommandLine, os.Args = oldFlags, oldArguments }()
	commands := t.TempDir()
	log := filepath.Join(commands, "commands.log")
	writeCommandStub(t, commands, "go", `printf 'go\n' >> "$PUBLICATION_LOG"; exit 12`)
	writeCommandStub(t, commands, "git", `printf '0123456789012345678901234567890123456789\n'`)
	t.Setenv("PATH", commands)
	t.Setenv("PUBLICATION_LOG", log)
	t.Setenv("SITEBRUSH_RELEASE_PRIVATE_KEY", "")
	output := t.TempDir()
	if err := os.Mkdir(filepath.Join(output, "123"), 0o700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(output, "123", "existing-artifact")
	if err := os.WriteFile(sentinel, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	flag.CommandLine = flag.NewFlagSet("crosscompile-test", flag.ContinueOnError)
	os.Args = []string{"crosscompile", "-mode=server-app", "-version=123", "-sign-updates", "-release-key=" + filepath.Join(commands, "missing.key"), "-output-dir=" + output}
	err := runCrosscompile()
	if err == nil || !strings.Contains(err.Error(), "release signing key") {
		t.Fatalf("missing key was not rejected: %v", err)
	}
	if _, err := os.Stat(sentinel); err != nil {
		t.Fatal("signing failure deleted previous artifacts")
	}
	if _, err := os.Stat(log); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("signing failure reached the compiler")
	}
	var guide bytes.Buffer
	console, _ := buildconsole.New(&guide, "never")
	t.Setenv("SITEBRUSH_RELEASE_PRIVATE_KEY", "sensitive-key-content")
	showSigningGuide(console, "ignored.key")
	if strings.Contains(guide.String(), "sensitive-key-content") || !strings.Contains(guide.String(), "takes precedence") {
		t.Fatal("signing guide exposed the key or hid its effective source")
	}
}

func TestWizardReadinessFailsBeforeCompilation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix command fixtures")
	}
	commands := t.TempDir()
	writeCommandStub(t, commands, "go", `printf 'go version go1.27.3\n'`)
	writeCommandStub(t, commands, "git", `printf '0123456789012345678901234567890123456789\n'`)
	writeCommandStub(t, commands, "docker", `exit 23`)
	t.Setenv("PATH", commands)
	console, _ := buildconsole.New(io.Discard, "never")
	plan := publicationPlan{}
	if err := checkBuildReadiness(t.TempDir(), console, modeAll, desktopBuildOptions{goVersion: "1.27.3"}, plan); err == nil || !strings.Contains(err.Error(), "Docker") {
		t.Fatalf("missing Docker not diagnosed: %v", err)
	}
	writeCommandStub(t, commands, "ssh", `printf 'SSH authentication failed\n'; exit 255`)
	writeCommandStub(t, commands, "rsync", `exit 0`)
	plan.targets = []publicationTarget{defaultPublicationTarget("example.com")}
	if err := checkBuildReadiness(t.TempDir(), console, modeServerApp, desktopBuildOptions{goVersion: "1.27.3"}, plan); err == nil || !strings.Contains(err.Error(), "SSH authentication") {
		t.Fatalf("missing SSH access not diagnosed: %v", err)
	}
}

func TestChildToolsNeverInheritReleaseSigningSecret(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix command fixtures")
	}
	commands := t.TempDir()
	writeCommandStub(t, commands, "check-env", `test -z "${SITEBRUSH_RELEASE_PRIVATE_KEY:-}"; test "$PUBLIC_BUILD_SETTING" = retained`)
	t.Setenv("PATH", commands)
	t.Setenv("SITEBRUSH_RELEASE_PRIVATE_KEY", "private-sentinel")
	t.Setenv("PUBLIC_BUILD_SETTING", "retained")
	if err := runCommand(commands, "check-env"); err != nil {
		t.Fatal("command inherited the signing secret", err)
	}
	if _, err := readinessOutput(commands, "check-env"); err != nil {
		t.Fatal("readiness probe inherited the signing secret", err)
	}
	if _, err := runOutput(commands, "check-env"); err != nil {
		t.Fatal("source/SDK probe inherited the signing secret", err)
	}
}

func TestRsyncPathsCannotExpandRemoteShellCommands(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix command fixtures")
	}
	commands := t.TempDir()
	t.Setenv("PATH", commands)
	writeCommandStub(t, commands, "rsync", `printf 'openrsync: protocol version 29\n'`)
	directory := "/srv/download's $(command-that-must-not-run) folder"
	arguments, err := artifactSyncArguments(commands, "/source", "root@example.com", directory)
	if err != nil {
		t.Fatal(err)
	}
	quoted := strings.TrimPrefix(arguments[len(arguments)-1], "root@example.com:")
	output, err := exec.Command("/bin/sh", "-c", "printf '%s' "+quoted).CombinedOutput()
	if err != nil || string(output) != directory+"/" {
		t.Fatalf("legacy rsync path is executable shell syntax: %q, %v", output, err)
	}
	for _, capability := range []string{"--protect-args", "--secluded-args"} {
		writeCommandStub(t, commands, "rsync", "printf '%s\\n' "+shellQuote(capability))
		arguments, err = artifactSyncArguments(commands, "/source", "root@example.com", directory)
		if err != nil || arguments[1] != capability || arguments[len(arguments)-1] != "root@example.com:"+directory+"/" {
			t.Fatalf("modern rsync path protection: %v, %v", arguments, err)
		}
	}
}
