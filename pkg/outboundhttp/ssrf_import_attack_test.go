package outboundhttp

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

type redirectAttackRoundTripper struct{ calls int }

func (transport *redirectAttackRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	transport.calls++
	redirectTarget := "https://public.example/next"
	if transport.calls == 2 {
		redirectTarget = "http://127.0.0.1/private"
	}
	return &http.Response{
		StatusCode: http.StatusFound,
		Header:     http.Header{"Location": []string{redirectTarget}},
		Body:       io.NopCloser(strings.NewReader("redirect")),
		Request:    request,
	}, nil
}

func TestSSRFAttackPublicImportRedirectNeverRequestsPrivateTarget(t *testing.T) {
	transport := &redirectAttackRoundTripper{}
	client := &http.Client{Transport: transport, CheckRedirect: CheckRedirect}
	request, err := http.NewRequest(http.MethodGet, "https://public.example/import", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if response != nil {
		_ = response.Body.Close()
	}
	if err == nil {
		t.Fatal("SECURITY: public import followed a redirect to loopback")
	}
	if transport.calls != 2 {
		t.Fatalf("SECURITY: transport opened %d requests, want the initial request and one public redirect", transport.calls)
	}
}
