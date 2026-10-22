package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/matveynator/sitebrush/v2/pkg/appupdate"
	"github.com/matveynator/sitebrush/v2/pkg/buildconsole"
)

func TestPublicationWizardRemembersDestinationsButNeverRestartConsent(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "sitebrush", "crosscompile.json")
	console, _ := buildconsole.New(io.Discard, "never")
	// Skip both defaults, add one destination, keep its settings, choose restart.
	reader := bufio.NewReader(strings.NewReader("no\nno\nEXAMPLE.COM\nyes\nno\nyes\n\nyes\n"))
	plan, err := configurePublicationWithConfig(console, reader, nil, filename)
	if err != nil || len(plan.targets) != 1 || !plan.targets[0].Restart || plan.targets[0].Host != "root@example.com" {
		t.Fatalf("wizard: %+v, %v", plan, err)
	}
	confirmed, err := console.Confirm(reader, "Start?", false)
	if err != nil || !confirmed {
		t.Fatalf("wizard consumed the final confirmation: %v", err)
	}
	if err := savePublicationConfig(filename, plan.saved); err != nil {
		t.Fatal(err)
	}
	saved, err := loadPublicationConfig(filename)
	if err != nil || len(saved) != 3 || saved[2].Domain != "example.com" || saved[2].Restart {
		t.Fatalf("saved settings or consent: %+v, %v", saved, err)
	}
	contents, _ := os.ReadFile(filename)
	if strings.Contains(string(contents), "restart") || strings.Contains(string(contents), "private_key") {
		t.Fatal("settings persisted consent or secrets")
	}
	if information, _ := os.Stat(filename); runtime.GOOS != "windows" && information.Mode().Perm() != 0o600 {
		t.Fatal("settings are not private")
	}
	// Defaults and the remembered extra destination are all offered next time.
	reader = bufio.NewReader(strings.NewReader("no\nno\n\nno\nno\n\n"))
	plan, err = configurePublicationWithConfig(console, reader, nil, filename)
	if err != nil || len(plan.targets) != 1 || plan.targets[0].Domain != "example.com" || plan.targets[0].Restart {
		t.Fatalf("remembered destination: %+v, %v", plan, err)
	}
}

func TestWizardExplicitTargetsDoNotAcquireOfficialDestinations(t *testing.T) {
	console, _ := buildconsole.New(io.Discard, "never")
	explicit := []syncDestination{{"deploy@example.com", "/srv/releases"}}
	reader := bufio.NewReader(strings.NewReader("\nno\nno\n\n"))
	plan, err := configurePublicationWithConfig(console, reader, explicit, filepath.Join(t.TempDir(), "settings.json"))
	if err != nil || len(plan.targets) != 1 || plan.targets[0].Host != "deploy@example.com" || plan.targets[0].Directory != "/srv/releases" {
		t.Fatalf("explicit targets changed: %+v, %v", plan, err)
	}
}

func TestPublicationSettingsRejectUnsafeCleanupPaths(t *testing.T) {
	for _, filename := range []string{"/", "/srv/../", "relative", "/srv/installation/", "/srv/\ninstallation"} {
		target := defaultPublicationTarget("example.com")
		target.Storage = filename
		if err := validatePublicationTarget(target); err == nil {
			t.Errorf("accepted unsafe installation %q", filename)
		}
	}
	target := defaultPublicationTarget("example.com")
	target.Service = "sitebrush; echo injected"
	if _, err := publicationProbeScript(target); err == nil {
		t.Fatal("accepted service command injection")
	}
	filename := filepath.Join(t.TempDir(), "invalid.json")
	if err := os.WriteFile(filename, []byte(`{"version":1,"destinations":[],"restart":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadPublicationConfig(filename); err == nil {
		t.Fatal("accepted unexpected saved action")
	}
}

func TestPublicationPlatformAndDisplayedMatrix(t *testing.T) {
	for _, scenario := range []struct{ response, goos, goarch string }{
		{"banner\nSITEBRUSH_PLATFORM=Linux/x86_64", "linux", "amd64"},
		{"SITEBRUSH_PLATFORM=Linux/aarch64", "linux", "arm64"},
		{"SITEBRUSH_PLATFORM=FreeBSD/amd64", "freebsd", "amd64"},
		{"SITEBRUSH_PLATFORM=Linux/i386", "", ""}, {"missing", "", ""},
	} {
		goos, goarch, err := parsePublicationPlatform(scenario.response)
		if goos != scenario.goos || goarch != scenario.goarch || (err != nil) != (scenario.goos == "") {
			t.Errorf("platform %q -> %s/%s, %v", scenario.response, goos, goarch, err)
		}
	}
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
	if err == nil || !strings.Contains(err.Error(), "no server restarts") || strings.Contains(string(logged), "second.example") || strings.Contains(string(logged), "service") {
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
	if err := checkBuildReadiness(t.TempDir(), console, modeAll, desktopBuildOptions{goVersion: "1.27.3"}, &plan, false, "sitebrush"); err == nil || !strings.Contains(err.Error(), "Docker") {
		t.Fatalf("missing Docker not diagnosed: %v", err)
	}
	writeCommandStub(t, commands, "ssh", `printf 'SSH authentication failed\n'; exit 255`)
	writeCommandStub(t, commands, "rsync", `exit 0`)
	plan.targets = []publicationTarget{defaultPublicationTarget("example.com")}
	if err := checkBuildReadiness(t.TempDir(), console, modeServerApp, desktopBuildOptions{goVersion: "1.27.3"}, &plan, false, "sitebrush"); err == nil || !strings.Contains(err.Error(), "SSH authentication") {
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

func TestRemoteUpdateChecksBeforeStopAndStopsAfterEveryFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix shell deployment fixtures")
	}
	for _, failure := range []string{"hash", "version", "stop", "rename", "delete", "start", ""} {
		t.Run(failure, func(t *testing.T) {
			directory, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			// Quotes, spaces, and shell metacharacters must remain literal paths.
			root := filepath.Join(directory, "release's $(no-such-command)")
			target := defaultPublicationTarget("example.com")
			target.Directory, target.Storage, target.Binary = filepath.Join(root, "download"), filepath.Join(root, "installation"), filepath.Join(root, "bin", "sitebrush")
			target.GOOS, target.GOARCH, target.Restart = "linux", "amd64", true
			state := filepath.Join(target.Storage, appupdate.Directory)
			for _, folder := range []string{state, filepath.Dir(target.Binary), filepath.Join(target.Directory, "123", "server-app")} {
				if err := os.MkdirAll(folder, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			oldBinary := "#!/bin/sh\nprintf 'old\\n'\n"
			candidate := "#!/bin/sh\nprintf '123\\n'\n"
			if failure == "version" {
				candidate = "#!/bin/sh\nprintf '999\\n'\n"
			}
			candidatePath := filepath.Join(target.Directory, "123", "server-app", "sitebrush_linux_amd64")
			if err := os.WriteFile(target.Binary, []byte(oldBinary), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(candidatePath, []byte(candidate), 0o755); err != nil {
				t.Fatal(err)
			}
			digest := sha256.Sum256([]byte(candidate))
			artifact := appupdate.Artifact{OS: "linux", Arch: "amd64", Name: "sitebrush_linux_amd64", Size: int64(len(candidate)), SHA256: hex.EncodeToString(digest[:])}
			if failure == "hash" {
				artifact.SHA256 = strings.Repeat("0", 64)
			}
			script, err := publicationRestartScript(target, "123", artifact)
			if err != nil {
				t.Fatal(err)
			}
			commands := t.TempDir()
			log := filepath.Join(commands, "service.log")
			writeCommandStub(t, commands, "service", `printf '%s\n' "$2" >> "$PUBLICATION_LOG"; if [ "$2" = "$FAIL_ACTION" ]; then exit 23; fi`)
			moveCommand, _ := exec.LookPath("mv")
			removeCommand, _ := exec.LookPath("rm")
			writeCommandStub(t, commands, "mv", `if [ "$FAIL_ACTION" = rename ]; then exit 23; fi; exec `+shellQuote(moveCommand)+` "$@"`)
			writeCommandStub(t, commands, "rm", `if [ "$FAIL_ACTION" = delete ] && [ "$1" = -rf ]; then exit 23; fi; exec `+shellQuote(removeCommand)+` "$@"`)
			command := exec.Command("/bin/sh", "-c", script)
			command.Env = append(os.Environ(), "PATH="+commands+string(os.PathListSeparator)+os.Getenv("PATH"), "PUBLICATION_LOG="+log, "FAIL_ACTION="+failure)
			output, err := command.CombinedOutput()
			if (err != nil) != (failure != "") {
				t.Fatalf("failure=%s, err=%v, output=%s", failure, err, output)
			}
			logged, _ := os.ReadFile(log)
			installed, _ := os.ReadFile(target.Binary)
			_, stateErr := os.Stat(state)
			wantNew := failure == "delete" || failure == "start" || failure == ""
			wantDeleted := failure == "start" || failure == ""
			if (string(installed) == candidate) != wantNew || errors.Is(stateErr, os.ErrNotExist) != wantDeleted {
				t.Fatalf("failure=%s unexpected binary or deletion: %q, %v", failure, installed, stateErr)
			}
			if (failure == "hash" || failure == "version") && len(logged) != 0 {
				t.Fatalf("unverified candidate stopped the service: %s", logged)
			}
			if strings.Contains(string(logged), "start") != wantDeleted {
				t.Fatalf("service started after failed prerequisite: %s", logged)
			}
		})
	}
}
