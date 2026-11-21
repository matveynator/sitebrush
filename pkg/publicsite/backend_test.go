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

func TestBackendAdmissionSeparatesVisitorSocketsFromControlRequests(t *testing.T) {
	started := make(chan struct{}, VisitorAnalyticsLimit+BackendLimit)
	finished := make(chan struct{}, VisitorAnalyticsLimit+BackendLimit)
	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	application := NewBackendAdmission()
	// Exercise both admission boundaries with the same long-lived sessions.
	handler := LimitBackend(application.Wrap(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("request") == "analytics" || request.URL.Query().Has("hold") {
			started <- struct{}{}
			select {
			case <-release:
			case <-request.Context().Done():
			}
		}
		writer.WriteHeader(http.StatusNoContent)
	})))
	boundary, cancel := context.WithCancel(context.Background())
	defer cancel()
	startHeldRequest := func(request *http.Request) {
		go func() { handler.ServeHTTP(httptest.NewRecorder(), request); finished <- struct{}{} }()
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("independent budget rejected an available contact")
		}
	}
	newVisitorRequest := func() *http.Request {
		request := httptest.NewRequest("GET", "http://example.org/?request=analytics", nil)
		request.Header.Set("Upgrade", "websocket")
		return request
	}
	for index := 0; index < VisitorAnalyticsLimit; index++ {
		request := newVisitorRequest()
		if index == 0 {
			request = request.WithContext(boundary)
		}
		startHeldRequest(request)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, newVisitorRequest())
	if response.Code != http.StatusServiceUnavailable || response.Header().Get("Retry-After") != "1" {
		t.Fatal("visitor budget is unbounded", response.Code)
	}
	for _, endpoint := range []string{"login", "edit", "register"} {
		response = httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("GET", "http://example.org/?"+endpoint, nil))
		if response.Code != http.StatusNoContent {
			t.Fatal("visitor sockets exhausted control admission", endpoint, response.Code)
		}
	}
	for index := 0; index < BackendLimit; index++ {
		startHeldRequest(httptest.NewRequest("GET", "http://example.org/?login&hold", nil))
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest("GET", "http://example.org/?login", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatal("control budget is unbounded", response.Code)
	}
	cancel()
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("visitor disconnect retained admission")
	}
	// Visitor admission still works while every control contact is held.
	startHeldRequest(newVisitorRequest())
	close(release)
	for index := 0; index < VisitorAnalyticsLimit+BackendLimit; index++ {
		select {
		case <-finished:
		case <-time.After(time.Second):
			t.Fatal("admitted session did not release its contacts")
		}
	}
}

func TestVisitorAnalyticsAdmissionRejectsMixedControlSelectors(t *testing.T) {
	for _, scenario := range []struct {
		method, endpoint, upgrade string
		visitor                   bool
	}{
		{"GET", "/?request=analytics", "websocket", true},
		{"GET", "/?request=analytics", "WebSocket", true},
		{"POST", "/?request=analytics", "", true},
		{"GET", "/?request=analytics", "", false},
		{"GET", "/?request=analytics&ignite", "websocket", false},
		{"POST", "/?request=analytics&mcp", "", false},
		{"GET", "/?request=analytics&login", "websocket", false},
		{"GET", "/?request=analytics&request=analytics", "websocket", false},
		{"GET", "/.well-known/oauth-authorization-server?request=analytics", "websocket", false},
	} {
		request := httptest.NewRequest(scenario.method, "http://example.org"+scenario.endpoint, nil)
		request.Header.Set("Upgrade", scenario.upgrade)
		if visitorAnalyticsRequest(request) != scenario.visitor {
			t.Fatal("incorrect admission class", scenario)
		}
	}
}
