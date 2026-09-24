package channelacme

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestACMEAtomicWriteSecurityAndOverwrite(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "nested", "private-key.pem")
	if err := atomicWrite(path, []byte("first")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("SECURITY: ACME secret permissions=%o want 600", info.Mode().Perm())
	}
	if err := atomicWrite(path, []byte("second")); err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(path)
	if err != nil || string(payload) != "second" {
		t.Fatalf("atomic overwrite=%q err=%v", payload, err)
	}

	blocker := filepath.Join(directory, "blocker")
	if err := os.WriteFile(blocker, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := atomicWrite(filepath.Join(blocker, "child"), []byte("x")); err == nil {
		t.Fatal("SECURITY: ACME secret write escaped through non-directory path")
	}
}

func TestACMEPrepareAccountReuseAndValidAuthorizationFastPath(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	state := &clientState{
		config: Config{
			HTTPClient: &http.Client{Transport: acmeRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				return acmeTestResponse(request, http.StatusOK, http.Header{"Replay-Nonce":[]string{"next"}}, `{"status":"valid"}`), nil
			})},
		},
		directory: directory{NewOrder:"https://acme.test/order"},
		accountKey: key,
		accountKID: "existing-account",
		nonce: "nonce",
	}
	if err := state.prepareAccount(context.Background()); err != nil {
		t.Fatalf("existing account was not reused: %v", err)
	}
	if err := state.authorize(context.Background(), "https://acme.test/authorization"); err != nil {
		t.Fatalf("already-valid authorization failed: %v", err)
	}
}

func TestACMEWaitForOrderAndIssueFailClosedBranches(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct{
		name string
		body string
		want string
	}{
		{"valid without certificate", `{"status":"valid"}`, "no certificate URL"},
		{"invalid order", `{"status":"invalid"}`, "became invalid"},
		{"malformed order", `{`, "unexpected end"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			state := &clientState{
				config: Config{HTTPClient:&http.Client{Transport:acmeRoundTripFunc(func(request *http.Request)(*http.Response,error){
					return acmeTestResponse(request,http.StatusOK,http.Header{"Replay-Nonce":[]string{"next"}},tc.body),nil
				})}},
				accountKey:key, accountKID:"account", nonce:"nonce",
			}
			_, err := state.waitForOrder(context.Background(),"https://acme.test/order")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("waitForOrder error=%v want containing %q", err, tc.want)
			}
		})
	}

	state := &clientState{
		config: Config{HTTPClient:&http.Client{Transport:acmeRoundTripFunc(func(request *http.Request)(*http.Response,error){
			if strings.Contains(request.URL.Path,"order") {
				return acmeTestResponse(request,http.StatusCreated,http.Header{"Replay-Nonce":[]string{"next"},"Location":[]string{"https://acme.test/order/1"}},`{"status":"pending","authorizations":[],"finalize":"https://acme.test/finalize"}`),nil
			}
			if strings.Contains(request.URL.Path,"finalize") {
				return acmeTestResponse(request,http.StatusServiceUnavailable,http.Header{"Replay-Nonce":[]string{"next"}},"unavailable"),nil
			}
			return acmeTestResponse(request,http.StatusOK,http.Header{"Replay-Nonce":[]string{"next"}},"{}"),nil
		})}, CacheDir:t.TempDir()},
		directory:directory{NewOrder:"https://acme.test/order"},
		accountKey:key, accountKID:"account", nonce:"nonce",
	}
	if _,_,err:=state.issue(context.Background(),"example.com"); err==nil || !strings.Contains(err.Error(),"503") {
		t.Fatalf("SECURITY: ACME issue ignored finalize failure: %v",err)
	}
}

func TestACMEAccountKeyFailureUnderUnsafeCachePath(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "cache")
	if err := os.WriteFile(blocker, []byte("not-directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadOrCreateAccountKey(filepath.Join(blocker, "nested")); err == nil {
		t.Fatal("SECURITY: account key creation succeeded below a file")
	}
}

func TestACMEStoreCertificateRejectsOlderCachedCertificateAndKeepsPermissions(t *testing.T) {
	cache := t.TempDir()
	now := time.Now().UTC()
	oldPEM := selfSignedTestCertificate(t, "example.com")
	if err := storeCertificate(cache, "example.com", oldPEM, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(cache, "example.com"))
	if err != nil || info.Mode().Perm()!=0o600 {
		t.Fatalf("certificate permissions=%v err=%v",info,err)
	}
	if err:=storeCertificate(cache,"example.com",[]byte("replacement"),now.Add(-time.Hour)); err==nil {
		t.Fatal("SECURITY: older certificate replacement was accepted")
	}
}
