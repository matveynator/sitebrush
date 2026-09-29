package aieditor

import (
	"strings"
	"testing"
	"time"
)

type testStore struct{ requests chan Request }

func (store *testStore) Execute(request Request, done <-chan struct{}) Result {
	select {
	case <-done:
		return Result{Operation: request.Operation, Path: request.Path, Err: ErrTaskCanceled}
	case store.requests <- request:
		return Result{Operation: request.Operation, Path: request.Path, Message: "accepted"}
	}
}

func TestValidateRejectsTraversalAndUnknownOperations(t *testing.T) {
	if err := Validate(Request{Operation: OperationReadPage, Path: "/../secret"}); err == nil {
		t.Fatal("page traversal accepted")
	}
	if err := Validate(Request{Operation: OperationUploadFile, FileName: "../secret", FileContent: []byte("x")}); err == nil {
		t.Fatal("file traversal accepted")
	}
	if err := Validate(Request{Operation: Operation("shell")}); err != ErrUnsupportedOperation {
		t.Fatalf("err=%v", err)
	}
}

func TestExecutorUsesChannelsAndCancelsBeforeStore(t *testing.T) {
	store := &testStore{requests: make(chan Request, 2)}
	executor := NewExecutor(store, 2)
	defer executor.Close()

	canceled := make(chan struct{})
	close(canceled)
	canceledReply := make(chan Result)
	if err := executor.Submit(Task{Request: Request{Operation: OperationReadPage, Path: "/canceled.html"}, Done: canceled, Reply: canceledReply}); err != nil {
		t.Fatal(err)
	}

	active := make(chan struct{})
	activeReply := make(chan Result, 1)
	if err := executor.Submit(Task{Request: Request{Operation: OperationReadPage, Path: "/index.html"}, Done: active, Reply: activeReply}); err != nil {
		t.Fatal(err)
	}

	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case result := <-activeReply:
		if result.Err != nil {
			t.Fatal(result.Err)
		}
	case <-timer.C:
		t.Fatal("canceled task blocked the executor")
	}

	select {
	case result := <-canceledReply:
		t.Fatalf("canceled task unexpectedly returned a result: %+v", result)
	default:
	}
	if request := <-store.requests; request.Path != "/index.html" {
		t.Fatalf("canceled task reached store: %+v", request)
	}
}

func TestExecutorPassesOnlyValidatedRequestToStore(t *testing.T) {
	store := &testStore{requests: make(chan Request, 1)}
	executor := NewExecutor(store, 1)
	defer executor.Close()
	done := make(chan struct{})
	reply := make(chan Result, 1)
	if err := executor.Submit(Task{Request: Request{Operation: OperationUpdatePage, Path: "/about", HTML: strings.Repeat("x", 12)}, Done: done, Reply: reply}); err != nil {
		t.Fatal(err)
	}
	if result := <-reply; result.Err != nil {
		t.Fatal(result.Err)
	}
	if request := <-store.requests; request.Path != "/about" {
		t.Fatalf("request=%+v", request)
	}
}

func TestValidateAcceptsSupportedOperationsAndEnforcesLimits(t *testing.T) {
	requests := []Request{
		{Operation: OperationListPages},
		{Operation: OperationReadPage, Path: "/index.html"},
		{Operation: OperationCreatePage, Path: "/new"},
		{Operation: OperationUpdatePage, Path: "/new"},
		{Operation: OperationUploadFile, FileName: "images/photo.jpg", FileContent: []byte("x")},
		{Operation: OperationPublish},
		{Operation: OperationRollback, Path: "/new"},
	}
	for _, request := range requests {
		if err := Validate(request); err != nil {
			t.Fatalf("request=%+v err=%v", request, err)
		}
	}
	if err := Validate(Request{Operation: OperationUpdatePage, Path: "/new", HTML: strings.Repeat("x", 32<<20+1)}); err == nil {
		t.Fatal("oversized HTML accepted")
	}
	if err := Validate(Request{Operation: OperationUploadFile, FileName: "photo.jpg", FileContent: make([]byte, 128<<20+1)}); err == nil {
		t.Fatal("oversized file accepted")
	}
	for _, fileName := range []string{"", "a\\b", "a\x00b", "../a"} {
		if err := Validate(Request{Operation: OperationUploadFile, FileName: fileName, FileContent: []byte("x")}); err == nil {
			t.Fatalf("invalid file name accepted: %q", fileName)
		}
	}
}

func TestExecutorConcurrentCloseAndSubmitDoNotHang(t *testing.T) {
	storeStarted := make(chan struct{}, 1)
	releaseStore := make(chan struct{})
	store := &blockingTestStore{started: storeStarted, release: releaseStore}
	executor := NewExecutor(store, 4)

	done := make(chan struct{})
	reply := make(chan Result, 1)
	if err := executor.Submit(Task{Request: Request{Operation: OperationReadPage, Path: "/first"}, Done: done, Reply: reply}); err != nil {
		t.Fatal(err)
	}
	<-storeStarted

	closed := make(chan struct{}, 2)
	go func() {
		executor.Close()
		closed <- struct{}{}
	}()
	go func() {
		executor.Close()
		closed <- struct{}{}
	}()

	lateDone := make(chan struct{})
	lateReply := make(chan Result, 1)
	lateResult := make(chan error, 1)
	go func() {
		lateResult <- executor.Submit(Task{Request: Request{Operation: OperationReadPage, Path: "/late"}, Done: lateDone, Reply: lateReply})
	}()

	close(releaseStore)
	for completed := 0; completed < 2; completed++ {
		select {
		case <-closed:
		case <-time.After(time.Second):
			t.Fatal("concurrent executor Close blocked")
		}
	}

	select {
	case err := <-lateResult:
		if err == nil {
			t.Fatal("task queued behind shutdown was reported as accepted")
		}
	case <-time.After(time.Second):
		t.Fatal("Submit racing shutdown blocked")
	}
}

func TestExecutorCloseIsIdempotent(t *testing.T) {
	executor := NewExecutor(nil, 1)
	executor.Close()
	executor.Close()

	done := make(chan struct{})
	reply := make(chan Result, 1)
	if err := executor.Submit(Task{Request: Request{Operation: OperationReadPage, Path: "/index"}, Done: done, Reply: reply}); err == nil {
		t.Fatal("closed executor accepted task")
	}
}

func TestExecutorRejectsMissingStoreAndClosedExecutor(t *testing.T) {
	executor := NewExecutor(nil, 1)
	done := make(chan struct{})
	reply := make(chan Result)
	if err := executor.Submit(Task{Request: Request{Operation: OperationReadPage, Path: "/index"}, Done: done, Reply: reply}); err != nil {
		t.Fatal(err)
	}
	if result := <-reply; result.Err == nil {
		t.Fatal("missing store did not fail")
	}
	executor.Close()
	if err := executor.Submit(Task{Request: Request{Operation: OperationReadPage, Path: "/index"}, Done: done, Reply: reply}); err == nil {
		t.Fatal("closed executor accepted task")
	}
}

func TestExecutorValidatesTaskChannelsAndNormalizesPaths(t *testing.T) {
	executor := NewExecutor(nil, 0)
	if err := executor.Submit(Task{Request: Request{Operation: OperationReadPage, Path: "/x"}}); err == nil {
		t.Fatal("task without reply or done accepted")
	}
	done := make(chan struct{})
	if err := executor.Submit(Task{Request: Request{Operation: OperationReadPage, Path: "/x"}, Done: done}); err == nil {
		t.Fatal("task without reply accepted")
	}
	if err := executor.Submit(Task{Request: Request{Operation: OperationReadPage, Path: "/x"}, Reply: make(chan Result, 1)}); err == nil {
		t.Fatal("task without done accepted")
	}
	executor.Close()
	for _, request := range []Request{{Operation: OperationReadPage, Path: "about"}, {Operation: OperationReadPage, Path: "/"}, {Operation: OperationUploadFile, FileName: "photo.jpg", FileContent: []byte("x")}} {
		if err := Validate(request); err != nil {
			t.Fatalf("valid request rejected: %+v: %v", request, err)
		}
	}
}


func TestExecutorHandlesMalformedInternalTaskAndLateCancellation(t *testing.T) {
	storeStarted := make(chan struct{}, 1)
	releaseStore := make(chan struct{})
	store := &blockingTestStore{started: storeStarted, release: releaseStore}
	executor := NewExecutor(store, 1)
	defer executor.Close()

	// The worker must tolerate a malformed task even if an internal caller bypasses Submit.
	executor.requests <- Task{Request: Request{Operation: OperationReadPage, Path: "/ignored"}}

	done := make(chan struct{})
	reply := make(chan Result, 1)
	if err := executor.Submit(Task{Request: Request{Operation: OperationReadPage, Path: "/index"}, Done: done, Reply: reply}); err != nil {
		t.Fatal(err)
	}
	<-storeStarted
	close(done)
	close(releaseStore)

	select {
	case <-reply:
		t.Fatal("late-canceled task returned a result")
	default:
	}
}

type blockingTestStore struct {
	started chan struct{}
	release chan struct{}
}

func (store *blockingTestStore) Execute(request Request, done <-chan struct{}) Result {
	store.started <- struct{}{}
	select {
	case <-done:
		return Result{Operation: request.Operation, Path: request.Path, Err: ErrTaskCanceled}
	case <-store.release:
		return Result{Operation: request.Operation, Path: request.Path}
	}
}

func TestValidateRejectsMalformedPathsAndEmptyUploads(t *testing.T) {
	for _, pagePath := range []string{"", "a\\b", "a\x00b", "a/../b"} {
		if err := Validate(Request{Operation: OperationReadPage, Path: pagePath}); err == nil {
			t.Fatalf("malformed page path accepted: %q", pagePath)
		}
	}
	if err := Validate(Request{Operation: OperationListPages, Path: "a/../b"}); err == nil {
		t.Fatal("invalid optional list path accepted")
	}
	if err := Validate(Request{Operation: OperationUploadFile, FileName: "photo.jpg"}); err == nil {
		t.Fatal("empty upload content accepted")
	}
}

func TestNilExecutorIsSafe(t *testing.T) {
	var executor *Executor
	if err := executor.Submit(Task{}); err != ErrInvalidRequest {
		t.Fatalf("nil executor error=%v", err)
	}
	executor.Close()
}
