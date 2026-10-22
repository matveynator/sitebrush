package aiimport

import (
	"errors"
	"testing"
	"time"

	"github.com/matveynator/sitebrush/v2/pkg/crawler"
)

func waitStatus(t *testing.T, worker *Worker, id string, request Request, state string) Status {
	t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for {
		status, err := worker.Exchange("status", id, request)
		if err != nil {
			t.Fatal(err)
		}
		if status.State == state {
			return status
		}
		select {
		case <-deadline.C:
			t.Fatalf("wanted %s, received %+v", state, status)
		case <-time.After(time.Millisecond):
		}
	}
}

func TestImportLifetimeIsolationAndCapacity(t *testing.T) {
	started := make(chan struct{}, 4)
	worker := New(func(request Request, id string, stop <-chan struct{}, progress chan<- crawler.ProgressEvent) (crawler.ImportResult, error) {
		started <- struct{}{}
		select {
		case progress <- crawler.ProgressEvent{Stage: "download"}:
		case <-stop:
		}
		<-stop
		return crawler.ImportResult{}, ErrCanceled
	})
	defer worker.Close()
	session := make(chan struct{})
	request := Request{Domain: "one.example", Grant: "grant-one", Done: session}
	first, err := worker.Exchange("start", "", request)
	if err != nil {
		t.Fatal(err)
	}
	<-started
	for _, foreign := range []Request{{Domain: "two.example", Grant: request.Grant, Done: session}, {Domain: request.Domain, Grant: "grant-two", Done: session}} {
		if _, err := worker.Exchange("cancel", first.ID, foreign); err == nil {
			t.Fatal("foreign grant reached import")
		}
	}
	for index := 0; index < 3; index++ {
		if _, err := worker.Exchange("start", "", request); err != nil {
			t.Fatal(err)
		}
		<-started
	}
	if _, err := worker.Exchange("start", "", request); err == nil {
		t.Fatal("unbounded parallel imports")
	}
	if _, err := worker.Exchange("cancel", first.ID, request); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, worker, first.ID, request, "canceled")
	close(session)
	fresh := request
	fresh.Done = make(chan struct{})
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for {
		next, err := worker.Exchange("start", "", fresh)
		if err == nil {
			if _, err := worker.Exchange("cancel", next.ID, fresh); err != nil {
				t.Fatal(err)
			}
			break
		}
		select {
		case <-deadline.C:
			t.Fatal("revocation did not release capacity")
		case <-time.After(time.Millisecond):
		}
	}
	if _, err := worker.Exchange("start", "", request); !errors.Is(err, ErrCanceled) {
		t.Fatal("revoked session submitted import", err)
	}
}

func TestImportCompletionAndShutdownWaitForRunner(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	worker := New(func(request Request, id string, stop <-chan struct{}, progress chan<- crawler.ProgressEvent) (crawler.ImportResult, error) {
		entered <- struct{}{}
		select {
		case <-release:
			return crawler.ImportResult{RedirectPath: "/copied"}, nil
		case <-stop:
			return crawler.ImportResult{}, ErrCanceled
		}
	})
	request := Request{Domain: "one.example", Grant: "grant", Done: make(chan struct{})}
	status, err := worker.Exchange("start", "", request)
	if err != nil {
		t.Fatal(err)
	}
	<-entered
	close(release)
	completed := waitStatus(t, worker, status.ID, request, "done")
	if completed.Result.RedirectPath != "/copied" {
		t.Fatal(completed)
	}
	worker.Close()
	worker.Close()
	if _, err := worker.Exchange("status", status.ID, request); err == nil {
		t.Fatal("closed worker accepted request")
	}
}
