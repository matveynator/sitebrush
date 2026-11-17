package goprofile

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDiagnosticBoundary(t *testing.T) {
	handler := diagnosticHandler("public", "fixture")
	for _, scenario := range []struct {
		method, target, origin string
		status                 int
	}{
		{"GET", "http://127.0.0.1:6060/debug/pprof/", "", 200},
		{"GET", "http://127.0.0.1:6060/debug/pprof/goroutine?debug=2", "", 200},
		{"GET", "http://127.0.0.1:6060/debug/pprof/cmdline", "", 404},
		{"GET", "http://127.0.0.1:6060/debug/pprof/profile?seconds=121", "", 400},
		{"GET", "http://127.0.0.1:6060/debug/pprof/heap?seconds=-1", "", 400},
		{"GET", "http://127.0.0.1:6060/debug/pprof/profile?seconds=invalid", "", 400},
		{"POST", "http://127.0.0.1:6060/debug/pprof/profile", "", 405},
		{"GET", "http://foreign.example:6060/debug/pprof/", "", 403},
		{"GET", "http://127.0.0.1:6060/debug/pprof/", "https://foreign.example", 403},
	} {
		request := httptest.NewRequest(scenario.method, scenario.target, nil)
		request.Header.Set("Origin", scenario.origin)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != scenario.status {
			t.Fatalf("%+v: %d", scenario, response.Code)
		}
	}
}

func TestSessionLoopbackProfilesAndShutdown(t *testing.T) {
	stop := make(chan struct{})
	session, err := Start("public", "fixture", "console", stop)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	host, _, err := net.SplitHostPort(session.Address())
	if err != nil || host != "127.0.0.1" {
		t.Fatal("non-loopback profiler", session.Address(), err)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	for _, path := range []string{"/", "/profile?seconds=1", "/heap", "/allocs"} {
		response, requestErr := client.Get("http://" + session.Address() + "/debug/pprof" + path)
		if requestErr != nil {
			t.Fatal(requestErr)
		}
		body, readErr := io.ReadAll(response.Body)
		response.Body.Close()
		if readErr != nil || response.StatusCode != 200 {
			t.Fatalf("%s: %d %v", path, response.StatusCode, readErr)
		}
		if path == "/" {
			if !strings.Contains(string(body), "role=public") || !strings.Contains(string(body), "curl client's current directory") {
				t.Fatal("missing profiler instructions")
			}
		} else if len(body) < 2 || body[0] != 0x1f || body[1] != 0x8b {
			t.Fatalf("%s did not return a compressed Go profile", path)
		}
	}
	close(stop)
	select {
	case <-session.done:
	case <-time.After(5 * time.Second):
		t.Fatal("profiler did not stop with its owner")
	}
	if response, requestErr := client.Get("http://" + session.Address() + "/debug/pprof/"); requestErr == nil {
		response.Body.Close()
		t.Fatal("profiler listener survived shutdown")
	}
}
