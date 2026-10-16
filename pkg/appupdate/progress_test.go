package appupdate

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestProgressSnapshotsAreIndependentAndBounded(t *testing.T) {
	progress := OperationProgress{Action: "upgrade", StartedAt: time.Now().UTC()}
	progress.event("preparing", "stage_started", "")
	published := progress
	for range 180 {
		progress.event("downloading", "fallback", strings.Repeat("<", 2048))
	}
	if len(published.Events) != 1 || published.Events[0].Sequence != 1 {
		t.Fatal("worker changed an already published snapshot")
	}
	encoded, err := json.Marshal(progress.Events)
	if err != nil || len(encoded) > operationLogLimit || !progress.Truncated || progress.Events[0].Sequence <= 1 {
		t.Fatalf("journal was not bounded: %d bytes, truncated=%v, %v", len(encoded), progress.Truncated, err)
	}
	if _, err := encodeContactResult(Response{State: State{Progress: progress}}); err != nil {
		t.Fatal("bounded progress exceeded the contact protocol", err)
	}
}

func TestMeasuredTaskCancellationJoinsProducer(t *testing.T) {
	for _, scenario := range []string{"subscriber-stop", "journal-failure"} {
		t.Run(scenario, func(t *testing.T) {
			stop := make(chan struct{})
			joined := make(chan struct{})
			_, err := runMeasured(stop, func(updates chan<- WorkProgress, session <-chan struct{}) (struct{}, error) {
				defer close(joined)
				if err := sendWork(updates, WorkProgress{Bytes: 1}, session); err != nil {
					return struct{}{}, err
				}
				<-session
				return struct{}{}, errors.New("producer cancelled")
			}, func(WorkProgress) error {
				if scenario == "subscriber-stop" {
					close(stop)
					return nil
				}
				return errors.New("cannot persist journal")
			})
			if err == nil {
				t.Fatal("cancelled task returned success")
			}
			select {
			case <-joined:
			default:
				t.Fatal("task still owns resources after cancellation returned")
			}
		})
	}
}

func TestSnapshotCountersDescribeActualCopiedContent(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "site"), 0700); err != nil {
		t.Fatal(err)
	}
	content := bytes.Repeat([]byte("content"), 10000)
	if err := os.WriteFile(filepath.Join(root, "site", "page"), content, 0600); err != nil {
		t.Fatal(err)
	}
	store, err := openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	totals, err := inspectBackupSpace(root, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var latest WorkProgress
	backup, err := runMeasured(nil, func(updates chan<- WorkProgress, session <-chan struct{}) (string, error) {
		return snapshotProgress(root, store, strings.Repeat("d", 32), session, updates, totals)
	}, func(work WorkProgress) error { latest = work; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if latest.Bytes != int64(len(content)) || latest.TotalBytes != latest.Bytes || latest.Files != 2 || latest.TotalFiles != 2 {
		t.Fatalf("incorrect snapshot totals: %+v", latest)
	}
	if err := verifySnapshot(store, backup); err != nil {
		t.Fatal(err)
	}
}

func TestDownloadCountersForNetChanAndHTTP(t *testing.T) {
	for _, transport := range []string{"netchan", "http"} {
		t.Run(transport, func(t *testing.T) {
			payload := bytes.Repeat([]byte("measured release"), 20000)
			catalog, manifest, directory, _ := releaseMirrorFixture(t, payload)
			if transport == "http" {
				catalog.mirror = nil
				server := httptest.NewServer(http.FileServer(http.Dir(directory)))
				defer server.Close()
				catalog.artifactBases = []string{server.URL + "/"}
				catalog.artifactBase = server.URL + "/"
			}
			var latest WorkProgress
			var source string
			_, err := runMeasured(nil, func(updates chan<- WorkProgress, session <-chan struct{}) (struct{}, error) {
				return struct{}{}, catalog.downloadProgress(manifest, filepath.Join(t.TempDir(), "candidate"), session, updates)
			}, func(work WorkProgress) error {
				if work.Code == "source" {
					source = work.Detail
				} else {
					latest = work
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if latest.Bytes != int64(len(payload)) || latest.TotalBytes != latest.Bytes || source == "" {
				t.Fatalf("missing download measurements: %+v, source=%q", latest, source)
			}
		})
	}
}

func TestOperationLogKeepsSkippedStagesAndSanitizesTerminalOutput(t *testing.T) {
	progress := OperationProgress{Action: "upgrade", StartedAt: time.Now().UTC()}
	progress.event("preparing", "stage_started", "")
	progress.event("verifying", "stage_started", "")
	progress.event("inspecting", "stage_failed", "unsafe link\nforged log\x1b[31m")
	var output bytes.Buffer
	sequence, err := WriteOperationEvents(&output, progress, 0)
	if err != nil || sequence != progress.Sequence || strings.Count(output.String(), "\n") != 3 || strings.ContainsRune(output.String(), '\x1b') {
		t.Fatalf("invalid operation log: %q, %v", output.String(), err)
	}
	if _, err := WriteOperationEvents(&output, progress, sequence); err != nil || strings.Count(output.String(), "\n") != 3 {
		t.Fatal("polling duplicated operation events", err)
	}
	writer := &progressWriter{output: io.Discard, started: time.Now().Add(-time.Second), last: time.Now().Add(-time.Second)}
	if _, err := writer.Write([]byte("measured")); err != nil || writer.work.Speed <= 0 {
		t.Fatal("missing transfer speed", err)
	}
	progress.Stage = "backing_up"
	progress.Work = WorkProgress{Bytes: 1024, TotalBytes: 1024, Files: 3, TotalFiles: 3}
	progress.finishStage()
	finished := progress.Events[len(progress.Events)-1]
	if !strings.Contains(finished.Detail, "bytes=1024/1024 files=3/3") {
		t.Fatal("completed stage lost measurements when the next stage reset its counters", finished)
	}
}
