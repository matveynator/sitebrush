package channelacme

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type acmeRoundTripFunc func(*http.Request) (*http.Response, error)

func (roundTrip acmeRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

func TestIssueCompletesACMEFlowThroughCertificateValidation(t *testing.T) {
	certificatePEM := selfSignedTestCertificate(t, "example.com")
	var manager *Manager
	var accountPosts atomic.Int32
	var authorizationPosts atomic.Int32
	var orderPosts atomic.Int32
	transport := acmeRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method == http.MethodGet && request.URL.Path == "/directory" {
			return acmeTestResponse(request, http.StatusOK, nil, `{"newNonce":"https://acme.test/nonce","newAccount":"https://acme.test/account","newOrder":"https://acme.test/order"}`), nil
		}
		if request.Method == http.MethodHead && request.URL.Path == "/nonce" {
			return acmeTestResponse(request, http.StatusNoContent, http.Header{"Replay-Nonce": []string{"nonce"}}, ""), nil
		}
		headers := http.Header{"Replay-Nonce": []string{"next-nonce"}}
		switch request.URL.Path {
		case "/account":
			if accountPosts.Add(1) == 1 {
				return acmeTestResponse(request, http.StatusBadRequest, headers, `{"type":"urn:ietf:params:acme:error:badNonce"}`), nil
			}
			headers.Set("Location", "https://acme.test/account/1")
			return acmeTestResponse(request, http.StatusCreated, headers, `{}`), nil
		case "/order", "/order/1":
			if orderPosts.Add(1) == 1 {
				headers.Set("Location", "https://acme.test/order/1")
				return acmeTestResponse(request, http.StatusCreated, headers, `{"status":"pending","authorizations":["https://acme.test/authorization/1"],"finalize":"https://acme.test/finalize/1"}`), nil
			}
			return acmeTestResponse(request, http.StatusOK, headers, `{"status":"valid","certificate":"https://acme.test/certificate/1"}`), nil
		case "/authorization/1":
			if authorizationPosts.Add(1) > 1 {
				return acmeTestResponse(request, http.StatusOK, headers, `{"status":"valid"}`), nil
			}
			return acmeTestResponse(request, http.StatusOK, headers, `{"status":"pending","challenges":[{"type":"http-01","url":"https://acme.test/challenge/1","token":"token-1"}]}`), nil
		case "/challenge/1":
			challengeResponse := httptest.NewRecorder()
			manager.HTTPHandler(http.NotFoundHandler()).ServeHTTP(challengeResponse, httptest.NewRequest(http.MethodGet, "/.well-known/acme-challenge/token-1", nil))
			if challengeResponse.Code != http.StatusOK || !strings.HasPrefix(challengeResponse.Body.String(), "token-1.") {
				return acmeTestResponse(request, http.StatusInternalServerError, headers, "challenge was not provisioned"), nil
			}
			return acmeTestResponse(request, http.StatusAccepted, headers, `{}`), nil
		case "/finalize/1":
			return acmeTestResponse(request, http.StatusAccepted, headers, `{}`), nil
		case "/certificate/1":
			return acmeTestResponse(request, http.StatusOK, headers, string(certificatePEM)), nil
		default:
			return acmeTestResponse(request, http.StatusNotFound, headers, "not found"), nil
		}
	})
	var err error
	manager, err = Start(Config{DirectoryURL: "https://acme.test/directory", CacheDir: t.TempDir(), HTTPClient: &http.Client{Transport: transport}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	result := manager.Issue(ctx, " Example.COM ")
	if result.Err == nil || !strings.Contains(result.Err.Error(), "private key does not match public key") {
		t.Fatalf("Issue error=%v, want local test-certificate key rejection", result.Err)
	}
	if accountPosts.Load() != 2 || authorizationPosts.Load() != 2 || orderPosts.Load() != 2 {
		t.Fatalf("ACME protocol counts account=%d authorization=%d order=%d", accountPosts.Load(), authorizationPosts.Load(), orderPosts.Load())
	}
}

func TestOrderPollingNonceRetryAndCertificateStorageBranches(t *testing.T) {
	accountKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`not-json`, `{"status":"invalid"}`, `{"status":"valid"}`} {
		state := &clientState{config: Config{HTTPClient: &http.Client{Transport: acmeRoundTripFunc(func(request *http.Request) (*http.Response, error) {
			return acmeTestResponse(request, http.StatusOK, http.Header{"Replay-Nonce": []string{"next"}}, body), nil
		})}}, accountKey: accountKey, accountKID: "account", nonce: "nonce"}
		if _, err := state.waitForOrder(context.Background(), "https://acme.test/order"); err == nil {
			t.Errorf("invalid order response %q accepted", body)
		}
	}
	var nonceCount, postCount atomic.Int32
	state := &clientState{
		config: Config{HTTPClient: &http.Client{Transport: acmeRoundTripFunc(func(request *http.Request) (*http.Response, error) {
			if request.Method == http.MethodHead {
				nonceCount.Add(1)
				return acmeTestResponse(request, http.StatusNoContent, http.Header{"Replay-Nonce": []string{"nonce"}}, ""), nil
			}
			postCount.Add(1)
			return acmeTestResponse(request, http.StatusBadRequest, nil, `badNonce`), nil
		})}},
		directory: directory{NewNonce: "https://acme.test/nonce"}, accountKey: accountKey,
	}
	if _, _, err := state.post(context.Background(), "https://acme.test/order", nil); err == nil || !strings.Contains(err.Error(), "three consecutive nonces") {
		t.Fatalf("nonce retries error=%v", err)
	}
	if nonceCount.Load() != 3 || postCount.Load() != 3 {
		t.Fatalf("nonce retry counts head=%d post=%d", nonceCount.Load(), postCount.Load())
	}

	certificatePEM := selfSignedTestCertificate(t, "example.com")
	leafBlock, _ := pem.Decode(certificatePEM)
	leaf, err := x509.ParseCertificate(leafBlock.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	cacheDir := t.TempDir()
	if err := storeCertificate(cacheDir, "example.com", certificatePEM, leaf.NotAfter); err != nil {
		t.Fatal(err)
	}
	if err := storeCertificate(cacheDir, "example.com", []byte("older"), leaf.NotAfter.Add(-time.Second)); err == nil {
		t.Fatal("older replacement certificate accepted")
	}
	if err := storeCertificate(cacheDir, "example.com", []byte("renewed"), leaf.NotAfter.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	blockedPath := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blockedPath, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := atomicWrite(filepath.Join(blockedPath, "certificate"), []byte("x")); err == nil {
		t.Fatal("atomic write below a file succeeded")
	}
	existingDirectory := filepath.Join(t.TempDir(), "certificate")
	if err := os.Mkdir(existingDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := atomicWrite(existingDirectory, []byte("x")); err == nil {
		t.Fatal("atomic write over a directory succeeded")
	}
}

func TestACMEPostAndAuthorizationValidationFailures(t *testing.T) {
	if _, _, err := (&clientState{}).post(context.Background(), "", nil); err == nil {
		t.Fatal("empty endpoint accepted")
	}
	state := &clientState{directory: directory{NewOrder: "https://acme.test/order"}}
	if err := state.prepareAccount(context.Background()); err == nil {
		t.Fatal("account creation without nonce endpoint succeeded")
	}
	accountKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{"status":"pending","challenges":[]}`, `not-json`} {
		state = &clientState{
			config: Config{HTTPClient: &http.Client{Transport: acmeRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				return acmeTestResponse(request, http.StatusOK, http.Header{"Replay-Nonce": []string{"next"}}, body), nil
			})}},
			directory: directory{NewNonce: "https://acme.test/nonce"}, accountKey: accountKey, nonce: "nonce", accountKID: "account",
		}
		if err := state.authorize(context.Background(), "https://acme.test/authorization"); err == nil {
			t.Errorf("authorization body %q accepted", body)
		}
	}
}

func TestIssueAndAccountProtocolEarlyErrors(t *testing.T) {
	if _, _, err := (&clientState{}).issue(context.Background(), ""); err == nil {
		t.Fatal("empty domain accepted")
	}
	accountKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	for _, reply := range []struct {
		body, location string
		status         int
		want           string
	}{
		{body: "not-json", status: http.StatusCreated, location: "https://acme.test/order/1", want: "decode ACME order"},
		{body: `{"status":"pending"}`, status: http.StatusCreated, want: "order location is missing"},
		{body: `{"status":"pending","finalize":"https://acme.test/finalize"}`, status: http.StatusCreated, location: "https://acme.test/order/1", want: "HTTP 503"},
	} {
		state := &clientState{
			config: Config{CacheDir: t.TempDir(), HTTPClient: &http.Client{Transport: acmeRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				headers := http.Header{"Replay-Nonce": []string{"next"}}
				if reply.location != "" {
					headers.Set("Location", reply.location)
				}
				status := reply.status
				if request.URL.Path == "/finalize" {
					status = http.StatusServiceUnavailable
					return acmeTestResponse(request, status, headers, "service unavailable"), nil
				}
				return acmeTestResponse(request, status, headers, reply.body), nil
			})}},
			directory: directory{NewOrder: "https://acme.test/order"}, accountKey: accountKey, accountKID: "account", nonce: "nonce",
		}
		_, _, issueErr := state.issue(context.Background(), "example.com")
		if issueErr == nil || !strings.Contains(issueErr.Error(), reply.want) {
			t.Errorf("issue error=%v, want %q", issueErr, reply.want)
		}
	}
	brokenDirectory := &clientState{config: Config{DirectoryURL: "://bad", HTTPClient: &http.Client{}}}
	if err := brokenDirectory.prepareAccount(context.Background()); err == nil {
		t.Fatal("malformed directory URL accepted")
	}
}

func TestACMEClientResponseAndPersistenceEdgeCases(t *testing.T) {
	state := &clientState{directory: directory{NewNonce: "https://acme.test/nonce"}, config: Config{HTTPClient: &http.Client{Transport: acmeRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		return acmeTestResponse(request, http.StatusNoContent, nil, ""), nil
	})}}}
	if err := state.fetchNonce(context.Background()); err == nil {
		t.Fatal("empty replay nonce accepted")
	}
	state.config.HTTPClient = &http.Client{Transport: acmeRoundTripFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("transport failed") })}
	if err := state.fetchNonce(context.Background()); err == nil {
		t.Fatal("transport error ignored")
	}
	if _, err := (&clientState{}).signedBody("https://acme.test/order", make(chan int)); err == nil {
		t.Fatal("unmarshalable JWS payload accepted")
	}
	if _, err := readResponse(&http.Response{StatusCode: http.StatusOK, Body: failingReadCloser{}}); err == nil {
		t.Fatal("body read failure ignored")
	}
	dateHeader := http.Header{"Retry-After": []string{time.Now().Add(time.Minute).UTC().Format(http.TimeFormat)}}
	if _, ok := RetryAfter(responseError(&http.Response{StatusCode: http.StatusServiceUnavailable, Header: dateHeader}, nil)); !ok {
		t.Fatal("HTTP-date Retry-After was ignored")
	}

	cacheDir := t.TempDir()
	keyPath := filepath.Join(cacheDir, "acme_account+key")
	if err := os.WriteFile(keyPath, []byte("invalid key"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadOrCreateAccountKey(cacheDir); err != nil {
		t.Fatalf("corrupt account key was not replaced: %v", err)
	}
}

func TestAccountAndChallengeSetupFailureBranches(t *testing.T) {
	blockedCache := filepath.Join(t.TempDir(), "cache")
	if err := os.WriteFile(blockedCache, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Start(Config{CacheDir: blockedCache}); err == nil {
		t.Fatal("account key creation below a file succeeded")
	}
	if err := (&clientState{config: Config{HTTPClient: &http.Client{}}, directory: directory{NewNonce: "://bad"}}).fetchNonce(context.Background()); err == nil {
		t.Fatal("malformed nonce URL accepted")
	}
	if _, _, err := (&clientState{config: Config{HTTPClient: &http.Client{Transport: acmeRoundTripFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("transport failed") })}}, nonce: "nonce", accountKey: mustACMEKey(t), accountKID: "account"}).post(context.Background(), "https://acme.test/order", nil); err == nil {
		t.Fatal("POST transport error ignored")
	}

	var posts atomic.Int32
	state := &clientState{config: Config{DirectoryURL: "https://acme.test/directory", HTTPClient: &http.Client{Transport: acmeRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method == http.MethodGet {
			return acmeTestResponse(request, http.StatusOK, nil, `{"newNonce":"https://acme.test/nonce","newAccount":"https://acme.test/account","newOrder":"https://acme.test/order"}`), nil
		}
		if request.Method == http.MethodHead {
			return acmeTestResponse(request, http.StatusNoContent, http.Header{"Replay-Nonce": []string{"nonce"}}, ""), nil
		}
		posts.Add(1)
		return acmeTestResponse(request, http.StatusCreated, http.Header{"Replay-Nonce": []string{"next"}}, `{}`), nil
	})}}}
	state.accountKey = mustACMEKey(t)
	if err := state.prepareAccount(context.Background()); err == nil || !strings.Contains(err.Error(), "account location is missing") {
		t.Fatalf("missing account location error=%v", err)
	}
	if posts.Load() != 1 {
		t.Fatalf("account post count=%d", posts.Load())
	}
}

func TestAuthorizationRejectionAndOrderWaitCancellation(t *testing.T) {
	accountKey := mustACMEKey(t)
	var authorizationChecks atomic.Int32
	state := &clientState{
		config: Config{HTTPClient: &http.Client{Transport: acmeRoundTripFunc(func(request *http.Request) (*http.Response, error) {
			if request.URL.Path == "/authorization" {
				if authorizationChecks.Add(1) == 1 {
					return acmeTestResponse(request, http.StatusOK, http.Header{"Replay-Nonce": []string{"next"}}, `{"status":"pending","challenges":[{"type":"http-01","url":"https://acme.test/challenge","token":"token"}]}`), nil
				}
				return acmeTestResponse(request, http.StatusOK, http.Header{"Replay-Nonce": []string{"next"}}, `{"status":"invalid"}`), nil
			}
			return acmeTestResponse(request, http.StatusAccepted, http.Header{"Replay-Nonce": []string{"next"}}, `{}`), nil
		})}},
		accountKey: accountKey, accountKID: "account", nonce: "nonce", challenges: make(chan challengeRequest, 4),
	}
	if err := state.authorize(context.Background(), "https://acme.test/authorization"); err == nil || !strings.Contains(err.Error(), "became invalid") {
		t.Fatalf("invalid authorization error=%v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	waitState := &clientState{config: Config{HTTPClient: &http.Client{Transport: acmeRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		return acmeTestResponse(request, http.StatusOK, nil, `{"status":"pending"}`), nil
	})}}, accountKey: accountKey, accountKID: "account", nonce: "nonce"}
	if _, err := waitState.waitForOrder(ctx, "https://acme.test/order"); !errors.Is(err, context.Canceled) {
		t.Fatalf("order wait cancellation=%v", err)
	}
}

func mustACMEKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

type failingReadCloser struct{}

func (failingReadCloser) Read([]byte) (int, error) { return 0, errors.New("read failure") }
func (failingReadCloser) Close() error             { return nil }

func acmeTestResponse(request *http.Request, status int, headers http.Header, body string) *http.Response {
	if headers == nil {
		headers = make(http.Header)
	}
	return &http.Response{StatusCode: status, Status: fmt.Sprintf("%d %s", status, http.StatusText(status)), Header: headers, Body: io.NopCloser(strings.NewReader(body)), Request: request}
}

func selfSignedTestCertificate(t *testing.T, domain string) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	template := &x509.Certificate{SerialNumber: big.NewInt(7), Subject: pkix.Name{CommonName: domain}, DNSNames: []string{domain}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}
