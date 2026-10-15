package appupdate

import (
	"bytes"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"time"
)

func TestMain(suite *testing.M) {
	for _, argument := range os.Args[1:] {
		if argument == "--update-fixture" {
			os.Exit(runUpdateFixture())
		}
	}
	os.Exit(suite.Run())
}

func runUpdateFixture() int {
	root := ""
	for index, argument := range os.Args {
		if argument == "-path" && index+1 < len(os.Args) {
			root = os.Args[index+1]
		}
	}
	contains := func(name string) bool {
		for _, argument := range os.Args {
			if argument == name {
				return true
			}
		}
		return false
	}
	exists := func(name string) bool { _, err := os.Stat(filepath.Join(root, name)); return err == nil }
	if contains("-fixture-wait") {
		_, _ = io.Copy(io.Discard, os.Stdin)
		return 0
	}
	if contains("-update-supervisor") {
		if err := os.WriteFile(filepath.Join(root, "maintenance-started"), []byte("yes"), 0600); err != nil {
			return 14
		}
		return 0
	}
	if contains("-update-preflight") {
		if exists("reject-preflight") {
			return 2
		}
		return 0
	}
	if contains("-update-migrate") {
		if err := os.WriteFile(filepath.Join(root, "migration-result"), []byte("new schema"), 0o600); err != nil {
			return 3
		}
		if exists("reject-migration") {
			return 4
		}
		return 0
	}
	if !contains("-server-child") {
		return 5
	}
	go func() { _, _ = io.Copy(io.Discard, os.Stdin); os.Exit(0) }()
	session, err := ServerSession(root, nil, 0)
	if err != nil {
		return 12
	}
	defer session.Close()
	store, err := openStore(root)
	if err != nil {
		return 6
	}
	state, err := loadState(store)
	store.Close()
	if err != nil {
		return 7
	}
	executable, err := os.Executable()
	if err != nil {
		return 8
	}
	candidate := false
	if state.Candidate != nil {
		expected := filepath.Join(root, Directory, state.Candidate.Binary)
		canonical, _ := filepath.EvalSymlinks(expected)
		canonicalExe, _ := filepath.EvalSymlinks(executable)
		candidate = canonical == canonicalExe
	}
	if candidate && exists("reject-start") {
		return 9
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 10
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-SiteBrush-Update-Probe") != "fixture-nonce" {
			w.WriteHeader(403)
			return
		}
		w.WriteHeader(204)
	}), ReadHeaderTimeout: time.Second}
	go func() { _ = server.Serve(listener) }()
	tasks := make(chan ChildTask, 4)
	stop := make(chan struct{})
	go func() {
		if err := ChildContact(root, tasks, stop); err != nil {
			os.Exit(11)
		}
	}()
	for task := range tasks {
		result := ChildResult{Address: listener.Addr().String(), Nonce: "fixture-nonce"}
		if task.Action == "probe" && candidate && exists("reject-health") {
			result.Error = "fixture database cannot open"
		}
		if task.Action == "commit" {
			if err := os.WriteFile(filepath.Join(root, "writes-opened"), []byte("yes"), 0o600); err != nil {
				result.Error = err.Error()
			}
			if candidate && exists("reject-activation") {
				if err := os.WriteFile(filepath.Join(root, "accepted-after-activation"), []byte("new user write"), 0o600); err != nil {
					return 13
				}
				result.Error = "fixture failed after accepting a write"
			}
		}
		if task.Reply != nil {
			_ = ReplyChild(task, result, stop)
		}
		if task.Action == "stop" {
			close(stop)
			_ = server.Close()
			return 0
		}
	}
	return 0
}

// Keep fixture contacts below the CI ephemeral client range. Otherwise a
// preceding outgoing TLS connection can reserve a later fixture's hashed port.
func newFixtureInstallation(t *testing.T) string {
	t.Helper()
	for attempt := 0; attempt < 100; attempt++ {
		root := t.TempDir()
		address := Address(root)
		host, rawPort, err := net.SplitHostPort(address)
		if err != nil {
			t.Fatal(err)
		}
		port, err := strconv.Atoi(rawPort)
		if err != nil {
			t.Fatal(err)
		}
		if port >= 30000 {
			continue
		}
		contact, err := net.Listen("tcp", address)
		if err != nil {
			continue
		}
		session, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port+1)))
		contact.Close()
		if err != nil {
			continue
		}
		session.Close()
		return root
	}
	t.Fatal("cannot allocate an available fixture contact and process session")
	return ""
}

func startFixtureSupervisor(t *testing.T, root string, scenario string) (Options, <-chan struct{}, chan struct{}) {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	return startFixtureSupervisorWithPayload(t, root, scenario, payload)
}

func startFixtureSupervisorWithPayload(t *testing.T, root, scenario string, payload []byte) (Options, <-chan struct{}, chan struct{}) {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	manifest, publicKey, privateKey := testRelease(t, payload)
	if scenario == "third-release" {
		manifest.Version = "3"
		manifest.Sequence = 3
	}
	if strings.HasPrefix(scenario, "incompatible") {
		manifest.Compatibility = Compatibility{Generation: 2, ReadsFrom: 2, ReadsThrough: 2, WritesCompatible: false, MigratesFrom: 1}
	}
	signed, err := Sign(manifest, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/manifest" {
			_, _ = w.Write(signed)
			return
		}
		if scenario == "download-failure" {
			w.WriteHeader(503)
			return
		}
		_, _ = w.Write(payload)
	}))
	t.Cleanup(origin.Close)
	catalog := NewCatalog(publicKey)
	catalog.client = origin.Client()
	catalog.manifestURL = origin.URL + "/manifest"
	catalog.artifactBase = origin.URL + "/"
	options := Options{StoragePath: root, Executable: binary, Version: "1", Compatibility: Compatibility{Generation: 1, ReadsFrom: 1, ReadsThrough: 1, WritesCompatible: true}, Arguments: []string{"--update-fixture", "-path", root}, PublicKey: publicKey, Catalog: &catalog, Output: io.Discard, Stopped: scenario == "stopped", HealthWindow: 10 * time.Millisecond, HealthTimeout: 4 * time.Second}
	stop := make(chan struct{})
	done := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		defer close(done)
		runErr := Run(options, stop)

		result <- runErr
	}()
	t.Cleanup(func() {
		select {
		case <-stop:
		default:
			close(stop)
		}
		select {
		case <-done:
		case <-time.After(60 * time.Second):
			t.Error("supervisor did not stop")
		}
	})
	deadline := time.Now().Add(10 * time.Second)
	for {
		if response, err := Exchange(root, Request{Action: "status"}, make(chan struct{})); err == nil && !response.State.Busy {
			break
		}
		select {
		case err := <-result:
			t.Fatalf("supervisor startup: %v", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("supervisor did not become available")
		}
		time.Sleep(20 * time.Millisecond)
	}
	return options, done, stop
}

func fixtureExchange(t *testing.T, root, action, digest string) Response {
	t.Helper()
	response, err := Exchange(root, Request{Action: action, ManifestHash: digest}, make(chan struct{}))
	if err != nil {
		contents, _ := os.ReadFile(filepath.Join(root, Directory, "state.json"))
		t.Fatalf("%s: %v journal=%s", action, err, contents)
	}
	return response
}

func checkedFixture(t *testing.T, root string) State {
	t.Helper()
	before := fixtureExchange(t, root, "status", "").State.CheckedAt
	fixtureExchange(t, root, "check", "")
	deadline := time.Now().Add(10 * time.Second)
	for {
		state := fixtureExchange(t, root, "status", "").State
		if state.CheckedAt != before {
			if state.Error != "" {
				t.Fatal(state.Error)
			}
			return state
		}
		if time.Now().After(deadline) {
			t.Fatal("release check timed out")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func awaitFixture(t *testing.T, root string) State {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		state := fixtureExchange(t, root, "status", "").State
		if !state.Busy {
			return state
		}
		if time.Now().After(deadline) {
			t.Fatalf("operation timed out at %s: %s", state.Stage, state.Error)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestSupervisorUpdateRollbackAndFailures(t *testing.T) {
	for _, scenario := range []string{"success", "download-failure", "reject-preflight", "reject-migration", "reject-start", "reject-health", "incompatible"} {
		t.Run(scenario, func(t *testing.T) {
			root := newFixtureInstallation(t)
			if err := os.WriteFile(filepath.Join(root, "user-content"), []byte("preserve me"), 0o640); err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(scenario, "reject-") {
				if err := os.WriteFile(filepath.Join(root, scenario), []byte("yes"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			startFixtureSupervisor(t, root, scenario)
			checked := checkedFixture(t, root)
			if _, err := Exchange(root, Request{Action: "upgrade", ManifestHash: "stale"}, make(chan struct{})); err == nil {
				t.Fatal("accepted stale manifest confirmation")
			}
			fixtureExchange(t, root, "upgrade", checked.ManifestHash)
			if _, err := Exchange(root, Request{Action: "rollback"}, make(chan struct{})); err == nil {
				t.Fatal("accepted concurrent version change")
			}
			state := awaitFixture(t, root)
			if scenario == "success" || scenario == "incompatible" {
				if state.Stage != "completed" || state.Current.Version != "2" || state.Previous == nil || state.Previous.Version != "1" {
					t.Fatalf("update state=%+v", state)
				}
				if scenario == "incompatible" {
					if state.RollbackAvailable {
						t.Fatal("unsafe rollback advertised")
					}
					if _, err := Exchange(root, Request{Action: "rollback"}, make(chan struct{})); err == nil {
						t.Fatal("unsafe rollback accepted")
					}
				} else {
					if !state.RollbackAvailable {
						t.Fatal("safe rollback unavailable")
					}
					fixtureExchange(t, root, "rollback", "")
					rolled := awaitFixture(t, root)
					if rolled.Stage != "rolled_back" || rolled.Current.Version != "1" {
						t.Fatalf("manual rollback: %+v", rolled)
					}
				}
			} else {
				if state.Current.Version != "1" || state.OperationError == "" {
					t.Fatalf("failure damaged current state: %+v", state)
				}
				if strings.HasPrefix(scenario, "reject-") && scenario != "reject-preflight" {
					if state.Stage != "rolled_back" {
						t.Fatalf("automatic rollback: %+v", state)
					}
					if _, err := os.Stat(filepath.Join(root, "migration-result")); !os.IsNotExist(err) {
						t.Fatal("failed migration data survived backup recovery")
					}
				}
			}
			contents, err := os.ReadFile(filepath.Join(root, "user-content"))
			if err != nil || !bytes.Equal(contents, []byte("preserve me")) {
				t.Fatal("user content changed")
			}
		})
	}
}

func TestRecoveryRestoresInterruptedMigration(t *testing.T) {
	root := newFixtureInstallation(t)
	if err := os.WriteFile(filepath.Join(root, "content"), []byte("before"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	state, err := bootstrap(store, Options{Executable: executable, Version: "1", Compatibility: Compatibility{Generation: 1, ReadsFrom: 1, ReadsThrough: 1, WritesCompatible: true}})
	if err != nil {
		t.Fatal(err)
	}
	state.Backup, err = snapshot(root, store, "interrupted", make(chan struct{}))
	if err != nil {
		t.Fatal(err)
	}
	state.Busy = true
	state.WritesOpened = false
	state.Stage = "installing"
	if err := atomicJSON(store, "state.json", state); err != nil {
		t.Fatal(err)
	}
	store.Close()
	if err := os.WriteFile(filepath.Join(root, "content"), []byte("half-migrated"), 0o600); err != nil {
		t.Fatal(err)
	}
	startFixtureSupervisor(t, root, "success")
	contents, err := os.ReadFile(filepath.Join(root, "content"))
	if err != nil || string(contents) != "before" {
		t.Fatalf("interrupted data recovery: %q %v", contents, err)
	}
	recovered := fixtureExchange(t, root, "status", "").State
	if recovered.Busy || recovered.Current.Version != "1" {
		t.Fatalf("recovered state=%+v", recovered)
	}
}

func TestActivationFailureRollsBackWithoutDiscardingNewWrites(t *testing.T) {
	root := newFixtureInstallation(t)
	if err := os.WriteFile(filepath.Join(root, "reject-activation"), []byte("yes"), 0o600); err != nil {
		t.Fatal(err)
	}
	startFixtureSupervisor(t, root, "success")
	checked := checkedFixture(t, root)
	fixtureExchange(t, root, "upgrade", checked.ManifestHash)
	state := awaitFixture(t, root)
	if state.Stage != "rolled_back" || state.Current.Version != "1" || state.OperationError == "" {
		t.Fatalf("compatible activation recovery failed: %+v", state)
	}
	contents, err := os.ReadFile(filepath.Join(root, "accepted-after-activation"))
	if err != nil || string(contents) != "new user write" {
		t.Fatal("activation rollback restored an old snapshot over newly accepted writes")
	}
}

func TestIncompatibleActivationFailureNeverRestoresOverNewWrites(t *testing.T) {
	root := newFixtureInstallation(t)
	if err := os.WriteFile(filepath.Join(root, "reject-activation"), []byte("yes"), 0o600); err != nil {
		t.Fatal(err)
	}
	startFixtureSupervisor(t, root, "incompatible-activation")
	checked := checkedFixture(t, root)
	fixtureExchange(t, root, "upgrade", checked.ManifestHash)
	state := awaitFixture(t, root)
	if state.Stage != "failed" || state.Current.Version != "2" || state.RollbackAvailable || state.OperationError == "" {
		t.Fatalf("unsafe activation recovery: %+v", state)
	}
	contents, err := os.ReadFile(filepath.Join(root, "accepted-after-activation"))
	if err != nil || string(contents) != "new user write" {
		t.Fatal("unsafe recovery discarded a new write", err)
	}
	if _, err := Exchange(root, Request{Action: "rollback"}, nil); err == nil {
		t.Fatal("incompatible manual rollback was allowed")
	}
}

func TestSupervisorRejectsSecondOwnerAndAnonymousTLS(t *testing.T) {
	root := newFixtureInstallation(t)
	options, _, _ := startFixtureSupervisor(t, root, "success")
	if err := Run(options, make(chan struct{})); err == nil {
		t.Fatal("second supervisor acquired the installation")
	}
	store, err := openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	configuration, err := tlsIdentity(store, false)
	if err != nil {
		t.Fatal(err)
	}
	configuration.Certificates = nil
	connection, err := tls.DialWithDialer(&net.Dialer{Timeout: time.Second}, "tcp", Address(root), configuration)
	if err == nil {
		defer connection.Close()
		_ = connection.SetDeadline(time.Now().Add(time.Second))
		_, _ = connection.Write([]byte("unauthenticated"))
		var response [1]byte
		if _, err := connection.Read(response[:]); err == nil {
			t.Fatal("anonymous TLS peer received authenticated traffic")
		}
	}
}

func TestRuntimePreservesActualPortAndWorkingDirectory(t *testing.T) {
	for _, scenario := range []struct {
		name      string
		arguments []string
		address   string
		expected  []string
	}{
		{"fallback", []string{"-port", "80,443", "-debug"}, "127.0.0.1:8080", []string{"-port", "8080", "-debug"}},
		{"tls", []string{"-port", "80,443"}, "127.0.0.1:80", []string{"-port", "80,443"}},
		{"custom", []string{"-port", "9000"}, "127.0.0.1:9000", []string{"-port", "9000"}},
		{"missing", []string{"-debug"}, "127.0.0.1:9001", []string{"-debug", "-port", "9001"}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			original := append([]string(nil), scenario.arguments...)
			arguments, err := argumentsForAddress(scenario.arguments, scenario.address)
			if err != nil || !reflect.DeepEqual(arguments, scenario.expected) {
				t.Fatalf("arguments=%v error=%v", arguments, err)
			}
			if !reflect.DeepEqual(original, scenario.arguments) {
				t.Fatal("configuration changed while owned by its caller")
			}
		})
	}
	for _, address := range []string{"example.com:80", "[::1]:80", "127.0.0.1:0", "127.0.0.1:65536", "127.0.0.1:http"} {
		if _, err := argumentsForAddress(nil, address); err == nil {
			t.Fatalf("accepted invalid private contact %q", address)
		}
	}
	root := newFixtureInstallation(t)
	startFixtureSupervisor(t, root, "success")
	if _, err := Exchange(root, Request{Action: "server_ready", Detail: "127.0.0.1:9123"}, nil); err != nil {
		t.Fatal(err)
	}
	arguments, directory, err := InstalledRuntime(root)
	if err != nil || directory == "" || !reflect.DeepEqual(arguments[len(arguments)-2:], []string{"-port", "9123"}) {
		t.Fatalf("lost actual server contact: %v %q %v", arguments, directory, err)
	}
}

func TestStoppedInstallationRemainsStoppedAfterUpgrade(t *testing.T) {
	root := newFixtureInstallation(t)
	startFixtureSupervisor(t, root, "stopped")
	checked := checkedFixture(t, root)
	fixtureExchange(t, root, "upgrade", checked.ManifestHash)
	state := awaitFixture(t, root)
	if state.Stage != "completed" || state.Current.Version != "2" {
		t.Fatalf("offline update failed: %+v", state)
	}
	if _, err := os.Stat(filepath.Join(root, "writes-opened")); !os.IsNotExist(err) {
		t.Fatal("offline health check activated application writes")
	}
	arguments, directory, err := InstalledRuntime(root)
	if err != nil || serverPathArgument(arguments) != root || directory == "" {
		t.Fatalf("lost installation runtime: %v %q %v", arguments, directory, err)
	}
	session, err := ServerSession(root, nil, time.Second)
	if err != nil {
		t.Fatalf("offline update left a server running: %v", err)
	}
	session.Close()
}

func serverPathArgument(arguments []string) string {
	for index, argument := range arguments {
		if argument == "-path" && index+1 < len(arguments) {
			return arguments[index+1]
		}
	}
	return ""
}

func TestSuccessiveUpdatesAndRollbackPruneSupersededArtifacts(t *testing.T) {
	root := newFixtureInstallation(t)
	_, done, stop := startFixtureSupervisor(t, root, "success")
	original := fixtureExchange(t, root, "status", "").State.Current.Binary
	checked := checkedFixture(t, root)
	fixtureExchange(t, root, "upgrade", checked.ManifestHash)
	second := awaitFixture(t, root)
	if second.Stage != "completed" || second.CleanupError != "" {
		t.Fatal(second)
	}
	firstBackup := second.Backup
	close(stop)
	<-done
	startFixtureSupervisor(t, root, "third-release")
	checked = checkedFixture(t, root)
	fixtureExchange(t, root, "upgrade", checked.ManifestHash)
	third := awaitFixture(t, root)
	if third.Current.Version != "3" || third.Previous.Version != "2" || third.CleanupError != "" {
		t.Fatal(third)
	}
	for _, obsolete := range []string{original, firstBackup} {
		if _, err := os.Stat(filepath.Join(root, Directory, obsolete)); !os.IsNotExist(err) {
			t.Fatal("superseded upgrade artifact retained", obsolete, err)
		}
	}
	fixtureExchange(t, root, "rollback", "")
	rolled := awaitFixture(t, root)
	if rolled.Current.Version != "2" || rolled.Previous.Version != "3" || rolled.CleanupError != "" {
		t.Fatal(rolled)
	}
	if _, err := os.Stat(filepath.Join(root, Directory, third.Backup)); !os.IsNotExist(err) {
		t.Fatal("superseded snapshot survived rollback", err)
	}
	for _, retained := range []string{rolled.Current.Binary, rolled.Previous.Binary, rolled.Backup} {
		if _, err := os.Stat(filepath.Join(root, Directory, retained)); err != nil {
			t.Fatal("active artifact lost", retained, err)
		}
	}
}

func TestFailedPreparationRetainsLastWorkingSnapshot(t *testing.T) {
	root := newFixtureInstallation(t)
	startFixtureSupervisor(t, root, "success")
	checked := checkedFixture(t, root)
	fixtureExchange(t, root, "upgrade", checked.ManifestHash)
	second := awaitFixture(t, root)
	if err := os.WriteFile(filepath.Join(root, "reject-preflight"), []byte("yes"), 0600); err != nil {
		t.Fatal(err)
	}
	fixtureExchange(t, root, "rollback", "")
	rolled := awaitFixture(t, root)
	if rolled.Stage != "rolled_back" {
		t.Fatal(rolled)
	}
	checked = checkedFixture(t, root)
	fixtureExchange(t, root, "upgrade", checked.ManifestHash)
	failed := awaitFixture(t, root)
	if failed.Stage != "failed" || failed.RetainedBackup != rolled.Backup || failed.CleanupError != "" {
		t.Fatal(failed)
	}
	if _, err := os.Stat(filepath.Join(root, Directory, rolled.Backup)); err != nil {
		t.Fatal("failed preparation deleted last recovery snapshot", err)
	}
	if _, err := os.Stat(filepath.Join(root, Directory, second.Backup)); !os.IsNotExist(err) {
		t.Fatal("superseded snapshot retained", err)
	}
}

func TestStartupPrunesOrphanedArtifactsWithoutTouchingWorkingContent(t *testing.T) {
	root := newFixtureInstallation(t)
	orphan := strings.Repeat("a", 32)
	for _, name := range []string{filepath.Join(root, Directory, orphan, "backup", "partial"), filepath.Join(root, Directory, "versions", orphan, "candidate"), filepath.Join(root, "user-content")} {
		if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte("keep user content"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	startFixtureSupervisor(t, root, "success")
	for _, name := range []string{filepath.Join(root, Directory, orphan), filepath.Join(root, Directory, "versions", orphan)} {
		if _, err := os.Stat(name); !os.IsNotExist(err) {
			t.Fatal("startup left orphaned artifacts", name, err)
		}
	}
	if contents, err := os.ReadFile(filepath.Join(root, "user-content")); err != nil || string(contents) != "keep user content" {
		t.Fatal("cleanup changed working content", err)
	}
	for _, request := range []Request{{Action: "unknown"}, {Action: "attach"}, {Action: "upgrade"}, {Action: "rollback"}, {Action: "server_ready", Detail: "evil.example:80"}} {
		if _, err := Exchange(root, request, nil); err == nil {
			t.Fatal("invalid command accepted", request.Action)
		}
	}
	response, err := Exchange(root, Request{Action: "notification_status", Detail: "legacy mail status"}, nil)
	if err != nil || response.State.NotificationError != "legacy mail status" {
		t.Fatal(response, err)
	}
}

func TestRestartFinishesInterruptedCleanupWithoutRestoringOldData(t *testing.T) {
	root := newFixtureInstallation(t)
	_, done, stop := startFixtureSupervisor(t, root, "success")
	checked := checkedFixture(t, root)
	fixtureExchange(t, root, "upgrade", checked.ManifestHash)
	completed := awaitFixture(t, root)
	if completed.Stage != "completed" {
		t.Fatal(completed)
	}
	close(stop)
	<-done
	if err := os.WriteFile(filepath.Join(root, "after-activation"), []byte("accepted user write"), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := openStore(root)
	if err != nil {
		t.Fatal(err)
	}
	completed.Busy = true
	if err := atomicJSON(store, "state.json", completed); err != nil {
		t.Fatal(err)
	}
	orphan := strings.Repeat("f", 32)
	if err := store.MkdirAll(filepath.Join(orphan, "backup"), 0700); err != nil {
		t.Fatal(err)
	}
	store.Close()
	startFixtureSupervisor(t, root, "success")
	recovered := fixtureExchange(t, root, "status", "").State
	if recovered.Stage != "completed" || recovered.Error != "" || recovered.CleanupError != "" || recovered.Current.Version != "2" {
		t.Fatal("cleanup interruption changed the committed operation", recovered)
	}
	if contents, err := os.ReadFile(filepath.Join(root, "after-activation")); err != nil || string(contents) != "accepted user write" {
		t.Fatal("cleanup recovery overwrote new user data", err)
	}
	if _, err := os.Stat(filepath.Join(root, Directory, orphan)); !os.IsNotExist(err) {
		t.Fatal("interrupted cleanup was not retried", err)
	}
}
