package main

import (
	"errors"
	"os"
	"os/exec"
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
