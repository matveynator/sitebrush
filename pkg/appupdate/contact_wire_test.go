package appupdate

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/matveynator/netchan"
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
	root := newFixtureInstallation(t)
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

func TestLifecycleBridgesRevokeBrokenConsumers(t *testing.T) {
	t.Run("closed request stream", func(t *testing.T) {
		tasks := make(chan ChildTask)
		close(tasks)
		done := make(chan struct{})
		go sendChildWire(tasks, nil, nil, done)
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("closed task stream retained bridge")
		}
	})
	t.Run("revoked result consumer", func(t *testing.T) {
		tasks := make(chan ChildTask, 1)
		reply := make(chan ChildResult)
		close(reply)
		remote := make(chan wireChildTask, 1)
		done := make(chan struct{})
		tasks <- ChildTask{Action: "probe", Reply: reply}
		go sendChildWire(tasks, remote, nil, done)
		task := <-remote
		task.Reply <- []byte(`{"Address":"localhost:80"}`)
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("revoked result consumer retained bridge")
		}
	})
	t.Run("closed inbound session", func(t *testing.T) {
		session := make(chan wireChildTask)
		close(session)
		done := make(chan struct{})
		go receiveChildWire(session, nil, nil, done)
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("closed session retained bridge")
		}
	})
	for _, scenario := range []string{"closed child tasks", "closed child reply", "oversized child result", "revoked wire reply"} {
		t.Run(scenario, func(t *testing.T) {
			tasks := make(chan ChildTask)
			session := make(chan wireChildTask, 1)
			reply := make(chan []byte, 1)
			stop := make(chan struct{})
			done := make(chan struct{})
			defer close(stop)
			if scenario == "closed child tasks" {
				close(tasks)
			}
			if scenario == "revoked wire reply" {
				close(reply)
			}
			go receiveChildWire(session, tasks, stop, done)
			session <- wireChildTask{Action: "probe", Reply: reply}
			if scenario != "closed child tasks" {
				task := <-tasks
				switch scenario {
				case "closed child reply":
					close(task.Reply)
				case "oversized child result":
					task.Reply <- ChildResult{Error: strings.Repeat("x", contactPayloadLimit)}
				default:
					task.Reply <- ChildResult{}
				}
			}
			if scenario == "closed child tasks" || scenario == "closed child reply" {
				select {
				case payload := <-reply:
					var result ChildResult
					if err := json.Unmarshal(payload, &result); err != nil || result.Error == "" {
						t.Fatalf("closed child accepted: %s %v", payload, err)
					}
				case <-time.After(time.Second):
					t.Fatal("closed child did not return failure")
				}
				close(session)
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("broken lifecycle retained bridge")
			}
		})
	}
	closed := make(chan wireChildTask)
	close(closed)
	if result := exchangeChildWire(closed, "probe", nil); result.Error == "" {
		t.Fatal("closed network capability accepted")
	}
}

func TestChildContactRejectsStartupFailureAndRevokedLifecycle(t *testing.T) {
	for _, scenario := range []string{"rejected", "invalid result", "revoked lifecycle"} {
		t.Run(scenario, func(t *testing.T) {
			root := newFixtureInstallation(t)
			store, err := openStore(root)
			if err != nil {
				t.Fatal(err)
			}
			configuration, err := tlsIdentity(store, true)
			store.Close()
			if err != nil {
				t.Fatal(err)
			}
			listener, err := netchan.Listen[wireRequest](Address(root), netchan.Config{TLS: configuration, Capacity: 4})
			if err != nil {
				t.Fatal(err)
			}
			serverStop := make(chan struct{})
			serverDone := make(chan struct{})
			serverResult := make(chan error, 1)
			go func() {
				defer close(serverDone)
				select {
				case connection, open := <-listener.Channels:
					if !open {
						return
					}
					defer connection.Abort()
					var attached wireRequest
					select {
					case attached = <-connection.Receive:
					case <-serverStop:
						return
					}
					response, _ := encodeContactResult(Response{})
					if scenario == "rejected" {
						response, _ = encodeContactResult(Response{Error: "fixture rejected startup"})
					}
					if scenario == "invalid result" {
						response.Result = []byte("broken")
					}
					if err := connection.Deliver(response); err != nil {
						serverResult <- err
						return
					}
					if scenario == "revoked lifecycle" {
						close(attached.Child)
					}
					<-serverStop
				case <-serverStop:
				}
			}()
			defer func() { close(serverStop); listener.Close(); <-serverDone }()
			stop := make(chan struct{})
			result := make(chan error, 1)
			clientDone := make(chan struct{})
			go func() { defer close(clientDone); result <- ChildContact(root, make(chan ChildTask), stop) }()
			defer func() {
				close(stop)
				select {
				case <-clientDone:
				case <-time.After(5 * time.Second):
					t.Error("child contact did not stop")
				}
			}()
			select {
			case err := <-result:
				if err == nil {
					t.Fatal("broken supervisor contact accepted")
				}
				if scenario == "rejected" && !strings.Contains(err.Error(), "fixture rejected startup") {
					t.Fatalf("startup failure not propagated: %v", err)
				}
				if scenario == "revoked lifecycle" && !strings.Contains(err.Error(), "lifecycle contact terminated") {
					t.Fatalf("revoked lifecycle remained attached: %v", err)
				}
			case err := <-serverResult:
				t.Fatal(err)
			case <-time.After(5 * time.Second):
				t.Fatal("broken contact did not terminate")
			}
		})
	}
}
