package appupdate

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/matveynator/netchan"
)

type Options struct {
	WorkingDirectory string
	StoragePath      string
	Executable       string
	Version          string
	Compatibility    Compatibility
	Arguments        []string
	PublicKey        ed25519.PublicKey
	Output           io.Writer
	Stopped          bool
	HealthWindow     time.Duration
	HealthTimeout    time.Duration
	// Catalog is only injectable by Go callers; remote requests cannot select it.
	Catalog *Catalog
}

type transition struct {
	state State
	reply chan error
}
type catalogResult struct {
	manifest Manifest
	digest   string
	err      error
}

func Run(options Options, stop <-chan struct{}) error {
	if options.WorkingDirectory == "" {
		directory, err := os.Getwd()
		if err != nil {
			return err
		}
		options.WorkingDirectory = directory
	}
	store, err := openStore(options.StoragePath)
	if err != nil {
		return err
	}
	defer store.Close()
	configuration, err := tlsIdentity(store, true)
	if err != nil {
		return err
	}
	listener, err := netchan.Listen[wireRequest](Address(options.StoragePath), netchan.Config{Capacity: 8, TLS: configuration})
	if err != nil {
		return fmt.Errorf("another supervisor or conflicting listener owns this installation: %w", err)
	}
	defer listener.Close()
	maintenanceSession, err := ServerSession(options.StoragePath, stop, 60*time.Second)
	if err != nil {
		return err
	}
	state, err := loadState(store)
	if errors.Is(err, os.ErrNotExist) {
		state, err = bootstrap(store, options)
	}
	if err != nil {
		maintenanceSession.Close()
		return err
	}
	if state.Busy {
		// Terminal stages are written before pruning. An interrupted cleanup
		// must not turn a committed update into a failed version operation.
		if state.Stage != "completed" && state.Stage != "rolled_back" {
			if !state.WritesOpened && state.Backup != "" {
				if err := restore(options.StoragePath, store, state.Backup); err != nil {
					maintenanceSession.Close()
					return fmt.Errorf("interrupted update recovery: %w", err)
				}
			}
			state.Stage = "failed"
			state.Error = "Interrupted version operation recovered before startup"
			if state.Progress.Action != "" {
				state.Progress.FailedStage = state.Progress.Stage
				state.Progress.Stage = "failed"
				state.Progress.FinishedAt = time.Now().UTC()
				state.Progress.event("failed", "interrupted", state.Error)
			}
		}
		state.Busy = false
		if err := atomicJSON(store, "state.json", state); err != nil {
			maintenanceSession.Close()
			return err
		}
	}
	if options.Stopped && state.WorkingDirectory != "" {
		options.WorkingDirectory = state.WorkingDirectory
	}
	state.Arguments = append([]string(nil), options.Arguments...)
	state.WorkingDirectory = options.WorkingDirectory
	if err := atomicJSON(store, "state.json", state); err != nil {
		maintenanceSession.Close()
		return err
	}
	if err := maintenanceSession.Close(); err != nil {
		return err
	}
	controllerStop := make(chan struct{})
	worker := startProcess(processOptions{storagePath: options.StoragePath, arguments: options.Arguments, directory: options.WorkingDirectory, output: options.Output, stop: controllerStop})
	defer func() { close(controllerStop); <-worker.done }()
	if !options.Stopped {
		if err := verifiedStart(store, worker, state.Current, false); err != nil {
			return err
		}
	}
	requests := make(chan Request, 16)
	sessions := make(chan struct{}, 16)
	go acceptContacts(listener, requests, sessions, stop)
	progress := make(chan transition)
	finished := make(chan State, 1)
	state.Busy = true
	go func(initial State) { finished <- settleOperation(store, initial, progress, stop) }(state)
	checks := make(chan catalogResult, 1)
	catalog := NewCatalog(options.PublicKey)
	if options.Catalog != nil {
		catalog = *options.Catalog
	}
	checking := false
	startCheck := func() {
		if checking || state.Busy {
			return
		}
		checking = true
		go func() {
			manifest, digest, err := catalog.Check(stop)
			select {
			case checks <- catalogResult{manifest: manifest, digest: digest, err: err}:
			case <-stop:
			}
		}()
	}
	initial := time.NewTimer(10 * time.Second)
	defer initial.Stop()
	jitter := time.Duration(time.Now().UnixNano() % int64(15*time.Minute))
	ticker := time.NewTicker(6*time.Hour + jitter)
	defer ticker.Stop()
	idle := time.NewTicker(time.Minute)
	defer idle.Stop()
	lastContact := time.Now()
	var loggedSequence uint64
	logOperation := func() {
		sequence, _ := WriteOperationEvents(options.Output, state.Progress, loggedSequence)
		loggedSequence = sequence
	}
	for {
		select {
		case err := <-worker.failures:
			if !state.Busy && !options.Stopped {
				return fmt.Errorf("server process failed: %w", err)
			}
		case <-stop:
			// The worker must finish or persist its recovery boundary before the
			// store and process controller are closed.
			if state.Busy {
				for {
					select {
					case step := <-progress:
						step.reply <- atomicJSON(store, "state.json", step.state)
					case state = <-finished:
						_ = atomicJSON(store, "state.json", state)
						return nil
					}
				}
			}
			return nil
		case <-initial.C:
			startCheck()
		case <-ticker.C:
			startCheck()
		case <-idle.C:
			if options.Stopped && !state.Busy && !checking && time.Since(lastContact) > time.Minute {
				return nil
			}
		case err := <-listener.Errors:
			if options.Output != nil {
				fmt.Fprintf(options.Output, "update transport: %v\n", err)
			}
		case checked := <-checks:
			checking = false
			state.CheckedAt = time.Now().UTC().Format(time.RFC3339Nano)
			if checked.err != nil {
				state.Error = checked.err.Error()
			} else if checked.manifest.Sequence < state.HighestSequence {
				state.Error = "official metadata moved backwards"
			} else {
				state.HighestSequence = checked.manifest.Sequence
				state.ManifestHash = checked.digest
				state.Available = &checked.manifest
				state.Error = ""
				installed, _ := strconv.ParseUint(state.Current.Version, 10, 64)
				if checked.manifest.Sequence > installed {
					state.Stage = "available"
				} else {
					state.Stage = "current"
				}
			}
			if err := atomicJSON(store, "state.json", state); err != nil {
				return err
			}
		case step := <-progress:
			step.state.NotificationError = state.NotificationError
			if err := atomicJSON(store, "state.json", step.state); err != nil {
				step.reply <- err
			} else {
				state = step.state
				logOperation()
				step.reply <- nil
			}
		case completed := <-finished:
			completed.NotificationError = state.NotificationError
			state = completed
			if err := atomicJSON(store, "state.json", state); err != nil {
				return err
			}
			logOperation()
			if !options.Stopped {
				if err := worker.exchange("alive", Version{}, false); err != nil {
					return fmt.Errorf("version operation ended without a running server: %w", err)
				}
			}
		case request := <-requests:
			lastContact = time.Now()
			response := Response{State: state}
			response.State.Busy = state.Busy || checking
			response.State.Checking = checking
			switch request.Action {
			case "status":
			case "server_ready":
				if !state.Busy {
					arguments, err := argumentsForAddress(state.Arguments, request.Detail)
					if err != nil {
						response.Error = err.Error()
						break
					}
					if err := worker.configure(arguments); err != nil {
						response.Error = err.Error()
						break
					}
					state.Arguments = arguments
					if err := atomicJSON(store, "state.json", state); err != nil {
						response.Error = err.Error()
					}
					response.State = state
				}
			case "notification_status":
				if len(request.Detail) <= 512 && state.NotificationError != request.Detail {
					state.NotificationError = request.Detail
					if err := atomicJSON(store, "state.json", state); err != nil {
						response.Error = err.Error()
					}
					response.State = state
				}
			case "attach":
				if request.Child == nil {
					response.Error = "missing child session"
					break
				}
				reply := make(chan error, 1)
				select {
				case worker.tasks <- processTask{action: "attach", child: request.Child, reply: reply}:
				case <-worker.done:
					return errors.New("process controller stopped")
				}
				if err := <-reply; err != nil {
					response.Error = err.Error()
				}
			case "check":
				if state.Busy {
					response.Error = "version operation is already running"
				} else {
					startCheck()
					response.State.Busy = true
					response.State.Checking = true
				}
			case "upgrade", "rollback":
				if state.Busy || checking {
					response.Error = "version operation is already running"
					break
				}
				if request.Action == "upgrade" && (state.Available == nil || request.ManifestHash == "" || request.ManifestHash != state.ManifestHash) {
					response.Error = "check and confirm the displayed official release first"
					break
				}
				if request.Action == "rollback" && (!state.RollbackAvailable || state.Previous == nil) {
					response.Error = "safe rollback is not available"
					break
				}
				identifier, err := Identifier()
				if err != nil {
					response.Error = err.Error()
					break
				}
				state.Busy = true
				state.Operation = identifier
				state.Error = ""
				state.OperationError = ""
				state.Stage = "preparing"
				target := state.Previous
				if request.Action == "upgrade" {
					target = &Version{Version: state.Available.Version}
				}
				state.Progress = OperationProgress{Action: request.Action, Stage: "preparing", FromVersion: state.Current.Version, TargetVersion: target.Version, StartedAt: time.Now().UTC(), StageAt: time.Now().UTC()}
				state.Progress.event("preparing", "stage_started", "")
				loggedSequence = 0
				if state.Backup != "" {
					state.RetainedBackup = state.Backup
				}
				state.Backup = ""
				state.Candidate = nil
				state.WritesOpened = false
				if err := atomicJSON(store, "state.json", state); err != nil {
					return err
				}
				response.State = state
				logOperation()
				go func(initial State, action string) {
					result := perform(options, store, catalog, worker, initial, action, progress, stop)
					finished <- settleOperation(store, result, progress, stop)
				}(state, request.Action)
			default:
				response.Error = "unknown update command"
			}
			if request.Reply != nil {
				_ = sendChannel(request.Reply, response, stop, 2*time.Second)
			}
		}
	}
}

func settleOperation(store *os.Root, state State, progress chan<- transition, stop <-chan struct{}) State {
	if state.Backup != "" && (state.Stage == "completed" || state.Stage == "rolled_back") {
		state.RetainedBackup = ""
	}
	state.Busy = true
	state.CleanupError = ""
	if state.Progress.Action != "" && state.Progress.FinishedAt.IsZero() {
		if state.Stage == "completed" || state.Stage == "rolled_back" {
			state.Progress.finishStage()
		}
		state.Progress.Stage = state.Stage
		state.Progress.event(state.Stage, "cleanup_started", "")
	}
	reply := make(chan error, 1)
	progress <- transition{state: state, reply: reply}
	if err := <-reply; err != nil {
		state.CleanupError = "cleanup journal: " + err.Error()
	} else if err := pruneArtifacts(store, state, stop); err != nil {
		state.CleanupError = err.Error()
	}
	state.Busy = false
	if state.Progress.Action != "" && state.Progress.FinishedAt.IsZero() {
		state.Progress.Stage = state.Stage
		state.Progress.FinishedAt = time.Now().UTC()
		if state.CleanupError != "" {
			state.Progress.event(state.Stage, "cleanup_warning", state.CleanupError)
		}
		state.Progress.event(state.Stage, "finished", state.Error)
	}
	return state
}

func acceptContacts(listener *netchan.Listener[wireRequest], requests chan<- Request, sessions chan struct{}, stop <-chan struct{}) {
	for {
		select {
		case <-stop:
			return
		case <-listener.Done:
			return
		case connection, open := <-listener.Channels:
			if !open {
				return
			}
			select {
			case sessions <- struct{}{}:
				go func() {
					defer func() { <-sessions }()
					graceful := false
					bridgeStop := make(chan struct{})
					var bridgeDone chan struct{}
					defer func() {
						if graceful {
							_ = connection.Close()
						} else {
							_ = connection.Abort()
						}
					}()
					defer func() {
						close(bridgeStop)
						if bridgeDone != nil {
							<-bridgeDone
						}
					}()
					for {
						select {
						case <-stop:
							return
						case <-bridgeDone:
							return
						case <-connection.Done:
							return
						case message, open := <-connection.Receive:
							if !open {
								return
							}
							if message.Protocol != Protocol {
								return
							}
							request := Request{Action: message.Action, ManifestHash: message.ManifestHash, Detail: message.Detail}
							if message.Child != nil {
								if request.Action != "attach" || bridgeDone != nil {
									return
								}
								tasks := make(chan ChildTask, 8)
								bridgeDone = make(chan struct{})
								go sendChildWire(tasks, message.Child, bridgeStop, bridgeDone)
								request.Child = tasks
							}
							reply := make(chan Response, 1)
							request.Reply = reply
							select {
							case requests <- request:
							case <-stop:
								return
							case <-connection.Done:
								return
							default:
								return
							}
							select {
							case response := <-reply:
								result, err := encodeContactResult(response)
								if err != nil {
									return
								}
								if err := connection.Deliver(result); err != nil {
									return
								}
								if request.Action != "attach" {
									graceful = true
									return
								}
							case <-stop:
								return
							case <-connection.Done:
								return
							}
						}
					}
				}()
			default:
				_ = connection.Abort()
			}
		}
	}
}

func bootstrap(store *os.Root, options Options) (State, error) {
	identifier, err := Identifier()
	if err != nil {
		return State{}, err
	}
	binary := filepath.Join("versions", identifier, BinaryName(runtime.GOOS, runtime.GOARCH))
	source, err := os.OpenRoot(filepath.Dir(options.Executable))
	if err != nil {
		return State{}, err
	}
	defer source.Close()
	digest, err := copyFile(source, filepath.Base(options.Executable), store, binary, 0o700)
	if err != nil {
		return State{}, err
	}
	if err := syncAncestors(store, filepath.Dir(binary)); err != nil {
		return State{}, err
	}
	state := State{Launcher: options.Executable, Stage: "current", Current: Version{Version: options.Version, Binary: binary, SHA256: digest, Compatibility: options.Compatibility}, WritesOpened: true, RollbackReason: "No previous working version"}
	return state, atomicJSON(store, "state.json", state)
}

func verifiedStart(store *os.Root, worker process, version Version, probation bool) error {
	digest, err := fileDigest(store, version.Binary)
	if err != nil {
		return err
	}
	if digest != version.SHA256 {
		return errors.New("installed binary checksum mismatch")
	}
	return worker.exchange("start", version, probation)
}

func perform(options Options, store *os.Root, catalog Catalog, worker process, state State, action string, progress chan<- transition, stop <-chan struct{}) State {
	old := state.Current
	previousBefore := state.Previous
	stopped := false
	changed := false
	persist := func() error {
		reply := make(chan error, 1)
		progress <- transition{state: state, reply: reply}
		return <-reply
	}
	emit := func(stage string) error {
		if state.Progress.Stage != stage {
			if state.Progress.Stage != state.Progress.FailedStage {
				state.Progress.finishStage()
			}
			state.Progress.Stage = stage
			state.Progress.StageAt = time.Now().UTC()
			state.Progress.Work = WorkProgress{}
			state.Progress.event(stage, "stage_started", "")
		}
		state.Stage = stage
		return persist()
	}
	report := func(work WorkProgress) error {
		if work.Code != "" {
			state.Progress.event(state.Stage, work.Code, work.Detail)
		} else {
			state.Progress.Work = work
		}
		return persist()
	}
	fail := func(cause error) State {
		state.Error = cause.Error()
		state.OperationError = cause.Error()
		state.Progress.FailedStage = state.Stage
		state.Progress.event(state.Stage, "stage_failed", cause.Error())
		// Activation can already have admitted writes. Only the compatibility
		// contract permits returning to the predecessor at this boundary; its
		// older snapshot must never overwrite those newly accepted writes.
		if stopped && state.WritesOpened && !options.Stopped && state.Current.Binary != old.Binary && canRollback(old, state.Current) {
			if err := emit("rolling_back"); err != nil {
				state.Error += "; rollback journal: " + err.Error()
				state.Stage = "failed"
				state.Busy = false
				return state
			}
			_ = worker.exchange("stop", Version{}, false)
			if err := verifiedStart(store, worker, old, true); err != nil {
				state.Error += "; compatible previous start: " + err.Error()
				state.Stage = "failed"
				state.Busy = false
				return state
			}
			if err := health(worker, options, stop); err != nil {
				state.Error += "; compatible previous health: " + err.Error()
				state.Stage = "failed"
				state.Busy = false
				return state
			}
			state.Previous = previousBefore
			state.Current = old
			state.Candidate = nil
			state.RollbackAvailable = previousBefore != nil && canRollback(*previousBefore, old)
			if err := emit("rolling_back"); err != nil {
				state.Error += "; rollback selection: " + err.Error()
				state.Stage = "failed"
				state.Busy = false
				return state
			}
			if _, err := worker.control("commit"); err != nil {
				state.Error += "; previous activation: " + err.Error()
				state.Stage = "failed"
			} else {
				state.Stage = "rolled_back"
			}
			state.Busy = false
			return state
		}
		if stopped && !state.WritesOpened {
			if err := emit("rolling_back"); err != nil {
				state.Error += "; journal: " + err.Error()
				state.Stage = "failed"
				state.Busy = false
				return state
			}
			_ = worker.exchange("stop", Version{}, false)
			if changed {
				if err := restore(options.StoragePath, store, state.Backup); err != nil {
					state.Error += "; backup restore: " + err.Error()
					state.Stage = "failed"
					state.Busy = false
					return state
				}
			}
			state.Current = old
			state.Candidate = nil
			if !options.Stopped {
				if err := verifiedStart(store, worker, old, true); err != nil {
					state.Error += "; previous start: " + err.Error()
					state.Stage = "failed"
					state.Busy = false
					return state
				}
				if err := health(worker, options, stop); err != nil {
					state.Error += "; previous health: " + err.Error()
					state.Stage = "failed"
					state.Busy = false
					return state
				}
				state.WritesOpened = true
				if err := emit("rolling_back"); err != nil {
					state.Error += "; journal: " + err.Error()
					state.Stage = "failed"
					state.Busy = false
					return state
				}
				if _, err := worker.control("commit"); err != nil {
					state.Error += "; previous activation: " + err.Error()
					state.Stage = "failed"
					state.Busy = false
					return state
				}
			}
			state.Stage = "rolled_back"
		} else {
			state.Stage = "failed"
		}
		state.Busy = false
		return state
	}
	if len(state.Arguments) > 0 {
		options.Arguments = append([]string(nil), state.Arguments...)
	}
	if !options.Stopped {
		contact, err := waitForAddress(worker, stop)
		if err != nil {
			return fail(err)
		}
		arguments, err := argumentsForAddress(options.Arguments, contact.Address)
		if err != nil {
			return fail(err)
		}
		if err := worker.configure(arguments); err != nil {
			return fail(err)
		}
		options.Arguments = arguments
		state.Arguments = append([]string(nil), arguments...)
	}
	var candidate Version
	if action == "upgrade" {
		if state.Available == nil {
			return fail(errors.New("no checked release"))
		}
		manifest := *state.Available
		verified, digest, err := catalog.Check(stop)
		if err != nil {
			return fail(err)
		}
		if digest != state.ManifestHash || verified.Version != manifest.Version {
			return fail(errors.New("the official release changed; check and confirm it again"))
		}
		state.Progress.event("preparing", "manifest_verified", verified.Version)
		manifest = verified
		installed, err := strconv.ParseUint(old.Version, 10, 64)
		if err != nil {
			return fail(errors.New("installed version has no official numeric build identity"))
		}
		if manifest.Sequence <= installed {
			state.Busy = false
			state.Stage = "current"
			return state
		}
		if manifest.Compatibility.MigrationMinimum() > old.Compatibility.Generation || manifest.Compatibility.ReadsThrough < old.Compatibility.Generation {
			return fail(errors.New("release cannot migrate the installed data generation"))
		}
		if err := emit("downloading"); err != nil {
			return fail(err)
		}
		binary := filepath.Join("versions", state.Operation, BinaryName(runtime.GOOS, runtime.GOARCH))
		if err := store.MkdirAll(filepath.Dir(binary), 0o700); err != nil {
			return fail(err)
		}
		if _, err := runMeasured(stop, func(updates chan<- WorkProgress, session <-chan struct{}) (struct{}, error) {
			return struct{}{}, catalog.downloadProgress(manifest, filepath.Join(options.StoragePath, Directory, binary), session, updates)
		}, report); err != nil {
			return fail(err)
		}
		state.Progress.event("downloading", "download_verified", "")
		if err := syncAncestors(store, filepath.Dir(binary)); err != nil {
			return fail(err)
		}
		if err := emit("verifying"); err != nil {
			return fail(err)
		}
		artifact, err := manifest.Artifact(runtime.GOOS, runtime.GOARCH)
		if err != nil {
			return fail(err)
		}
		candidate = Version{Version: manifest.Version, Binary: binary, SHA256: artifact.SHA256, Compatibility: manifest.Compatibility}
		arguments := append(append([]string{}, options.Arguments...), "-update-preflight", "-update-target-version", manifest.Version)
		if err := runCandidate(filepath.Join(options.StoragePath, Directory, binary), arguments, options.WorkingDirectory, options.Output, 2*time.Minute, stop); err != nil {
			return fail(fmt.Errorf("candidate preflight: %w", err))
		}
	} else {
		candidate = *state.Previous
		if !canRollback(candidate, old) {
			return fail(errors.New("previous release cannot safely read current data"))
		}
	}
	state.Candidate = &candidate
	if err := emit("inspecting"); err != nil {
		return fail(err)
	}
	totals, err := runMeasured(stop, func(updates chan<- WorkProgress, session <-chan struct{}) (WorkProgress, error) {
		return inspectBackupSpace(options.StoragePath, updates, session)
	}, report)
	if err != nil {
		return fail(err)
	}
	if err := emit("backing_up"); err != nil {
		return fail(err)
	}
	if err := worker.exchange("stop", Version{}, false); err != nil {
		return fail(err)
	}
	stopped = true
	backup, err := runMeasured(stop, func(updates chan<- WorkProgress, session <-chan struct{}) (string, error) {
		return snapshotProgress(options.StoragePath, store, state.Operation, session, updates, totals)
	}, report)
	if err != nil {
		return fail(err)
	}
	state.Backup = backup
	if err := emit("installing"); err != nil {
		return fail(err)
	}
	changed = true
	arguments := append(append([]string{}, options.Arguments...), "-update-migrate", "-update-target-version", candidate.Version)
	if err := runCandidate(filepath.Join(options.StoragePath, Directory, candidate.Binary), arguments, options.WorkingDirectory, options.Output, 30*time.Minute, stop); err != nil {
		return fail(fmt.Errorf("candidate migrations: %w", err))
	}
	if err := emit("restarting"); err != nil {
		return fail(err)
	}
	if err := verifiedStart(store, worker, candidate, true); err != nil {
		return fail(err)
	}
	if err := emit("checking"); err != nil {
		return fail(err)
	}
	if err := health(worker, options, stop); err != nil {
		return fail(err)
	}
	state.Previous = &old
	state.Current = candidate
	state.RollbackAvailable = canRollback(old, candidate)
	state.RollbackReason = ""
	if !state.RollbackAvailable {
		state.RollbackReason = "Persistent data is incompatible with the previous version after writes resume"
	}
	state.WritesOpened = true
	if err := emit("activating"); err != nil {
		state.WritesOpened = false
		return fail(err)
	}
	if options.Stopped {
		if err := worker.exchange("stop", Version{}, false); err != nil {
			return fail(err)
		}
	} else {
		if _, err := worker.control("commit"); err != nil {
			return fail(err)
		}
	}
	state.Candidate = nil
	state.Busy = false
	state.Stage = "completed"
	if action == "rollback" {
		state.Stage = "rolled_back"
	}
	return state
}

func waitForAddress(worker process, stop <-chan struct{}) (ChildResult, error) {
	deadline := time.NewTimer(2 * time.Minute)
	defer deadline.Stop()
	for {
		contact, err := worker.control("address")
		if err == nil {
			return contact, nil
		}
		if err := worker.exchange("alive", Version{}, false); err != nil {
			return ChildResult{}, err
		}
		select {
		case <-stop:
			return ChildResult{}, errors.New("server startup contact canceled")
		case <-deadline.C:
			return ChildResult{}, errors.New("server startup contact timed out")
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func argumentsForAddress(arguments []string, address string) ([]string, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 || host != "127.0.0.1" {
		return nil, errors.New("invalid server HTTP contact")
	}
	result := append([]string(nil), arguments...)
	for index, argument := range result {
		if argument == "-port" && index+1 < len(result) {
			if strings.Split(result[index+1], ",")[0] != port {
				result[index+1] = port
			}
			return result, nil
		}
	}
	return append(result, "-port", port), nil
}

func health(worker process, options Options, stop <-chan struct{}) error {
	window := options.HealthWindow
	if window <= 0 {
		window = 60 * time.Second
	}
	timeout := options.HealthTimeout
	if timeout <= 0 {
		timeout = 120 * time.Second
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var healthySince time.Time
	for {
		result, err := worker.control("probe")
		if err == nil {
			host, port, addressErr := net.SplitHostPort(result.Address)
			portNumber, portErr := strconv.Atoi(port)
			if addressErr != nil || portErr != nil || portNumber < 1 || portNumber > 65535 || host != "127.0.0.1" || len(result.Nonce) < 1 || len(result.Nonce) > 128 {
				return errors.New("invalid private HTTP health contact")
			}
			client := http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("health check redirected") }}
			request, requestErr := http.NewRequest(http.MethodGet, "http://"+result.Address+"/_sitebrush/update-health", nil)
			if requestErr != nil {
				return requestErr
			}
			request.Header.Set("X-SiteBrush-Update-Probe", result.Nonce)
			response, requestErr := client.Do(request)
			if requestErr == nil {
				response.Body.Close()
				if response.StatusCode != http.StatusNoContent {
					requestErr = errors.New("HTTP health check failed")
				}
			}
			err = requestErr
		}
		if err == nil {
			if healthySince.IsZero() {
				healthySince = time.Now()
			}
			if time.Since(healthySince) >= window {
				return nil
			}
		} else {
			healthySince = time.Time{}
		}
		select {
		case <-stop:
			return errors.New("health check canceled")
		case <-deadline.C:
			return errors.New("server did not pass startup, HTTP, database and migration health checks")
		case <-ticker.C:
		}
	}
}

func checkBackupSpace(storagePath string) error {
	_, err := inspectBackupSpace(storagePath, nil, nil)
	return err
}

func inspectBackupSpace(storagePath string, updates chan<- WorkProgress, stop <-chan struct{}) (WorkProgress, error) {
	work := WorkProgress{}
	last := time.Now()
	root, err := os.OpenRoot(storagePath)
	if err != nil {
		return work, err
	}
	defer root.Close()
	var required uint64 = 32 << 20
	err = fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if name == Directory {
			return fs.SkipDir
		}
		select {
		case <-stop:
			return errors.New("backup inspection canceled")
		default:
		}
		if name == "." {
			return nil
		}
		work.TotalFiles++
		if work.TotalFiles > 1000000 {
			return errors.New("backup entry limit exceeded")
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if _, err := backupLink(root, name); err != nil {
				return err
			}
		} else if !entry.IsDir() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("unsafe backup entry %s", name)
			}
			if info.Size() < 0 || uint64(info.Size()) > (^uint64(0)-required)/2 {
				return errors.New("backup size overflow")
			}
			required += uint64(info.Size()) * 2
			work.TotalBytes += info.Size()
		}
		if time.Since(last) >= time.Second {
			last = time.Now()
			return sendWork(updates, work, stop)
		}
		return nil
	})
	if err != nil {
		return work, err
	}
	available, err := availableSpace(storagePath)
	if err != nil {
		return work, err
	}
	if available < required {
		return work, fmt.Errorf("backup and recovery require %d free bytes, have %d", required, available)
	}
	return work, sendWork(updates, work, stop)
}
