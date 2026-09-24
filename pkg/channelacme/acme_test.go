package channelacme

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHTTPChallengeStoreServesOnlyProvisionedToken(t *testing.T) {
	manager, err := Start(Config{CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	challengePath := "/.well-known/acme-challenge/token"
	manager.challenges <- challengeRequest{action: "put", path: challengePath, body: "token.thumbprint"}
	handler := manager.HTTPHandler(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		responseWriter.WriteHeader(http.StatusTeapot)
	}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, challengePath, nil))
	if response.Code != http.StatusOK || response.Body.String() != "token.thumbprint" {
		t.Fatalf("challenge response code=%d body=%q", response.Code, response.Body.String())
	}
	if response.Header().Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("challenge content type=%q", response.Header().Get("Content-Type"))
	}
	fallbackResponse := httptest.NewRecorder()
	handler.ServeHTTP(fallbackResponse, httptest.NewRequest(http.MethodGet, "/ordinary", nil))
	if fallbackResponse.Code != http.StatusTeapot {
		t.Fatalf("fallback response code=%d", fallbackResponse.Code)
	}
	manager.challenges <- challengeRequest{action: "delete", path: challengePath}
	missingResponse := httptest.NewRecorder()
	handler.ServeHTTP(missingResponse, httptest.NewRequest(http.MethodGet, challengePath, nil))
	if missingResponse.Code != http.StatusNotFound {
		t.Fatalf("deleted challenge code=%d", missingResponse.Code)
	}
}

func TestHTTPChallengeHandlerStopsWhenRequestIsCancelled(t *testing.T) {
	manager := &Manager{challenges: make(chan challengeRequest)}
	requestContext, cancel := context.WithCancel(context.Background())
	go func() {
		<-manager.challenges
		cancel()
	}()
	request := httptest.NewRequest(http.MethodGet, "/.well-known/acme-challenge/pending", nil).WithContext(requestContext)
	response := httptest.NewRecorder()
	manager.HTTPHandler(http.NotFoundHandler()).ServeHTTP(response, request)
	if response.Body.Len() != 0 {
		t.Fatalf("cancelled response body=%q", response.Body.String())
	}
}

func TestSignedBodyUsesValidRawES256Signature(t *testing.T) {
	accountKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	state := clientState{accountKey: accountKey, accountKID: "account", nonce: "nonce"}
	signedBody, err := state.signedBody("https://acme.example/order", map[string]string{"hello": "world"})
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]string
	if err := json.Unmarshal([]byte(signedBody), &document); err != nil {
		t.Fatal(err)
	}
	signature, err := base64.RawURLEncoding.DecodeString(document["signature"])
	if err != nil || len(signature) != 64 {
		t.Fatalf("signature bytes=%d err=%v", len(signature), err)
	}
	digest := sha256.Sum256([]byte(document["protected"] + "." + document["payload"]))
	if !ecdsa.Verify(&accountKey.PublicKey, digest[:], newBigInteger(signature[:32]), newBigInteger(signature[32:])) {
		t.Fatal("JWS signature did not verify")
	}
}

func TestAccountKeyIsPersistedWithPrivatePermissions(t *testing.T) {
	cacheDir := t.TempDir()
	firstKey, err := loadOrCreateAccountKey(cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	secondKey, err := loadOrCreateAccountKey(cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	if firstKey.D.Cmp(secondKey.D) != 0 {
		t.Fatal("account key changed after reload")
	}
	keyInfo, err := os.Stat(filepath.Join(cacheDir, "acme_account+key"))
	if err != nil {
		t.Fatal(err)
	}
	if keyInfo.Mode().Perm() != 0o600 {
		t.Fatalf("account key permissions=%o", keyInfo.Mode().Perm())
	}
}

func TestIssueHonorsCancelledSubscriber(t *testing.T) {
	manager, err := Start(Config{CacheDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result := manager.Issue(ctx, "example.com")
	if result.Err == nil || !strings.Contains(result.Err.Error(), "canceled") {
		t.Fatalf("cancelled issue error=%v", result.Err)
	}
}

func TestResponseErrorAndRetryAfter(t *testing.T) {
	response := &http.Response{StatusCode: http.StatusTooManyRequests, Header: make(http.Header)}
	response.Header.Set("Retry-After", "30")
	issue := responseError(response, []byte(" rate limited "))
	if !strings.Contains(issue.Error(), "HTTP 429: rate limited") {
		t.Fatalf("response error = %q", issue)
	}
	retryAt, ok := RetryAfter(issue)
	if !ok || time.Until(retryAt) < 0 || time.Until(retryAt) > time.Minute {
		t.Fatalf("retry-after = %v, %t", retryAt, ok)
	}
	if _, ok := RetryAfter(errors.New("unrelated")); ok {
		t.Fatal("ordinary error unexpectedly included Retry-After")
	}
}

func TestReadResponseClosesBodyAndReportsStatus(t *testing.T) {
	response := &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("ok")), Header: make(http.Header)}
	body, err := readResponse(response)
	if err != nil || string(body) != "ok" {
		t.Fatalf("successful response = %q, %v", body, err)
	}
	response = &http.Response{StatusCode: http.StatusBadGateway, Body: io.NopCloser(strings.NewReader("upstream")), Header: make(http.Header)}
	body, err = readResponse(response)
	if string(body) != "upstream" || err == nil || !strings.Contains(err.Error(), "502") {
		t.Fatalf("failed response = %q, %v", body, err)
	}
}

func TestWaitReturnsOnTimerAndCancellation(t *testing.T) {
	if err := wait(context.Background(), time.Millisecond); err != nil {
		t.Fatalf("timer wait: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := wait(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled wait = %v", err)
	}
}

func TestStartRequiresCacheDirectoryAndPaddedInteger(t *testing.T) {
	if _, err := Start(Config{}); err == nil {
		t.Fatal("Start accepted an empty cache directory")
	}
	if got := paddedInteger(big.NewInt(258), 4); string(got) != "\x00\x00\x01\x02" {
		t.Fatalf("padded integer = %v", got)
	}
}

func TestValidateIssuedCertificate(t *testing.T) {
	now := time.Now()
	valid := &tls.Certificate{Certificate: [][]byte{{0x30}}, Leaf: &x509.Certificate{
		DNSNames:  []string{"example.com"},
		NotBefore: now.Add(-time.Hour),
		NotAfter:  now.Add(time.Hour),
		Issuer:    pkix.Name{CommonName: "test"},
		Subject:   pkix.Name{CommonName: "example.com"},
	}}
	if err := validateIssuedCertificate(valid, "example.com", now); err == nil || !strings.Contains(err.Error(), "chain") {
		t.Fatalf("untrusted leaf validation error = %v", err)
	}
	for _, certificate := range []*tls.Certificate{nil, {}} {
		if err := validateIssuedCertificate(certificate, "example.com", now); err == nil {
			t.Fatal("missing leaf was accepted")
		}
	}
	for _, testCase := range []struct {
		name  string
		leaf  *x509.Certificate
		chain [][]byte
	}{
		{name: "not yet valid", leaf: &x509.Certificate{NotBefore: now.Add(time.Hour), NotAfter: now.Add(2 * time.Hour), DNSNames: []string{"example.com"}}, chain: [][]byte{{0x30}}},
		{name: "expired", leaf: &x509.Certificate{NotBefore: now.Add(-2 * time.Hour), NotAfter: now.Add(-time.Hour), DNSNames: []string{"example.com"}}, chain: [][]byte{{0x30}}},
		{name: "hostname mismatch", leaf: &x509.Certificate{NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), DNSNames: []string{"other.example"}}, chain: [][]byte{{0x30}}},
		{name: "invalid intermediate", leaf: valid.Leaf, chain: [][]byte{{0x30}, {0x01}}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if err := validateIssuedCertificate(&tls.Certificate{Certificate: testCase.chain, Leaf: testCase.leaf}, "example.com", now); err == nil {
				t.Fatal("invalid certificate was accepted")
			}
		})
	}
}

func newBigInteger(bytes []byte) *big.Int {
	return new(big.Int).SetBytes(bytes)
}
