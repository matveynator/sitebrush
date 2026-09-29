// Package aieditor contains the channel boundary between an AI model and site content.
package aieditor

import (
	"errors"
	"path"
	"strings"
)

type Operation string

const (
	OperationListPages  Operation = "list_pages"
	OperationReadPage   Operation = "read_page"
	OperationCreatePage Operation = "create_page"
	OperationUpdatePage Operation = "update_page"
	OperationUploadFile Operation = "upload_file"
	OperationPublish    Operation = "publish"
	OperationRollback   Operation = "rollback"
)

var (
	ErrInvalidRequest       = errors.New("AI editor request is invalid")
	ErrUnsupportedOperation = errors.New("AI editor operation is not supported")
	ErrTaskCanceled         = errors.New("AI editor task was canceled")
)

type Request struct {
	Domain          string    `json:"domain,omitempty"`
	Operation       Operation `json:"operation"`
	Path            string    `json:"path,omitempty"`
	Title           string    `json:"title,omitempty"`
	HTML            string    `json:"html,omitempty"`
	FileName        string    `json:"file_name,omitempty"`
	FileContent     []byte    `json:"file_content,omitempty"`
	ExpectedVersion string    `json:"expected_version,omitempty"`
}

type Result struct {
	Operation Operation `json:"operation"`
	Path      string    `json:"path,omitempty"`
	Message   string    `json:"message,omitempty"`
	Payload   any       `json:"payload,omitempty"`
	Err       error     `json:"-"`
}

type Store interface {
	Execute(Request, <-chan struct{}) Result
}

// Task keeps cancellation explicit at the channel boundary. Done is owned by the
// consumer and closes when the result is no longer needed; Reply carries only
// completed results. Keeping both signals visible avoids propagating context
// through application code while still letting the worker stop before expensive work.
type Task struct {
	Request Request
	Done    <-chan struct{}
	Reply   chan<- Result

	accepted     chan struct{}
	closeRequest bool
}

// Executor owns its lifecycle in the same goroutine that owns the request queue.
// Close is therefore a protocol message rather than an external close on shared state.
type Executor struct {
	requests chan Task
	stop     chan struct{}
}

func NewExecutor(store Store, queueSize int) *Executor {
	if queueSize < 1 {
		queueSize = 16
	}
	executor := &Executor{requests: make(chan Task, queueSize), stop: make(chan struct{})}
	go executor.run(store)
	return executor
}

func (executor *Executor) run(store Store) {
	for {
		select {
		case <-executor.stop:
			return
		case task := <-executor.requests:
			if task.closeRequest {
				close(executor.stop)
				return
			}
			if task.accepted != nil {
				close(task.accepted)
			}
			if task.Reply == nil || task.Done == nil {
				continue
			}
			select {
			case <-task.Done:
				// The consumer is gone. Do not send a cancellation result to a
				// channel that may have no receiver; dropping the task is the
				// cancellation protocol and keeps the worker available.
				continue
			default:
			}
			requestErr := Validate(task.Request)
			result := Result{Operation: task.Request.Operation, Path: cleanRequestPath(task.Request.Path), Err: requestErr}
			if requestErr == nil {
				if store == nil {
					result.Err = errors.New("AI editor store is unavailable")
				} else {
					result = store.Execute(task.Request, task.Done)
				}
			}
			select {
			case <-task.Done:
			case task.Reply <- result:
			}
		}
	}
}

func (executor *Executor) Submit(task Task) error {
	if executor == nil || task.Reply == nil || task.Done == nil {
		return ErrInvalidRequest
	}

	// A buffered queue can contain a shutdown marker. Submit therefore waits for
	// the worker to acknowledge receipt before reporting success. If shutdown is
	// reached first, the caller learns that its task was never accepted.
	task.accepted = make(chan struct{})
	select {
	case executor.requests <- task:
	case <-executor.stop:
		return errors.New("AI editor executor is stopped")
	}
	select {
	case <-task.accepted:
		return nil
	case <-executor.stop:
		return errors.New("AI editor executor is stopped")
	}
}

func (executor *Executor) Close() {
	if executor == nil {
		return
	}

	// The worker is the only goroutine allowed to close stop. Every concurrent
	// closer waits on that same completion channel, so duplicate close requests
	// cannot strand a caller on a private acknowledgement channel.
	select {
	case executor.requests <- Task{closeRequest: true}:
	case <-executor.stop:
		return
	}
	<-executor.stop
}

func Validate(request Request) error {
	switch request.Operation {
	case OperationListPages, OperationPublish:
		if request.Path != "" && cleanRequestPath(request.Path) == "" {
			return ErrInvalidRequest
		}
	case OperationReadPage, OperationCreatePage, OperationUpdatePage, OperationRollback:
		if cleanRequestPath(request.Path) == "" {
			return ErrInvalidRequest
		}
	case OperationUploadFile:
		if cleanRequestFileName(request.FileName) == "" || len(request.FileContent) == 0 {
			return ErrInvalidRequest
		}
	default:
		return ErrUnsupportedOperation
	}
	if len(request.HTML) > 32<<20 || len(request.FileContent) > 128<<20 {
		return ErrInvalidRequest
	}
	return nil
}

func cleanRequestPath(requestPath string) string {
	trimmedPath := strings.TrimSpace(requestPath)
	if trimmedPath == "" {
		return ""
	}
	if strings.Contains(trimmedPath, "\\") || strings.Contains(trimmedPath, "\x00") {
		return ""
	}
	for _, pathPart := range strings.Split(trimmedPath, "/") {
		if pathPart == ".." {
			return ""
		}
	}
	if !strings.HasPrefix(trimmedPath, "/") {
		trimmedPath = "/" + trimmedPath
	}
	cleanedPath := path.Clean(trimmedPath)
	if cleanedPath == "." || cleanedPath == "" || strings.HasPrefix(cleanedPath, "/../") || cleanedPath == "/.." {
		return ""
	}
	return cleanedPath
}

func cleanRequestFileName(fileName string) string {
	trimmedName := strings.TrimSpace(fileName)
	if trimmedName == "" || strings.Contains(trimmedName, "\\") || strings.Contains(trimmedName, "\x00") {
		return ""
	}
	for _, pathPart := range strings.Split(trimmedName, "/") {
		if pathPart == ".." {
			return ""
		}
	}
	cleanedName := path.Clean("/" + trimmedName)
	if cleanedName == "/" || strings.HasPrefix(cleanedName, "/../") {
		return ""
	}
	return strings.TrimPrefix(cleanedName, "/")
}
