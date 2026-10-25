package publicload

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNewTLSConnectionsAndKeepAliveHaveSeparateSamples(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { _, _ = writer.Write([]byte("static")) }))
	defer server.Close()
	for _, fresh := range []bool{false, true} {
		transport := server.Client().Transport.(*http.Transport).Clone()
		transport.DisableKeepAlives = fresh
		transport.Protocols = new(http.Protocols)
		transport.Protocols.SetHTTP1(true)
		client := &http.Client{Transport: transport}
		report, err := Run(client, Options{URL: server.URL, Requests: 6, Concurrency: 1, ExpectedBytes: 6}, nil)
		transport.CloseIdleConnections()
		if err != nil || report.Successful != 6 || report.TransportErrors != 0 || report.IncompleteTraces != 0 {
			t.Fatal(report, err)
		}
		expected := 1
		if fresh {
			expected = 6
		}
		if report.NewConnections != expected || report.TCP.Samples != expected || report.TLS.Samples != expected || report.ResponseWait.Samples != 6 || report.ReusedConnections != 6-expected {
			t.Fatal("connection samples include reused connections", report)
		}
	}
}

func TestLoadCancellationInterruptsAnActiveHTTPBoundary(t *testing.T) {
	entered := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		entered <- struct{}{}
		<-request.Context().Done()
	}))
	defer server.Close()
	stop := make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		_, err := Run(server.Client(), Options{URL: server.URL, Requests: 100, Concurrency: 1}, stop)
		finished <- err
	}()
	<-entered
	close(stop)
	select {
	case err := <-finished:
		if err == nil {
			t.Fatal("canceled load reported a complete run")
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not release the active HTTP request")
	}
}

func TestBurstAndContentValidation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { _, _ = writer.Write([]byte("static")) }))
	defer server.Close()
	digest := sha256.Sum256([]byte("static"))
	options := Options{URL: server.URL, Requests: 17, Concurrency: 6, Burst: true, ExpectedBytes: 6, ExpectedSHA256: hex.EncodeToString(digest[:])}
	report, err := Run(server.Client(), options, nil)
	if err != nil || report.Successful != 17 || report.SuccessfulTTFB.Samples != 17 || report.ConnectionWait.Samples != 17 {
		t.Fatal("uneven burst did not complete", report, err)
	}
	digest = sha256.Sum256([]byte("wrong!"))
	options.ExpectedSHA256 = hex.EncodeToString(digest[:])
	report, err = Run(server.Client(), options, nil)
	if err != nil || report.ContentMismatches != 17 || report.Successful != 0 {
		t.Fatal("wrong content counted as useful throughput", report, err)
	}
}

func TestFailuresDoNotConflateStatusAndLength(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusServiceUnavailable} {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.WriteHeader(status)
			_, _ = writer.Write([]byte("short"))
		}))
		report, err := Run(server.Client(), Options{URL: server.URL, Requests: 4, Concurrency: 2, ExpectedBytes: 100}, nil)
		server.Close()
		if err != nil || report.Successful != 0 || report.TransportErrors != 0 {
			t.Fatal(report, err)
		}
		if status == http.StatusOK && report.LengthMismatches != 4 || status != http.StatusOK && report.Non2xx != 4 {
			t.Fatal("failed response classification was lost", report)
		}
	}
	stop := make(chan struct{})
	close(stop)
	report, err := Run(http.DefaultClient, Options{URL: "http://unused.test/", Requests: 4, Concurrency: 2}, stop)
	if err == nil || report.Requests != 0 {
		t.Fatal("cancelled consumer started requests", report, err)
	}
}
