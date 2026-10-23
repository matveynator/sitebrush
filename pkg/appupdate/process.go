package appupdate

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type processTask struct {
	action    string
	version   Version
	probation bool
	reply     chan error
	child     chan<- ChildTask
	probe     chan ChildResult
	arguments []string
}

// Process owns the child and its exit channel. Other subsystems never inspect
// exec.Cmd or os.Process while this participant can change them.
type process struct {
	tasks    chan processTask
	done     <-chan struct{}
	failures <-chan error
}

type processOptions struct {
	environment []string
	directory   string
	storagePath string
	arguments   []string
	output      io.Writer
	stop        <-chan struct{}
}

func startProcess(options processOptions) process {
	tasks := make(chan processTask, 8)
	finished := make(chan struct{})
	failures := make(chan error, 1)
	go func() {
		defer close(finished)
		var command *exec.Cmd
		var exited chan error
		var child chan<- ChildTask
		var lifeline io.WriteCloser
		stopChild := func() error {
			if command == nil {
				return nil
			}
			if lifeline != nil {
				_ = lifeline.Close()
				lifeline = nil
			}
			if child != nil {
				reply := make(chan ChildResult, 1)
				if err := sendChannel(child, ChildTask{Action: "stop", Reply: reply}, nil, 2*time.Second); err != nil {
					_ = signalChild(command.Process)
				}
			} else {
				_ = signalChild(command.Process)
			}
			select {
			case <-exited:
			case <-time.After(45 * time.Second):
				_ = command.Process.Kill()
				<-exited
			}
			command = nil
			exited = nil
			child = nil
			return nil
		}
		for {
			select {
			case <-options.stop:
				_ = stopChild()
				return
			case exitErr := <-exited:
				if lifeline != nil {
					_ = lifeline.Close()
					lifeline = nil
				}
				if exitErr == nil {
					exitErr = errors.New("server process exited unexpectedly")
				}
				select {
				case failures <- exitErr:
				default:
				}
				command = nil
				exited = nil
				child = nil
			case task := <-tasks:
				var err error
				switch task.action {
				case "configure":
					options.arguments = append([]string(nil), task.arguments...)
				case "attach":
					if command == nil {
						err = errors.New("no child process to attach")
					} else {
						child = task.child
					}
				case "probe", "commit", "address", "pause", "resume", "public_snapshot":
					result := ChildResult{Error: "server is starting"}
					if command != nil && child != nil {
						replyTimeout := 5 * time.Second
						if task.action == "commit" || task.action == "pause" || task.action == "public_snapshot" {
							replyTimeout = 35 * time.Second
						}
						reply := make(chan ChildResult, 1)
						if sendErr := sendChannel(child, ChildTask{Action: task.action, Reply: reply}, options.stop, time.Second); sendErr == nil {
							select {
							case received, open := <-reply:
								if open {
									result = received
								} else {
									result.Error = "server reply contact terminated"
								}
							case <-exited:
								if lifeline != nil {
									_ = lifeline.Close()
									lifeline = nil
								}
								select {
								case failures <- errors.New("server exited during lifecycle request"):
								default:
								}
								command = nil
								exited = nil
								child = nil
								result.Error = "server exited"
							case <-time.After(replyTimeout):
								result.Error = "server health timed out"
							}
						} else {
							result.Error = "server control unavailable"
						}
					}
					if task.probe != nil {
						task.probe <- result
					}
					if result.Error != "" {
						err = errors.New(result.Error)
					}
				case "stop":
					err = stopChild()
				case "alive":
					if command == nil {
						err = errors.New("server process is not running")
					}
				case "start":
					if command != nil {
						err = errors.New("server is already running")
						break
					}
					arguments := append([]string(nil), options.arguments...)
					arguments = append(arguments, "-server-child", "-update-target-version", task.version.Version)
					if task.probation {
						arguments = append(arguments, "-update-probation")
					}
					command = exec.Command(filepath.Join(options.storagePath, Directory, task.version.Binary), arguments...)
					command.Dir = options.directory
					command.Env = append(childEnvironment(os.Environ()), options.environment...)
					command.Stdout = options.output
					command.Stderr = options.output
					lifeline, err = command.StdinPipe()
					if err != nil {
						command = nil
						break
					}
					err = command.Start()
					if err != nil {
						_ = lifeline.Close()
						lifeline = nil
						command = nil
						break
					}
					exited = make(chan error, 1)
					go func(running *exec.Cmd, completion chan<- error) { completion <- running.Wait() }(command, exited)
				default:
					err = errors.New("unknown process action")
				}
				task.reply <- err
			}
		}
	}()
	return process{tasks: tasks, done: finished, failures: failures}
}

func (worker process) configure(arguments []string) error {
	reply := make(chan error, 1)
	select {
	case worker.tasks <- processTask{action: "configure", arguments: append([]string(nil), arguments...), reply: reply}:
	case <-worker.done:
		return errors.New("process controller stopped")
	}
	select {
	case err := <-reply:
		return err
	case <-worker.done:
		return errors.New("process controller stopped")
	}
}

func childEnvironment(environment []string) []string {
	result := make([]string, 0, len(environment))
	for _, entry := range environment {
		if !strings.HasPrefix(entry, "SITEBRUSH_UPDATE_") {
			result = append(result, entry)
		}
	}
	return result
}

func (worker process) exchange(action string, version Version, probation bool) error {
	reply := make(chan error, 1)
	select {
	case worker.tasks <- processTask{action: action, version: version, probation: probation, reply: reply}:
	case <-worker.done:
		return errors.New("process controller stopped")
	}
	select {
	case err := <-reply:
		return err
	case <-worker.done:
		return errors.New("process controller stopped")
	}
}

func (worker process) control(action string) (ChildResult, error) {
	reply := make(chan error, 1)
	probe := make(chan ChildResult, 1)
	select {
	case worker.tasks <- processTask{action: action, reply: reply, probe: probe}:
	case <-worker.done:
		return ChildResult{}, errors.New("process controller stopped")
	}
	select {
	case err := <-reply:
		select {
		case result := <-probe:
			return result, err
		default:
			return ChildResult{}, err
		}
	case <-worker.done:
		return ChildResult{}, errors.New("process controller stopped")
	}
}

func runCandidate(binary string, arguments []string, directory string, output io.Writer, timeout time.Duration, stop <-chan struct{}) error {
	command := exec.Command(binary, arguments...)
	command.Dir = directory
	command.Env = childEnvironment(os.Environ())
	command.Stdout = output
	command.Stderr = output
	lifeline, err := command.StdinPipe()
	if err != nil {
		return err
	}
	defer lifeline.Close()
	if err := command.Start(); err != nil {
		return err
	}
	exited := make(chan error, 1)
	go func() { exited <- command.Wait() }()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-exited:
		return err
	case <-timer.C:
		_ = command.Process.Kill()
		<-exited
		return fmt.Errorf("candidate timed out after %s", timeout)
	case <-stop:
		_ = command.Process.Kill()
		<-exited
		return errors.New("candidate canceled")
	}
}

// StartMaintenance keeps an accepted offline operation alive if its CLI observer
// disconnects. The temporary supervisor exits after its idle grace period.
func StartMaintenance(executable, storagePath string, arguments []string, directory string) error {
	store, err := openStore(storagePath)
	if err != nil {
		return err
	}
	defer store.Close()
	output, err := store.OpenFile("maintenance.log", os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer output.Close()
	command := exec.Command(executable, append(arguments, "-update-supervisor")...)
	command.Dir = directory
	command.Env = childEnvironment(os.Environ())
	command.Stdout = output
	command.Stderr = output
	detachCommand(command)
	if err := command.Start(); err != nil {
		return err
	}
	return command.Process.Release()
}
