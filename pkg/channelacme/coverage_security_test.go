package channelacme

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestACMEStorageAndConfigurationFailures(t *testing.T) {
	cacheParent := t.TempDir()
	cachePath := filepath.Join(cacheParent, "not-a-directory")
	if err := os.WriteFile(cachePath, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Start(Config{CacheDir: cachePath}); err == nil {
		t.Fatal("ACME accepted a cache path occupied by a file")
	}
	if err := atomicWrite(filepath.Join(cachePath, "certificate"), []byte("certificate")); err == nil {
		t.Fatal("atomicWrite accepted a path below a regular file")
	}

	invalidKeyPath := filepath.Join(t.TempDir(), "acme_account+key")
	if err := os.WriteFile(invalidKeyPath, []byte("not a PEM key"), 0o600); err != nil {
		t.Fatal(err)
	}
	key, err := loadOrCreateAccountKey(filepath.Dir(invalidKeyPath))
	if err != nil || key == nil {
		t.Fatalf("invalid cached key was not replaced: %v", err)
	}
}

func TestACMEProtocolRejectsInvalidAuthorizationAndOrders(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	state := &clientState{accountKey: key, accountKID: "account", nonce: "nonce"}
	if _, _, err := state.issue(context.Background(), ""); err == nil {
		t.Fatal("empty ACME domain was accepted")
	}
	for _, domain := range []string{"../example.com", `..\example.com`} {
		if _, _, err := state.issue(context.Background(), domain); err == nil {
			t.Fatalf("unsafe ACME cache domain was accepted: %q", domain)
		}
	}
	for _, body := range []string{
		`{"status":"pending","challenges":[]}`,
		`{"status":"pending","challenges":[{"type":"dns-01","url":"https://acme.test/challenge","token":"token"}]}`,
	} {
		state.config.HTTPClient = &http.Client{Transport: acmeRoundTripFunc(func(request *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Replay-Nonce": []string{"next"}}, Body: io.NopCloser(strings.NewReader(body))}, nil
		})}
		if err := state.authorize(context.Background(), "https://acme.test/authorization"); err == nil {
			t.Fatalf("invalid authorization body was accepted: %s", body)
		}
	}
	state.config.HTTPClient = &http.Client{}
	if _, err := state.waitForOrder(context.Background(), ""); err == nil || !strings.Contains(err.Error(), "endpoint") {
		t.Fatalf("missing order endpoint error = %v", err)
	}
	if _, _, err := state.post(context.Background(), "", nil); err == nil {
		t.Fatal("missing post endpoint was accepted")
	}
}

func TestACMEReadAndAtomicWriteFailuresAreReported(t *testing.T) {
	if _, err := readResponse(&http.Response{StatusCode: http.StatusOK, Body: failingReadCloser{}}); err == nil {
		t.Fatal("response read failure was hidden")
	}
	if _, ok := RetryAfter(errors.New("not ACME")); ok {
		t.Fatal("unrelated error exposed Retry-After")
	}
	if err := atomicWrite(filepath.Join(t.TempDir(), "certificate"), []byte("safe")); err != nil {
		t.Fatal(err)
	}
}

func TestACMEIssueAndNonceFailuresAreExplicit(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	for _, testCase := range []struct {
		name   string
		body   string
		header string
	}{
		{name: "malformed order", body: "not-json", header: "https://acme.test/order"},
		{name: "missing order location", body: `{"status":"pending"}`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			state := &clientState{
				config: Config{HTTPClient: &http.Client{Transport: acmeRoundTripFunc(func(request *http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: http.StatusCreated, Header: http.Header{"Location": []string{testCase.header}}, Body: io.NopCloser(strings.NewReader(testCase.body))}, nil
				})}},
				directory:  directory{NewOrder: "https://acme.test/order"},
				accountKey: key,
				accountKID: "https://acme.test/account",
				nonce:      "nonce",
			}
			if _, _, err := state.issue(context.Background(), "example.com"); err == nil {
				t.Fatal("malformed ACME order was accepted")
			}
		})
	}

	state := &clientState{config: Config{HTTPClient: &http.Client{Transport: acmeRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	})}}, directory: directory{NewNonce: "https://acme.test/nonce"}}
	if err := state.fetchNonce(context.Background()); err == nil {
		t.Fatal("empty nonce response was accepted")
	}
}

func TestACMEIssueStopsAtEachRemoteProtocolFailure(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	for _, testCase := range []struct {
		name       string
		failureURL string
	}{
		{name: "order request", failureURL: "https://acme.test/order"},
		{name: "finalize request", failureURL: "https://acme.test/finalize"},
		{name: "order polling", failureURL: "https://acme.test/order-result"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			state := &clientState{
				config: Config{HTTPClient: &http.Client{Transport: acmeRoundTripFunc(func(request *http.Request) (*http.Response, error) {
					if request.URL.String() == testCase.failureURL {
						return nil, errors.New("ACME transport failure")
					}
					header := make(http.Header)
					header.Set("Replay-Nonce", "next")
					body := `{"status":"pending"}`
					if request.URL.String() == "https://acme.test/order" {
						body = `{"status":"pending","finalize":"https://acme.test/finalize"}`
						header.Set("Location", "https://acme.test/order-result")
					}
					return &http.Response{StatusCode: http.StatusOK, Header: header, Body: io.NopCloser(strings.NewReader(body))}, nil
				})}},
				directory: directory{NewOrder: "https://acme.test/order"}, accountKey: key, accountKID: "account", nonce: "nonce",
			}
			if _, _, err := state.issue(context.Background(), "example.com"); err == nil {
				t.Fatal("remote ACME failure was hidden")
			}
		})
	}
}

func TestACMEHTTPHandlerAndChallengeStoreLifecycle(t *testing.T) {
	requests := make(chan challengeRequest, 4)
	go runChallengeStore(requests)
	manager := &Manager{challenges: requests}
	fallbackCalled := false
	fallback := http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		fallbackCalled = true
		responseWriter.WriteHeader(http.StatusAccepted)
	})
	request := httptest.NewRequest(http.MethodGet, "/ordinary", nil)
	response := httptest.NewRecorder()
	manager.HTTPHandler(fallback).ServeHTTP(response, request)
	if !fallbackCalled || response.Code != http.StatusAccepted {
		t.Fatalf("fallback response = %d, called=%t", response.Code, fallbackCalled)
	}
	request = httptest.NewRequest(http.MethodGet, "/.well-known/acme-challenge/missing", nil)
	response = httptest.NewRecorder()
	manager.HTTPHandler(fallback).ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("missing challenge status = %d", response.Code)
	}
	requests <- challengeRequest{action: "put", path: "/.well-known/acme-challenge/live", body: "proof"}
	request = httptest.NewRequest(http.MethodGet, "/.well-known/acme-challenge/live", nil)
	response = httptest.NewRecorder()
	manager.HTTPHandler(fallback).ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "proof" {
		t.Fatalf("challenge response = %d %q", response.Code, response.Body.String())
	}
	requests <- challengeRequest{action: "delete", path: "/.well-known/acme-challenge/live"}
}

func TestACMEAccountPreparationFailsClosedForDirectoryAndRegistrationErrors(t *testing.T) {
	for _, testCase := range []struct {
		name        string
		response    *http.Response
		responseErr error
	}{
		{name: "directory transport", responseErr: errors.New("directory unavailable")},
		{name: "directory JSON", response: &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("not-json"))}},
		{name: "account location", response: &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"newNonce":"https://acme.test/nonce","newAccount":"https://acme.test/account","newOrder":"https://acme.test/order"}`))}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			state := &clientState{config: Config{DirectoryURL: "https://acme.test/directory", HTTPClient: &http.Client{Transport: acmeRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				if testCase.responseErr != nil {
					return nil, testCase.responseErr
				}
				response := *testCase.response
				if testCase.name == "account location" {
					response.Header = make(http.Header)
				}
				return &response, nil
			})}}}
			if err := state.prepareAccount(context.Background()); err == nil {
				t.Fatal("ACME account preparation hid an upstream failure")
			}
		})
	}
}

func TestACMEIssueRejectsMalformedCertificatePayload(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	state := &clientState{
		config: Config{CacheDir: t.TempDir(), HTTPClient: &http.Client{Transport: acmeRoundTripFunc(func(request *http.Request) (*http.Response, error) {
			header := make(http.Header)
			header.Set("Replay-Nonce", "next")
			body := `{"status":"pending","finalize":"https://acme.test/finalize"}`
			if request.URL.String() == "https://acme.test/order-result" {
				body = `{"status":"valid","certificate":"https://acme.test/certificate"}`
			} else if request.URL.String() == "https://acme.test/certificate" {
				body = "not-a-certificate"
			}
			if request.URL.String() == "https://acme.test/order" {
				header.Set("Location", "https://acme.test/order-result")
			}
			return &http.Response{StatusCode: http.StatusOK, Header: header, Body: io.NopCloser(strings.NewReader(body))}, nil
		})}},
		directory: directory{NewOrder: "https://acme.test/order"}, accountKey: key, accountKID: "account", nonce: "nonce",
	}
	if _, _, err := state.issue(context.Background(), "example.com"); err == nil {
		t.Fatal("malformed certificate payload was accepted")
	}
}

func TestACMEAtomicWriteRejectsDirectoryTarget(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := atomicWrite(target, []byte("certificate")); err == nil {
		t.Fatal("atomicWrite replaced a directory target")
	}
}
