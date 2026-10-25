package requesttiming

import (
	"context"
	"io"
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
