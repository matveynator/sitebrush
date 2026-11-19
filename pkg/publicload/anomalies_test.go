package publicload

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strings"
	"testing"
)

type anomalyTransport func(*http.Request) (*http.Response, error)

func (transport anomalyTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func anomalyClient(status int, declared int64, body func() io.ReadCloser) *http.Client {
	return &http.Client{Transport: anomalyTransport(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: status, Proto: "HTTP/1.1", ContentLength: declared, Header: http.Header{"Etag": []string{`"fixture"`}}, Body: body(), Request: request}, nil
	})}
}

func TestAnomalySamplesAreOptInAndGloballyBounded(t *testing.T) {
	source := strings.Repeat("diagnostic ", 300)
	client := anomalyClient(503, int64(len(source)), func() io.ReadCloser { return io.NopCloser(strings.NewReader(source)) })
	for _, limit := range []int{0, 1, 16} {
		report, err := Run(client, Options{URL: "http://public.test/", Requests: 80, Concurrency: 20, ErrorSamples: limit}, nil)
		if err != nil || len(report.Anomalies) != limit || report.Non2xx != 80 {
			t.Fatalf("limit=%d samples=%d non2xx=%d error=%v", limit, len(report.Anomalies), report.Non2xx, err)
		}
		digest := sha256.Sum256([]byte(source))
		for _, anomaly := range report.Anomalies {
			if anomaly.Reason != "status" || anomaly.Status != 503 || anomaly.Protocol != "HTTP/1.1" || anomaly.DeclaredBytes != int64(len(source)) || anomaly.ActualBytes != int64(len(source)) || anomaly.ETag != `"fixture"` || anomaly.SHA256 != hex.EncodeToString(digest[:]) || len(anomaly.BodyPrefix) != 1024 || anomaly.BodyPrefix != source[:1024] {
				t.Fatalf("incorrect response evidence: %+v", anomaly)
			}
		}
	}
	for _, limit := range []int{-1, 17} {
		if _, err := Run(client, Options{URL: "http://public.test/", Requests: 1, Concurrency: 1, ErrorSamples: limit}, nil); err == nil {
			t.Fatal("accepted unbounded sample limit", limit)
		}
	}
}

type truncatedAnomalyBody struct{ remaining []byte }

func (body *truncatedAnomalyBody) Read(destination []byte) (int, error) {
	count := copy(destination, body.remaining)
	body.remaining = body.remaining[count:]
	return count, io.ErrUnexpectedEOF
}
func (*truncatedAnomalyBody) Close() error { return nil }

func TestAnomalySamplesDistinguishBodyAndTransportFailures(t *testing.T) {
	for _, scenario := range []struct {
		name, reason       string
		declared, expected int64
		expectedDigest     string
		source             func() io.ReadCloser
	}{
		{"declared", "declared_length", 8, 0, "", func() io.ReadCloser { return io.NopCloser(strings.NewReader("short")) }},
		{"expected", "length", 5, 8, "", func() io.ReadCloser { return io.NopCloser(strings.NewReader("short")) }},
		{"content", "content", 5, 5, strings.Repeat("0", 64), func() io.ReadCloser { return io.NopCloser(strings.NewReader("short")) }},
		{"truncated", "transport", 8, 8, "", func() io.ReadCloser { return &truncatedAnomalyBody{remaining: []byte("short")} }},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			report, err := Run(anomalyClient(200, scenario.declared, scenario.source), Options{URL: "http://public.test/", Requests: 1, Concurrency: 1, ErrorSamples: 1, ExpectedBytes: scenario.expected, ExpectedSHA256: scenario.expectedDigest}, nil)
			if err != nil || len(report.Anomalies) != 1 {
				t.Fatal(report, err)
			}
			anomaly := report.Anomalies[0]
			if anomaly.Reason != scenario.reason || anomaly.BodyPrefix != "short" || anomaly.ActualBytes != 5 || anomaly.ExpectedBytes != scenario.expected {
				t.Fatal(anomaly)
			}
			if scenario.reason == "transport" && (report.TransportErrors != 1 || !strings.Contains(anomaly.ReadError, "unexpected EOF")) {
				t.Fatal(report, anomaly)
			}
		})
	}
}

func TestAnomalyPrefixCopiesSurviveWorkerReuseAndBinaryData(t *testing.T) {
	bodies := make(chan []byte, 2)
	bodies <- []byte("first error")
	bodies <- []byte{0xff, 0xfe, 0x00}
	client := &http.Client{Transport: anomalyTransport(func(request *http.Request) (*http.Response, error) {
		source := <-bodies
		return &http.Response{StatusCode: 500, Proto: "HTTP/1.1", ContentLength: int64(len(source)), Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(source)), Request: request}, nil
	})}
	report, err := Run(client, Options{URL: "http://public.test/", Requests: 2, Concurrency: 1, ErrorSamples: 2}, nil)
	if err != nil || len(report.Anomalies) != 2 {
		t.Fatal(report, err)
	}
	if report.Anomalies[0].BodyPrefix != "first error" || report.Anomalies[0].BodyPrefixEncoding != "utf-8" || report.Anomalies[1].BodyPrefix != "fffe00" || report.Anomalies[1].BodyPrefixEncoding != "hex" {
		t.Fatal(report.Anomalies)
	}
}

func TestSuccessfulResponsesDoNotConsumeAnomalyTickets(t *testing.T) {
	client := anomalyClient(200, 5, func() io.ReadCloser { return io.NopCloser(strings.NewReader("valid")) })
	report, err := Run(client, Options{URL: "http://public.test/", Requests: 8, Concurrency: 4, ErrorSamples: 16, ExpectedBytes: 5}, nil)
	if err != nil || report.Successful != 8 || len(report.Anomalies) != 0 {
		t.Fatal(report, err)
	}
}
