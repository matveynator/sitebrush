// Package channelacme implements the small RFC 8555 subset SiteBrush needs.
// Mutable ACME and challenge state belongs to dedicated goroutines and is
// reachable only through channels.
package channelacme

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const LetsEncryptDirectoryURL = "https://acme-v02.api.letsencrypt.org/directory"

type Config struct {
	DirectoryURL string
	CacheDir     string
	HTTPClient   *http.Client
	OnStored     func(string, *tls.Certificate, time.Time) error
}

type Manager struct {
	issues     chan issueRequest
	challenges chan challengeRequest
}

type issueRequest struct {
	ctx      context.Context
	domain   string
	response chan IssueResult
}

type IssueResult struct {
	Certificate *tls.Certificate
	ExpiresAt   time.Time
	Err         error
}

type challengeRequest struct {
	action   string
	path     string
	body     string
	response chan challengeResponse
}

type challengeResponse struct {
	body  string
	found bool
}

type directory struct {
	NewNonce   string `json:"newNonce"`
	NewAccount string `json:"newAccount"`
	NewOrder   string `json:"newOrder"`
}

type clientState struct {
	config     Config
	directory  directory
	accountKey *ecdsa.PrivateKey
	accountKID string
	nonce      string
	challenges chan challengeRequest
}

type orderDocument struct {
	Status         string   `json:"status"`
	Authorizations []string `json:"authorizations"`
	Finalize       string   `json:"finalize"`
	Certificate    string   `json:"certificate"`
}

type authorizationDocument struct {
	Status     string `json:"status"`
	Challenges []struct {
		Type   string `json:"type"`
		URL    string `json:"url"`
		Token  string `json:"token"`
		Status string `json:"status"`
		Error  any    `json:"error"`
	} `json:"challenges"`
}

type acmeResponseError struct {
	statusCode int
	retryAfter time.Time
	body       string
}

func (err acmeResponseError) Error() string {
	return fmt.Sprintf("ACME returned HTTP %d: %s", err.statusCode, strings.TrimSpace(err.body))
}

func RetryAfter(err error) (time.Time, bool) {
	var responseError acmeResponseError
	if !errors.As(err, &responseError) || responseError.retryAfter.IsZero() {
		return time.Time{}, false
	}
	return responseError.retryAfter, true
}

func Start(config Config) (*Manager, error) {
	if strings.TrimSpace(config.DirectoryURL) == "" {
		config.DirectoryURL = LetsEncryptDirectoryURL
	}
	if strings.TrimSpace(config.CacheDir) == "" {
		return nil, errors.New("ACME cache directory is required")
	}
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}
	accountKey, err := loadOrCreateAccountKey(config.CacheDir)
	if err != nil {
		return nil, err
	}
	manager := &Manager{
		issues:     make(chan issueRequest, 128),
		challenges: make(chan challengeRequest, 128),
	}
	go runChallengeStore(manager.challenges)
	go runIssuer(clientState{config: config, accountKey: accountKey, challenges: manager.challenges}, manager.issues)
	return manager, nil
}

func (manager *Manager) Issue(ctx context.Context, domain string) IssueResult {
	response := make(chan IssueResult, 1)
	request := issueRequest{ctx: ctx, domain: strings.ToLower(strings.TrimSpace(domain)), response: response}
	select {
	case manager.issues <- request:
	case <-ctx.Done():
		return IssueResult{Err: ctx.Err()}
	}
	select {
	case result := <-response:
		return result
	case <-ctx.Done():
		return IssueResult{Err: ctx.Err()}
	}
}

func (manager *Manager) HTTPHandler(fallback http.Handler) http.Handler {
	return http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		if !strings.HasPrefix(request.URL.Path, "/.well-known/acme-challenge/") {
			fallback.ServeHTTP(responseWriter, request)
			return
		}
		response := make(chan challengeResponse, 1)
		lookup := challengeRequest{action: "get", path: request.URL.Path, response: response}
		select {
		case manager.challenges <- lookup:
		case <-request.Context().Done():
			return
		}
		select {
		case challenge := <-response:
			if !challenge.found {
				http.NotFound(responseWriter, request)
				return
			}
			responseWriter.Header().Set("Content-Type", "application/octet-stream")
			_, _ = io.WriteString(responseWriter, challenge.body)
		case <-request.Context().Done():
		}
	})
}

func runChallengeStore(requests <-chan challengeRequest) {
	responsesByPath := make(map[string]string)
	for request := range requests {
		switch request.action {
		case "put":
			responsesByPath[request.path] = request.body
		case "delete":
			delete(responsesByPath, request.path)
		case "get":
			body, found := responsesByPath[request.path]
			request.response <- challengeResponse{body: body, found: found}
		}
	}
}

func runIssuer(state clientState, requests <-chan issueRequest) {
	for request := range requests {
		certificate, expiresAt, err := state.issue(request.ctx, request.domain)
		request.response <- IssueResult{Certificate: certificate, ExpiresAt: expiresAt, Err: err}
	}
}

func (state *clientState) issue(ctx context.Context, domain string) (*tls.Certificate, time.Time, error) {
	if domain == "" {
		return nil, time.Time{}, errors.New("ACME domain is required")
	}
	if err := state.prepareAccount(ctx); err != nil {
		return nil, time.Time{}, err
	}
	orderBody, orderHeader, err := state.post(ctx, state.directory.NewOrder, map[string]any{
		"identifiers": []map[string]string{{"type": "dns", "value": domain}},
	})
	if err != nil {
		return nil, time.Time{}, err
	}
	orderURL := orderHeader.Get("Location")
	var order orderDocument
	if err := json.Unmarshal(orderBody, &order); err != nil {
		return nil, time.Time{}, fmt.Errorf("decode ACME order: %w", err)
	}
	if orderURL == "" {
		return nil, time.Time{}, errors.New("ACME order location is missing")
	}
	for _, authorizationURL := range order.Authorizations {
		if err := state.authorize(ctx, authorizationURL); err != nil {
			return nil, time.Time{}, err
		}
	}
	domainKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, time.Time{}, err
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{DNSNames: []string{domain}}, domainKey)
	if err != nil {
		return nil, time.Time{}, err
	}
	if _, _, err := state.post(ctx, order.Finalize, map[string]string{"csr": base64.RawURLEncoding.EncodeToString(csrDER)}); err != nil {
		return nil, time.Time{}, err
	}
	order, err = state.waitForOrder(ctx, orderURL)
	if err != nil {
		return nil, time.Time{}, err
	}
	certificatePEM, _, err := state.post(ctx, order.Certificate, nil)
	if err != nil {
		return nil, time.Time{}, err
	}
	privateKeyDER, err := x509.MarshalECPrivateKey(domainKey)
	if err != nil {
		return nil, time.Time{}, err
	}
	privateKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: privateKeyDER})
	combinedPEM := append(append([]byte(nil), privateKeyPEM...), certificatePEM...)
	certificate, err := tls.X509KeyPair(combinedPEM, combinedPEM)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("parse issued certificate: %w", err)
	}
	leaf, err := x509.ParseCertificate(certificate.Certificate[0])
	if err != nil {
		return nil, time.Time{}, err
	}
	certificate.Leaf = leaf
	if err := validateIssuedCertificate(&certificate, domain, time.Now()); err != nil {
		return nil, time.Time{}, err
	}
	if err := storeCertificate(state.config.CacheDir, domain, combinedPEM, leaf.NotAfter); err != nil {
		return nil, time.Time{}, err
	}
	if state.config.OnStored != nil {
		if err := state.config.OnStored(domain, &certificate, leaf.NotAfter); err != nil {
			return nil, time.Time{}, err
		}
	}
	return &certificate, leaf.NotAfter, nil
}

func (state *clientState) prepareAccount(ctx context.Context) error {
	if state.directory.NewOrder == "" {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, state.config.DirectoryURL, nil)
		if err != nil {
			return err
		}
		response, err := state.config.HTTPClient.Do(request)
		if err != nil {
			return err
		}
		body, readErr := readResponse(response)
		if readErr != nil {
			return readErr
		}
		if err := json.Unmarshal(body, &state.directory); err != nil {
			return fmt.Errorf("decode ACME directory: %w", err)
		}
	}
	if state.accountKID != "" {
		return nil
	}
	body, header, err := state.post(ctx, state.directory.NewAccount, map[string]any{"termsOfServiceAgreed": true})
	if err != nil {
		return err
	}
	state.accountKID = header.Get("Location")
	if state.accountKID == "" {
		return fmt.Errorf("ACME account location is missing: %s", strings.TrimSpace(string(body)))
	}
	return nil
}

func (state *clientState) authorize(ctx context.Context, authorizationURL string) error {
	body, _, err := state.post(ctx, authorizationURL, nil)
	if err != nil {
		return err
	}
	var authorization authorizationDocument
	if err := json.Unmarshal(body, &authorization); err != nil {
		return err
	}
	if authorization.Status == "valid" {
		return nil
	}
	challengeURL := ""
	challengeToken := ""
	for _, challenge := range authorization.Challenges {
		if challenge.Type == "http-01" {
			challengeURL = challenge.URL
			challengeToken = challenge.Token
			break
		}
	}
	if challengeURL == "" || challengeToken == "" {
		return errors.New("ACME HTTP-01 challenge is unavailable")
	}
	challengePath := "/.well-known/acme-challenge/" + challengeToken
	state.challenges <- challengeRequest{action: "put", path: challengePath, body: challengeToken + "." + state.accountThumbprint()}
	defer func() { state.challenges <- challengeRequest{action: "delete", path: challengePath} }()
	if _, _, err := state.post(ctx, challengeURL, map[string]any{}); err != nil {
		return err
	}
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		if err := wait(ctx, 2*time.Second); err != nil {
			return err
		}
		body, _, err = state.post(ctx, authorizationURL, nil)
		if err != nil {
			return err
		}
		if err := json.Unmarshal(body, &authorization); err != nil {
			return err
		}
		switch authorization.Status {
		case "valid":
			return nil
		case "invalid", "deactivated", "expired", "revoked":
			return fmt.Errorf("ACME authorization became %s", authorization.Status)
		}
	}
	return errors.New("ACME authorization timed out")
}

func (state *clientState) waitForOrder(ctx context.Context, orderURL string) (orderDocument, error) {
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		body, _, err := state.post(ctx, orderURL, nil)
		if err != nil {
			return orderDocument{}, err
		}
		var order orderDocument
		if err := json.Unmarshal(body, &order); err != nil {
			return orderDocument{}, err
		}
		switch order.Status {
		case "valid":
			if order.Certificate == "" {
				return orderDocument{}, errors.New("ACME valid order has no certificate URL")
			}
			return order, nil
		case "invalid":
			return orderDocument{}, errors.New("ACME order became invalid")
		}
		if err := wait(ctx, 2*time.Second); err != nil {
			return orderDocument{}, err
		}
	}
	return orderDocument{}, errors.New("ACME order timed out")
}

func (state *clientState) post(ctx context.Context, endpoint string, payload any) ([]byte, http.Header, error) {
	if endpoint == "" {
		return nil, nil, errors.New("ACME endpoint is missing")
	}
	for attempt := 0; attempt < 3; attempt++ {
		if state.nonce == "" {
			if err := state.fetchNonce(ctx); err != nil {
				return nil, nil, err
			}
		}
		requestBody, err := state.signedBody(endpoint, payload)
		if err != nil {
			return nil, nil, err
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(requestBody))
		if err != nil {
			return nil, nil, err
		}
		request.Header.Set("Content-Type", "application/jose+json")
		response, err := state.config.HTTPClient.Do(request)
		if err != nil {
			return nil, nil, err
		}
		state.nonce = response.Header.Get("Replay-Nonce")
		body, readErr := readResponse(response)
		if readErr == nil {
			return body, response.Header, nil
		}
		if strings.Contains(string(body), "badNonce") {
			state.nonce = ""
			continue
		}
		return nil, response.Header, responseError(response, body)
	}
	return nil, nil, errors.New("ACME rejected three consecutive nonces")
}

func (state *clientState) fetchNonce(ctx context.Context) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodHead, state.directory.NewNonce, nil)
	if err != nil {
		return err
	}
	response, err := state.config.HTTPClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	state.nonce = response.Header.Get("Replay-Nonce")
	if state.nonce == "" {
		return errors.New("ACME nonce response is empty")
	}
	return nil
}

func (state *clientState) signedBody(endpoint string, payload any) (string, error) {
	payloadBytes := []byte{}
	if payload != nil {
		var err error
		payloadBytes, err = json.Marshal(payload)
		if err != nil {
			return "", err
		}
	}
	protected := map[string]any{"alg": "ES256", "nonce": state.nonce, "url": endpoint}
	if state.accountKID == "" {
		protected["jwk"] = state.accountJWK()
	} else {
		protected["kid"] = state.accountKID
	}
	protectedBytes, err := json.Marshal(protected)
	if err != nil {
		return "", err
	}
	protectedEncoded := base64.RawURLEncoding.EncodeToString(protectedBytes)
	payloadEncoded := base64.RawURLEncoding.EncodeToString(payloadBytes)
	digest := sha256.Sum256([]byte(protectedEncoded + "." + payloadEncoded))
	r, s, err := ecdsa.Sign(rand.Reader, state.accountKey, digest[:])
	if err != nil {
		return "", err
	}
	signature := append(paddedInteger(r, 32), paddedInteger(s, 32)...)
	requestDocument := map[string]string{
		"protected": protectedEncoded,
		"payload":   payloadEncoded,
		"signature": base64.RawURLEncoding.EncodeToString(signature),
	}
	requestBytes, err := json.Marshal(requestDocument)
	return string(requestBytes), err
}

func (state *clientState) accountJWK() map[string]string {
	return map[string]string{
		"crv": "P-256",
		"kty": "EC",
		"x":   base64.RawURLEncoding.EncodeToString(paddedInteger(state.accountKey.X, 32)),
		"y":   base64.RawURLEncoding.EncodeToString(paddedInteger(state.accountKey.Y, 32)),
	}
}

func (state *clientState) accountThumbprint() string {
	jwk := state.accountJWK()
	canonical := `{"crv":"P-256","kty":"EC","x":"` + jwk["x"] + `","y":"` + jwk["y"] + `"}`
	digest := sha256.Sum256([]byte(canonical))
	return base64.RawURLEncoding.EncodeToString(digest[:])
}

func paddedInteger(integer *big.Int, size int) []byte {
	result := make([]byte, size)
	integerBytes := integer.Bytes()
	copy(result[size-len(integerBytes):], integerBytes)
	return result
}

func readResponse(response *http.Response) ([]byte, error) {
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return body, responseError(response, body)
	}
	return body, nil
}

func responseError(response *http.Response, body []byte) error {
	retryAfter := time.Time{}
	if retrySeconds, err := strconv.Atoi(strings.TrimSpace(response.Header.Get("Retry-After"))); err == nil {
		retryAfter = time.Now().Add(time.Duration(retrySeconds) * time.Second)
	} else if retryTime, err := http.ParseTime(response.Header.Get("Retry-After")); err == nil {
		retryAfter = retryTime
	}
	return acmeResponseError{statusCode: response.StatusCode, retryAfter: retryAfter, body: string(body)}
}

func wait(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func loadOrCreateAccountKey(cacheDir string) (*ecdsa.PrivateKey, error) {
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return nil, err
	}
	keyPath := filepath.Join(cacheDir, "acme_account+key")
	keyPEM, err := os.ReadFile(keyPath)
	if err == nil {
		block, _ := pem.Decode(keyPEM)
		if block != nil {
			if key, parseErr := x509.ParseECPrivateKey(block.Bytes); parseErr == nil {
				return key, nil
			}
		}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, err
	}
	if err := atomicWrite(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})); err != nil {
		return nil, err
	}
	return key, nil
}

func validateIssuedCertificate(certificate *tls.Certificate, domain string, now time.Time) error {
	if certificate == nil || certificate.Leaf == nil {
		return errors.New("issued certificate leaf is missing")
	}
	if now.Before(certificate.Leaf.NotBefore) || !now.Before(certificate.Leaf.NotAfter) {
		return errors.New("issued certificate is outside its validity period")
	}
	if err := certificate.Leaf.VerifyHostname(domain); err != nil {
		return fmt.Errorf("issued certificate hostname: %w", err)
	}
	intermediates := x509.NewCertPool()
	for _, certificateDER := range certificate.Certificate[1:] {
		parsedCertificate, err := x509.ParseCertificate(certificateDER)
		if err != nil {
			return err
		}
		intermediates.AddCert(parsedCertificate)
	}
	if _, err := certificate.Leaf.Verify(x509.VerifyOptions{DNSName: domain, Intermediates: intermediates, CurrentTime: now}); err != nil {
		return fmt.Errorf("issued certificate chain: %w", err)
	}
	return nil
}

func storeCertificate(cacheDir, domain string, certificatePEM []byte, expiresAt time.Time) error {
	certificatePath := filepath.Join(cacheDir, domain)
	if existingPEM, err := os.ReadFile(certificatePath); err == nil {
		for remainingPEM := existingPEM; len(remainingPEM) > 0; {
			block, nextPEM := pem.Decode(remainingPEM)
			if block == nil {
				break
			}
			remainingPEM = nextPEM
			if block.Type != "CERTIFICATE" {
				continue
			}
			existingCertificate, parseErr := x509.ParseCertificate(block.Bytes)
			if parseErr == nil && existingCertificate.NotAfter.After(expiresAt) {
				return fmt.Errorf("issued certificate expires before cached certificate: %s", existingCertificate.NotAfter.UTC().Format(time.RFC3339))
			}
			break
		}
	}
	return atomicWrite(certificatePath, certificatePEM)
}

func atomicWrite(path string, body []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporaryFile, err := os.CreateTemp(filepath.Dir(path), ".sitebrush-acme-")
	if err != nil {
		return err
	}
	temporaryPath := temporaryFile.Name()
	defer os.Remove(temporaryPath)
	if err := temporaryFile.Chmod(0o600); err != nil {
		_ = temporaryFile.Close()
		return err
	}
	if _, err := temporaryFile.Write(body); err != nil {
		_ = temporaryFile.Close()
		return err
	}
	if err := temporaryFile.Sync(); err != nil {
		_ = temporaryFile.Close()
		return err
	}
	if err := temporaryFile.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}
