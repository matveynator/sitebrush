package netchanidentity

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

type identityTransport func(*http.Request) (*http.Response, error)

func (transport identityTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestDiscoveryUsesOnlyOfficialOriginAndRejectsInvalidIdentity(t *testing.T) {
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	valid := fmt.Sprintf(`{"algorithm":"Ed25519","public_key":%q}`, base64.StdEncoding.EncodeToString(publicKey))
	for _, fixture := range []struct {
		name, body string
		status     int
		accepted   bool
	}{
		{"valid", valid, 200, true},
		{"redirect", "", 302, false},
		{"unavailable", valid, 503, false},
		{"wrong-algorithm", strings.Replace(valid, "Ed25519", "X25519", 1), 200, false},
		{"missing-key", `{"algorithm":"Ed25519"}`, 200, false},
		{"trailing-content", valid + `{}`, 200, false},
		{"oversized", valid + strings.Repeat(" ", 1025), 200, false},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			client := discoveryClient()
			client.Transport = identityTransport(func(request *http.Request) (*http.Response, error) {
				if request.URL.String() != URL || request.Method != http.MethodGet || request.Header.Get("Authorization") != "" {
					t.Fatal("discovery escaped its fixed credential-free HTTPS boundary")
				}
				return &http.Response{StatusCode: fixture.status, Body: io.NopCloser(strings.NewReader(fixture.body)), Header: http.Header{"Location": {"http://127.0.0.1/identity"}}, Request: request}, nil
			})
			actual, err := fetch(client, nil)
			if (err == nil) != fixture.accepted || (fixture.accepted && !actual.Equal(publicKey)) {
				t.Fatal("incorrect discovery outcome", actual, err)
			}
		})
	}
}

func TestDiscoveryCancellationSkipsNetwork(t *testing.T) {
	stop := make(chan struct{})
	close(stop)
	client := discoveryClient()
	client.Transport = identityTransport(func(*http.Request) (*http.Response, error) {
		t.Fatal("cancelled discovery made a request")
		return nil, nil
	})
	if _, err := fetch(client, stop); err == nil {
		t.Fatal("cancelled discovery succeeded")
	}
}
