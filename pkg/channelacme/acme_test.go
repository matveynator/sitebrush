package channelacme

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	manager.challenges <- challengeRequest{action: "delete", path: challengePath}
	missingResponse := httptest.NewRecorder()
	handler.ServeHTTP(missingResponse, httptest.NewRequest(http.MethodGet, challengePath, nil))
	if missingResponse.Code != http.StatusNotFound {
		t.Fatalf("deleted challenge code=%d", missingResponse.Code)
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

func newBigInteger(bytes []byte) *big.Int {
	return new(big.Int).SetBytes(bytes)
}
