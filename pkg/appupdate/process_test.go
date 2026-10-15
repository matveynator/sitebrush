package appupdate

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCandidateCancellationTimeoutAndMissingExecutable(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	arguments := []string{"--update-fixture", "-path", root, "-fixture-wait"}
	if err := runCandidate(filepath.Join(root, "missing"), nil, root, io.Discard, time.Second, nil); err == nil {
		t.Fatal("missing candidate started")
	}
	canceled := make(chan struct{})
	close(canceled)
	if err := runCandidate(executable, arguments, root, io.Discard, time.Second, canceled); err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatal("candidate cancellation ignored", err)
	}
	if err := runCandidate(executable, arguments, root, io.Discard, 20*time.Millisecond, nil); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatal("candidate timeout ignored", err)
	}
}

func TestMaintenanceLaunchAndUnavailableProcessController(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := StartMaintenance(filepath.Join(root, "missing"), root, nil, root); err == nil {
		t.Fatal("missing maintenance supervisor launched")
	}
	if err := StartMaintenance(executable, root, []string{"--update-fixture", "-path", root}, root); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(root, "maintenance-started")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("detached maintenance supervisor did not run")
		}
		time.Sleep(10 * time.Millisecond)
	}
	stopped := make(chan struct{})
	close(stopped)
	worker := process{done: stopped}
	if err := worker.configure(nil); err == nil {
		t.Fatal("stopped controller configured")
	}
	if err := worker.exchange("alive", Version{}, false); err == nil {
		t.Fatal("stopped controller reported alive")
	}
	if _, err := worker.control("probe"); err == nil {
		t.Fatal("stopped controller answered probe")
	}
}

func TestEmptyProcessControllerRejectsLifecycleWithoutAChild(t *testing.T) {
	stop := make(chan struct{})
	worker := startProcess(processOptions{storagePath: t.TempDir(), output: io.Discard, stop: stop})
	defer func() { close(stop); <-worker.done }()
	for _, action := range []string{"probe", "commit", "address"} {
		if _, err := worker.control(action); err == nil {
			t.Fatal("absent child answered lifecycle request", action)
		}
	}
	if err := worker.exchange("alive", Version{}, false); err == nil {
		t.Fatal("absent child reported alive")
	}
	if err := worker.exchange("unknown", Version{}, false); err == nil {
		t.Fatal("unknown operation accepted")
	}
	if err := worker.exchange("stop", Version{}, false); err != nil {
		t.Fatal("empty controller could not stop", err)
	}
	if err := worker.configure([]string{"-port", "8080"}); err != nil {
		t.Fatal(err)
	}
	if err := worker.exchange("start", Version{Binary: "missing"}, false); err == nil {
		t.Fatal("missing executable reported started")
	}
}
