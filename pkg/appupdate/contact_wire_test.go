package appupdate

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestContactResultsValidateProtocolAndAllowFutureFields(t *testing.T) {
	encoded, err := encodeContactResult(Response{State: State{Current: Version{Version: "7"}}})
	if err != nil {
		t.Fatal(err)
	}
	response, err := decodeContactResult(encoded)
	if err != nil || response.State.Current.Version != "7" {
		t.Fatalf("round trip: %+v %v", response, err)
	}
	for _, message := range []wireRequest{
		{Protocol: Protocol + 1, Result: encoded.Result},
		{Protocol: Protocol},
		{Protocol: Protocol, Result: []byte("invalid")},
		{Protocol: Protocol, Result: make([]byte, contactPayloadLimit+1)},
		{Protocol: Protocol, Result: []byte(`{"Error":"denied"}`)},
	} {
		if _, err := decodeContactResult(message); err == nil {
			t.Fatal("invalid contact result accepted")
		}
	}
	future := wireRequest{Protocol: Protocol, Result: []byte(`{"State":{"Current":{"version":"8"},"future_field":true},"FutureResult":true}`)}
	if result, err := decodeContactResult(future); err != nil || result.State.Current.Version != "8" {
		t.Fatalf("future result: %+v %v", result, err)
	}
	if _, err := encodeContactResult(Response{Error: strings.Repeat("x", contactPayloadLimit)}); err == nil {
		t.Fatal("oversized result encoded")
	}
}

func TestLifecycleWireRejectsBrokenResultsAndCancelsPendingWork(t *testing.T) {
	for _, payload := range [][]byte{nil, []byte("invalid"), make([]byte, contactPayloadLimit+1), []byte(`{"Address":"localhost:80","FutureField":true}`)} {
		remote := make(chan wireChildTask)
		done := make(chan struct{})
		go func() { defer close(done); task := <-remote; task.Reply <- payload }()
		result := exchangeChildWire(remote, "probe", nil)
		<-done
		if len(payload) > 0 && payload[0] == '{' {
			if result.Error != "" || result.Address != "localhost:80" {
				t.Fatalf("future lifecycle: %+v", result)
			}
		} else if result.Error == "" {
			t.Fatal("broken lifecycle result accepted")
		}
	}
	remote := make(chan wireChildTask)
	stop := make(chan struct{})
	result := make(chan ChildResult, 1)
	go func() { result <- exchangeChildWire(remote, "probe", stop) }()
	<-remote
	close(stop)
	select {
	case response := <-result:
		if response.Error == "" {
			t.Fatal("cancellation accepted as success")
		}
	case <-time.After(time.Second):
		t.Fatal("pending lifecycle did not cancel")
	}
	tasks := make(chan ChildTask)
	session := make(chan wireChildTask)
	bridgeStop := make(chan struct{})
	bridgeDone := make(chan struct{})
	go receiveChildWire(session, tasks, bridgeStop, bridgeDone)
	session <- wireChildTask{Action: "probe", Reply: make(chan []byte)}
	<-tasks
	close(bridgeStop)
	select {
	case <-bridgeDone:
	case <-time.After(time.Second):
		t.Fatal("bridge did not cancel pending child")
	}
}

// A real successor binary adds fields to both domain structures. Its NetChan
// root and lifecycle capabilities must still work with the older launcher.
func TestSupervisorUpdatesAcrossAdditiveDomainSchemaChanges(t *testing.T) {
	directory := t.TempDir()
	replacements := make(map[string]string)
	for _, change := range []struct{ file, before, after string }{
		{"storage.go", "type State struct {", "type State struct {\n FutureJournalField string"},
		{"contact.go", "type ChildResult struct {", "type ChildResult struct {\n FutureLifecycleField string"},
	} {
		original, err := filepath.Abs(change.file)
		if err != nil {
			t.Fatal(err)
		}
		source, err := os.ReadFile(original)
		if err != nil {
			t.Fatal(err)
		}
		replacement := strings.Replace(string(source), change.before, change.after, 1)
		if replacement == string(source) {
			t.Fatalf("fixture anchor missing: %s", change.file)
		}
		destination := filepath.Join(directory, change.file)
		if err := os.WriteFile(destination, []byte(replacement), 0600); err != nil {
			t.Fatal(err)
		}
		replacements[original] = destination
	}
	overlay, err := json.Marshal(struct{ Replace map[string]string }{replacements})
	if err != nil {
		t.Fatal(err)
	}
	overlayPath := filepath.Join(directory, "overlay.json")
	if err := os.WriteFile(overlayPath, overlay, 0600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(directory, "future.exe")
	command := exec.Command("go", "test", "-c", "-overlay", overlayPath, "-o", binary, ".")
	command.Env = append(os.Environ(), "CGO_ENABLED=0")
	outputPath := filepath.Join(directory, "build.log")
	output, err := os.Create(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	command.Stdout = output
	command.Stderr = output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { finished <- command.Wait() }()
	select {
	case err := <-finished:
		if err != nil {
			contents, _ := os.ReadFile(outputPath)
			t.Fatalf("successor build: %v\n%s", err, contents)
		}
	case <-time.After(90 * time.Second):
		_ = command.Process.Kill()
		<-finished
		t.Fatal("successor fixture compilation timed out")
	}
	payload, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	startFixtureSupervisorWithPayload(t, root, "success", payload)
	checked := checkedFixture(t, root)
	fixtureExchange(t, root, "upgrade", checked.ManifestHash)
	updated := awaitFixture(t, root)
	if updated.Stage != "completed" || updated.Current.Version != "2" || !updated.RollbackAvailable {
		t.Fatalf("schema-compatible upgrade: %+v", updated)
	}
	fixtureExchange(t, root, "rollback", "")
	rolled := awaitFixture(t, root)
	if rolled.Stage != "rolled_back" || rolled.Current.Version != "1" {
		t.Fatalf("schema-compatible rollback: %+v", rolled)
	}
}
