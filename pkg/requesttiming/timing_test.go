package requesttiming

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMiddlewareSeparatesConnectionAndFirstWrite(t *testing.T) {
	stop := make(chan struct{})
	defer close(stop)
	collector := New(stop)
	connection := collector.ConnContext(context.Background(), nil)
	handler := collector.Middleware(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		started := Start(request)
		writer.WriteHeader(http.StatusOK)
		time.Sleep(time.Millisecond)
		Finish(request, Policy, started)
		_, _ = io.Copy(writer, strings.NewReader("body"))
	}))
	for count := 0; count < 2; count++ {
		request := httptest.NewRequest(http.MethodGet, "http://site.test/", nil).WithContext(connection)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Body.String() != "body" {
			t.Fatal("measurement changed response")
		}
	}
	report, err := collector.Snapshot(stop)
	if err != nil {
		t.Fatal(err)
	}
	if report.Stages[AcceptedToRequest].Count != 1 || report.Stages[FirstWrite].Count != 2 {
		t.Fatal("keep-alive was counted as a new connection", report.Stages)
	}
	if report.Stages[Policy].TotalNS > report.Stages[FirstWrite].TotalNS {
		t.Fatal("post-header work was included in critical-path timing")
	}
	if report.Stages[Policy].PercentileUpperBound(95) < time.Duration(report.Stages[Policy].MaxNS) {
		t.Fatal("histogram upper bound is below its sample")
	}
}

func TestFullOrStoppedCollectorNeverWaitsForHTTP(t *testing.T) {
	stop := make(chan struct{})
	collector := &Collector{events: make(chan Event, 1), losses: make(chan struct{}, 1), stop: stop}
	if !collector.Observe(Event{}) || collector.Observe(Event{}) {
		t.Fatal("queue admission mismatch")
	}
	select {
	case <-collector.losses:
	default:
		t.Fatal("missing incomplete marker")
	}
	close(stop)
	if collector.Observe(Event{}) {
		t.Fatal("stopped collector accepted work")
	}
}

func TestHTTPBodyTrafficCounters(t *testing.T) {
	stop := make(chan struct{})
	defer close(stop)
	collector := New(stop)
	handler := collector.Middleware(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if _, err := io.Copy(io.Discard, request.Body); err != nil {
			t.Error(err)
		}
		writer.WriteHeader(http.StatusCreated)
		if _, err := io.WriteString(writer, "reply"); err != nil {
			t.Error(err)
		}
	}))
	request := httptest.NewRequest(http.MethodPost, "http://example.test/", strings.NewReader("request"))
	handler.ServeHTTP(httptest.NewRecorder(), request)
	report, err := collector.Snapshot(stop)
	if err != nil {
		t.Fatal(err)
	}
	if report.HTTP.Requests != 1 || report.HTTP.Received != 7 || report.HTTP.Sent != 5 || report.HTTP.Errors != 0 {
		t.Fatal(report.HTTP)
	}
}

func TestIntervalsSeparateRoutesStatusesAndExactMaximum(t *testing.T) {
	stop := make(chan struct{})
	defer close(stop)
	collector := New(stop)
	collector.RecordResult(ControlDBQueue, 9*time.Second, 499)
	first, err := collector.IntervalSnapshot(stop)
	if err != nil {
		t.Fatal(err)
	}
	collector.RecordResult(ControlDBQueue, time.Millisecond, 200)
	second, err := collector.IntervalSnapshot(stop)
	if err != nil {
		t.Fatal(err)
	}
	cumulative, err := collector.Snapshot(stop)
	if err != nil {
		t.Fatal(err)
	}
	if first.Stages[ControlDBQueue].MaxNS != uint64(9*time.Second) || first.Stages[ControlDBQueue].Statuses[499] != 1 {
		t.Fatal("first interval lost its cancellation")
	}
	if second.Stages[ControlDBQueue].Count != 1 || second.Stages[ControlDBQueue].MaxNS != uint64(time.Millisecond) || second.Stages[ControlDBQueue].Statuses[499] != 0 {
		t.Fatal("interval includes earlier traffic")
	}
	if cumulative.Stages[ControlDBQueue].Count != 2 || cumulative.Stages[ControlDBQueue].Statuses[499] != 1 {
		t.Fatal("reset changed cumulative observations")
	}
	first.Stages[ControlDBQueue].Statuses[499] = 100
	again, err := collector.Snapshot(stop)
	if err != nil || again.Stages[ControlDBQueue].Statuses[499] != 1 {
		t.Fatal("subscriber changed collector-owned observations", err)
	}
	if !second.Started.Equal(first.Finished) || second.Finished.Before(second.Started) {
		t.Fatal("invalid interval boundaries")
	}
}

func TestRouteLatencyIncludesCompletedResponseAndStatus(t *testing.T) {
	stop := make(chan struct{})
	defer close(stop)
	collector := New(stop)
	handler := collector.Middleware(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		Route(request, LocalIPEntry)
		writer.WriteHeader(http.StatusSeeOther)
	}))
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("HEAD", "http://127.0.0.1/", nil))
	report, err := collector.Snapshot(stop)
	if err != nil {
		t.Fatal(err)
	}
	counter := report.Stages[LocalIPEntry]
	if counter.Count != 1 || counter.Statuses[303] != 1 || counter.TotalNS == 0 || counter.PercentileUpperBound(99) < time.Duration(counter.MaxNS) {
		t.Fatal("missing route observation", counter)
	}
	if report.Stages[ProxyApplication].Count != 0 {
		t.Fatal("entry was also counted as a backend request")
	}
}

// The reverse proxy writes its 101 to the raw connection after hijacking.
type hijackRecorder struct {
	*httptest.ResponseRecorder
	connection net.Conn
}

func (recorder hijackRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return recorder.connection, bufio.NewReadWriter(bufio.NewReader(recorder.connection), bufio.NewWriter(recorder.connection)), nil
}

func TestUpgradedSessionDoesNotInflateProxyRequestLatency(t *testing.T) {
	stop := make(chan struct{})
	defer close(stop)
	collector := New(stop)
	connection, peer := net.Pipe()
	defer peer.Close()
	upgradedHandler := collector.Middleware(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		Route(request, ProxyApplication)
		hijacked, _, err := http.NewResponseController(writer).Hijack()
		if err != nil {
			t.Fatal(err)
		}
		_ = hijacked.Close()
	}))
	request := httptest.NewRequest(http.MethodGet, "http://site.test/?server_profiler_ws", nil)
	request.Header.Set("Upgrade", "websocket")
	upgradedHandler.ServeHTTP(hijackRecorder{httptest.NewRecorder(), connection}, request)

	rejectedHandler := collector.Middleware(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		Route(request, ProxyApplication)
		writer.WriteHeader(http.StatusForbidden)
	}))
	rejectedHandler.ServeHTTP(httptest.NewRecorder(), request)
	report, err := collector.Snapshot(stop)
	if err != nil {
		t.Fatal(err)
	}
	proxy := report.Stages[ProxyApplication]
	upgrade := report.Stages[UpgradeSessionLifetime]
	if proxy.Count != 1 || proxy.Statuses[http.StatusForbidden] != 1 || upgrade.Count != 1 || upgrade.Statuses[http.StatusSwitchingProtocols] != 1 {
		t.Fatalf("wrong proxy or upgrade counts: proxy=%d, upgrade=%d", proxy.Count, upgrade.Count)
	}
	if report.HTTP.Requests != 1 || report.HTTP.Errors != 1 {
		t.Fatalf("only the rejected upgrade should count as HTTP traffic: %+v", report.HTTP)
	}
}

func TestRequestCaptureIncludesStatusAndOpenWebSocket(t *testing.T) {
	stop := make(chan struct{})
	defer close(stop)
	collector := New(stop)
	capture := make(chan struct{})
	defer close(capture)
	httpHandler := collector.Middleware(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusForbidden)
	}))
	request := httptest.NewRequest(http.MethodGet, "http://test.example/docs?files&token=secret", nil)
	httpHandler.ServeHTTP(httptest.NewRecorder(), request)
	if _, err := collector.CaptureRequests(capture, false, stop); err != nil {
		t.Fatal(err)
	}
	otherCapture := make(chan struct{})
	defer close(otherCapture)
	if _, err := collector.CaptureRequests(otherCapture, false, stop); err == nil {
		t.Fatal("overlapping capture replaced the active owner")
	}
	httpHandler.ServeHTTP(httptest.NewRecorder(), request)
	connection, peer := net.Pipe()
	defer peer.Close()
	defer connection.Close()
	upgraded := make(chan struct{})
	resume := make(chan struct{})
	defer close(resume)
	socketHandler := collector.Middleware(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		Route(request, ProxyApplication)
		if _, _, err := http.NewResponseController(writer).Hijack(); err != nil {
			t.Error(err)
		}
		close(upgraded)
		<-resume
	}))
	socketRequest := httptest.NewRequest(http.MethodGet, "http://test.example/?server_update_ws", nil)
	socketRequest.Header.Set("Upgrade", "websocket")
	go socketHandler.ServeHTTP(hijackRecorder{httptest.NewRecorder(), connection}, socketRequest)
	<-upgraded

	report, err := collector.CaptureRequests(capture, true, stop)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.HTTPCalls) != 2 {
		t.Fatalf("capture contains %d requests: %+v", len(report.HTTPCalls), report.HTTPCalls)
	}
	files, socket := report.HTTPCalls[0], report.HTTPCalls[1]
	if files.Route != "/docs?files" || files.Status != http.StatusForbidden || socket.Route != "/?server_update_ws" || socket.Status != http.StatusSwitchingProtocols {
		t.Fatalf("unexpected HTTP capture: %+v", report.HTTPCalls)
	}
	if report.Stages[ProxyApplication].Count != 0 || report.HTTP.Requests != 2 {
		t.Fatal("open WebSocket contributed to HTTP traffic or proxy latency")
	}
}

func TestRequestCaptureReportsDetailLimitWithoutDroppingHTTPTotals(t *testing.T) {
	stop := make(chan struct{})
	defer close(stop)
	collector := New(stop)
	capture := make(chan struct{})
	defer close(capture)
	if _, err := collector.CaptureRequests(capture, false, stop); err != nil {
		t.Fatal(err)
	}
	for range httpCallsLimit + 3 {
		if !collector.Observe(Event{Call: HTTPCall{Method: "GET", Route: "/"}, HTTP: HTTPMetrics{Requests: 1}}) {
			t.Fatal("unexpected queue overflow")
		}
	}
	report, err := collector.CaptureRequests(capture, true, stop)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.HTTPCalls) != httpCallsLimit || report.HTTPCallsOmitted != 3 || report.HTTP.Requests != httpCallsLimit+3 {
		t.Fatalf("wrong capture totals: %d captured, %d omitted, %d HTTP requests", len(report.HTTPCalls), report.HTTPCallsOmitted, report.HTTP.Requests)
	}
}
