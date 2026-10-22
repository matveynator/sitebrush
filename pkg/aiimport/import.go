// Package aiimport owns bounded, grant-scoped asynchronous import sessions.
package aiimport

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"github.com/matveynator/sitebrush/v2/pkg/crawler"
)

var ErrCanceled = errors.New("import session canceled")
var ErrBusy = errors.New("import capacity unavailable")

type Request struct {
	Domain    string
	Grant     string
	SourceURL string
	Path      string
	Mode      string
	Done      <-chan struct{}
}

type Status struct {
	ID       string                `json:"job_id"`
	State    string                `json:"state"`
	Progress crawler.ProgressEvent `json:"progress"`
	Result   crawler.ImportResult  `json:"result"`
	Error    string                `json:"error,omitempty"`
}

type Runner func(Request, string, <-chan struct{}, chan<- crawler.ProgressEvent) (crawler.ImportResult, error)

type command struct {
	request    Request
	action, id string
	reply      chan response
}

type response struct {
	status Status
	err    error
}
type event struct {
	id       string
	progress crawler.ProgressEvent
	result   crawler.ImportResult
	err      error
	finished bool
}
type entry struct {
	request  Request
	status   Status
	cancel   chan struct{}
	finished bool
	updated  time.Time
}

type Worker struct {
	requests chan command
	events   chan event
	stop     chan struct{}
	finished chan struct{}
}

func New(runner Runner) *Worker {
	worker := &Worker{requests: make(chan command), events: make(chan event, 32), stop: make(chan struct{}), finished: make(chan struct{})}
	go worker.run(runner)
	return worker
}

func (worker *Worker) Exchange(action, id string, request Request) (Status, error) {
	if worker == nil || request.Done == nil {
		return Status{}, errors.New("import worker unavailable")
	}
	reply := make(chan response, 1)
	select {
	case <-request.Done:
		return Status{}, ErrCanceled
	default:
	}
	select {
	case worker.requests <- command{request: request, action: action, id: id, reply: reply}:
	case <-request.Done:
		return Status{}, ErrCanceled
	case <-worker.finished:
		return Status{}, errors.New("import worker stopped")
	}
	select {
	case result := <-reply:
		return result.status, result.err
	case <-request.Done:
		return Status{}, ErrCanceled
	case <-worker.finished:
		return Status{}, errors.New("import worker stopped")
	}
}

func (worker *Worker) Close() {
	if worker == nil {
		return
	}
	select {
	case worker.stop <- struct{}{}:
	case <-worker.finished:
	}
	<-worker.finished
}

func (worker *Worker) run(runner Runner) {
	defer close(worker.finished)
	jobs := make(map[string]entry)
	cleanup := time.NewTicker(time.Minute)
	defer cleanup.Stop()
	running := 0
	for {
		select {
		case <-worker.stop:
			for _, job := range jobs {
				if !job.finished && job.cancel != nil {
					close(job.cancel)
				}
			}
			// Import routines finish their boundary I/O before shutdown completes.
			for running > 0 {
				if update := <-worker.events; update.finished {
					running--
				}
			}
			return
		case <-cleanup.C:
			for id, job := range jobs {
				if job.finished && time.Since(job.updated) > 15*time.Minute {
					delete(jobs, id)
				}
			}
		case update := <-worker.events:
			job, exists := jobs[update.id]
			if !exists {
				continue
			}
			if update.finished {
				running--
				job.finished = true
				job.status.Result = update.result
				if job.status.State != "canceling" {
					job.status.State = "done"
				} else {
					job.status.State = "canceled"
				}
				if errors.Is(update.err, ErrCanceled) {
					job.status.State = "canceled"
				}
				if update.err != nil && job.status.State != "canceled" {
					job.status.State = "error"
					job.status.Error = update.err.Error()
				}
			} else {
				job.status.Progress = update.progress
			}
			job.updated = time.Now()
			jobs[update.id] = job
		case request := <-worker.requests:
			if request.action == "start" {
				if running >= 4 || len(jobs) >= 128 || runner == nil || request.request.Domain == "" || request.request.Grant == "" {
					request.reply <- response{err: ErrBusy}
					continue
				}
				var randomID [24]byte
				if _, err := rand.Read(randomID[:]); err != nil {
					request.reply <- response{err: err}
					continue
				}
				id := hex.EncodeToString(randomID[:])
				job := entry{request: request.request, status: Status{ID: id, State: "running"}, cancel: make(chan struct{}), updated: time.Now()}
				jobs[id] = job
				running++
				go worker.execute(runner, id, job.request, job.cancel)
				request.reply <- response{status: job.status}
				continue
			}
			job, exists := jobs[request.id]
			if !exists || job.request.Domain != request.request.Domain || job.request.Grant != request.request.Grant {
				request.reply <- response{err: errors.New("import not found")}
				continue
			}
			if request.action == "cancel" && !job.finished && job.cancel != nil {
				close(job.cancel)
				job.cancel = nil
				job.status.State = "canceling"
				jobs[request.id] = job
			}
			request.reply <- response{status: job.status}
		}
	}
}

func (worker *Worker) execute(runner Runner, id string, request Request, cancel <-chan struct{}) {
	select {
	case <-request.Done:
		worker.events <- event{id: id, err: ErrCanceled, finished: true}
		return
	default:
	}
	stop := make(chan struct{})
	progress := make(chan crawler.ProgressEvent, 8)
	result := make(chan response, 1)
	go func() {
		completed, err := runner(request, id, stop, progress)
		result <- response{status: Status{Result: completed}, err: err}
	}()
	stopped := false
	for {
		select {
		case <-request.Done:
			if !stopped {
				close(stop)
				stopped = true
			}
			request.Done = nil
		case <-cancel:
			if !stopped {
				close(stop)
				stopped = true
			}
			cancel = nil
		case update := <-progress:
			worker.events <- event{id: id, progress: update}
		case completed := <-result:
			if !stopped {
				close(stop)
			}
			if stopped {
				completed.err = ErrCanceled
			}
			worker.events <- event{id: id, result: completed.status.Result, err: completed.err, finished: true}
			return
		}
	}
}
