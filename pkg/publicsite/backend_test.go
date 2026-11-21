package publicsite

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestBackendAdmissionHasNoWaitQueueAndReleasesCanceledSessions(t *testing.T) {
	admission := NewBackendAdmission()
	started := make(chan struct{}, BackendLimit+1)
	finished := make(chan struct{}, BackendLimit+1)
	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	handler := admission.Wrap(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		started <- struct{}{}
		select {
		case <-release:
		case <-request.Context().Done():
		}
	}))
	boundary, cancel := context.WithCancel(context.Background())
	defer cancel()
	for index := 0; index < BackendLimit; index++ {
		request := httptest.NewRequest("GET", "http://127.0.0.1/?login", nil)
		if index == 0 {
			request = request.WithContext(boundary)
		}
		go func() { handler.ServeHTTP(httptest.NewRecorder(), request); finished <- struct{}{} }()
		<-started
	}
	response := httptest.NewRecorder()
	// Wrappers share one contact budget across different dynamic endpoints.
	admission.Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("overflow reached backend") })).ServeHTTP(response, httptest.NewRequest("GET", "http://127.0.0.1/?register", nil))
	if response.Code != 503 || response.Header().Get("Retry-After") != "1" {
		t.Fatal("missing explicit overflow response", response.Code)
	}
	cancel()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("disconnect retained admission")
	}
	go func() {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "http://127.0.0.1/?login", nil))
		finished <- struct{}{}
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("canceled slot was not reusable")
	}
	// Join handlers before closing the test; no goroutine retains a test capture.
	close(release)
	for index := 0; index < BackendLimit; index++ {
		<-finished
	}
}
