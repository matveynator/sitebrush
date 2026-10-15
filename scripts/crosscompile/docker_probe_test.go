package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestDockerProbeFixture(t *testing.T) {
	if os.Getenv("SITEBRUSH_DOCKER_PROBE_FIXTURE") != "hang" {
		return
	}
	time.Sleep(time.Minute)
	os.Exit(0)
}

func TestDockerReadinessProbeHasBoundedWait(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(binary, "-test.run=^TestDockerProbeFixture$")
	command.Env = append(os.Environ(), "SITEBRUSH_DOCKER_PROBE_FIXTURE=hang")
	started := time.Now()
	err = runDockerProbe(command, 100*time.Millisecond)
	if !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("stalled daemon error: %v", err)
	}
	if time.Since(started) > 3*time.Second {
		t.Fatal("stalled daemon was not bounded")
	}
	if command.ProcessState == nil {
		t.Fatal("probe was not reaped")
	}
	command = exec.Command(binary, "-test.run=^TestDockerProbeFixture$")
	command.Env = append(os.Environ(), "SITEBRUSH_DOCKER_PROBE_FIXTURE=")
	if err := runDockerProbe(command, 5*time.Second); err != nil {
		t.Fatalf("responsive daemon: %v", err)
	}
	if err := runDockerProbe(exec.Command("missing-sitebrush-docker-command"), time.Second); err == nil {
		t.Fatal("missing executable accepted")
	}
}

func TestDockerStallAbortsDesktopPreparationWithoutReplacingArtifacts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires a Unix Docker command stub")
	}
	directory := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(directory, "docker-commands.log")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$SITEBRUSH_DOCKER_COMMAND_LOG\"\nexec " + shellQuote(executable) + " '-test.run=^TestDockerProbeFixture$'\n"
	if err := os.WriteFile(filepath.Join(directory, "docker"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("SITEBRUSH_DOCKER_COMMAND_LOG", logPath)
	t.Setenv("SITEBRUSH_DOCKER_PROBE_FIXTURE", "hang")
	desktopDirectory := filepath.Join(directory, "desktop-app")
	if err := os.Mkdir(desktopDirectory, 0700); err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(desktopDirectory, desktopArtifactName("sitebrush", "linux", "arm64", "gtk40"))
	if err := os.WriteFile(artifact, []byte("previous desktop binary"), 0700); err != nil {
		t.Fatal(err)
	}
	built, err := buildLinuxDesktopArtifacts(directory, desktopDirectory, "sitebrush", "2", desktopBuildOptions{targetFilter: buildTargetFilter{goos: "linux", goarch: "arm64"}})
	if built || !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("stalled preparation: built=%t err=%v", built, err)
	}
	commands, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(commands), "\n") != 1 || !strings.HasPrefix(string(commands), "image inspect ") {
		t.Fatalf("preparation continued after stalled daemon: %s", commands)
	}
	contents, err := os.ReadFile(artifact)
	if err != nil || string(contents) != "previous desktop binary" {
		t.Fatal("failed preparation replaced existing artifact")
	}
}

func TestDockerEmulationRetriesCachedPlatformAfterRegistration(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires a Unix Docker command stub")
	}
	directory := t.TempDir()
	script := `#!/bin/sh
case "$1:$2" in
image:inspect) exit 0;;
run:--privileged) printf enabled > "$SITEBRUSH_DOCKER_EMULATION_MARKER"; exit 0;;
run:*) [ -f "$SITEBRUSH_DOCKER_EMULATION_MARKER" ] && [ "$SITEBRUSH_DOCKER_EMULATION_STILL_BROKEN" != 1 ]; exit $?;;
esac
exit 1
`
	if err := os.WriteFile(filepath.Join(directory, "docker"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	for _, broken := range []bool{false, true} {
		marker := filepath.Join(t.TempDir(), "registered")
		t.Setenv("SITEBRUSH_DOCKER_EMULATION_MARKER", marker)
		t.Setenv("SITEBRUSH_DOCKER_EMULATION_STILL_BROKEN", "")
		if broken {
			t.Setenv("SITEBRUSH_DOCKER_EMULATION_STILL_BROKEN", "1")
		}
		err := ensureDockerPlatformSupport(directory, "linux/arm64", "cached-builder")
		if broken && (err == nil || !strings.Contains(err.Error(), "still cannot run")) {
			t.Fatalf("failed registered platform accepted: %v", err)
		}
		if !broken && err != nil {
			t.Fatalf("registered platform rejected: %v", err)
		}
		if _, err := os.Stat(marker); err != nil {
			t.Fatal("platform registration was not attempted", err)
		}
	}
}
