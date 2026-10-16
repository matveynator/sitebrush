package aicapability

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

type oauthFixture struct {
	client        OAuthClient
	authorization OAuthAuthorization
	pairing       Pairing
	code          string
}

func beginOAuthFixture(t *testing.T, manager *Manager, domain string, scopes []string) oauthFixture {
	t.Helper()
	client, err := manager.RegisterOAuthClient(domain, OAuthClient{Name: "External AI", RedirectURIs: []string{"https://client.example/callback"}})
	if err != nil {
		t.Fatal(err)
	}
	verifier := strings.Repeat("v", 43)
	digest := sha256.Sum256([]byte(verifier))
	authorization := OAuthAuthorization{ClientID: client.ID, RedirectURI: client.RedirectURIs[0], Resource: "https://" + domain + "/_sitebrush/mcp", State: "client-state", Challenge: base64.RawURLEncoding.EncodeToString(digest[:]), Scopes: scopes}
	pairing, err := manager.BeginOAuth(domain, authorization)
	if err != nil {
		t.Fatal(err)
	}
	if pairing.DeviceCode != "" || pairing.VerificationCode == "" {
		t.Fatal("OAuth exposed device credentials or omitted approval contact")
	}
	return oauthFixture{client: client, authorization: authorization, pairing: pairing}
}

func approveOAuthFixture(t *testing.T, manager *Manager, domain string, fixture oauthFixture, scopes []string) oauthFixture {
	t.Helper()
	if err := manager.DecidePairing(domain, "owner@example.org", fixture.pairing.VerificationCode, true, scopes); err != nil {
		t.Fatal(err)
	}
	callback, err := manager.OAuthApprovalRedirect(domain, fixture.pairing.VerificationCode)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(callback)
	if err != nil {
		t.Fatal(err)
	}
	fixture.code = parsed.Query().Get("code")
	if fixture.code == "" || parsed.Query().Get("state") != "client-state" || parsed.Query().Get("iss") != "https://"+domain {
		t.Fatal("incomplete OAuth callback")
	}
	return fixture
}

func exchangeOAuthFixture(t *testing.T, manager *Manager, domain string, fixture oauthFixture) OAuthTokens {
	t.Helper()
	tokens, err := manager.ExchangeOAuthCode(domain, fixture.client.ID, fixture.authorization.Resource, fixture.authorization.RedirectURI, fixture.code, strings.Repeat("v", 43))
	if err != nil {
		t.Fatal(err)
	}
	if tokens.AccessToken == "" || tokens.RefreshToken == "" || tokens.AccessToken == tokens.RefreshToken || tokens.TokenType != "Bearer" || tokens.ExpiresIn != 900 {
		t.Fatal("invalid OAuth token response")
	}
	return tokens
}

func TestOAuthBindsPKCEClientSiteResourceAndOneTimeCode(t *testing.T) {
	manager := NewManager()
	defer manager.Close()
	fixture := approveOAuthFixture(t, manager, "one.example", beginOAuthFixture(t, manager, "one.example", StandardScopes()), StandardScopes())
	for _, changes := range []struct{ domain, client, resource, redirect, verifier string }{
		{"two.example", fixture.client.ID, fixture.authorization.Resource, fixture.authorization.RedirectURI, strings.Repeat("v", 43)},
		{"one.example", "other-client", fixture.authorization.Resource, fixture.authorization.RedirectURI, strings.Repeat("v", 43)},
		{"one.example", fixture.client.ID, "https://two.example/_sitebrush/mcp", fixture.authorization.RedirectURI, strings.Repeat("v", 43)},
		{"one.example", fixture.client.ID, fixture.authorization.Resource, "https://evil.example/callback", strings.Repeat("v", 43)},
		{"one.example", fixture.client.ID, fixture.authorization.Resource, fixture.authorization.RedirectURI, strings.Repeat("x", 43)},
	} {
		if _, err := manager.ExchangeOAuthCode(changes.domain, changes.client, changes.resource, changes.redirect, fixture.code, changes.verifier); err == nil {
			t.Fatal("unbound OAuth code accepted")
		}
	}
	tokens := exchangeOAuthFixture(t, manager, "one.example", fixture)
	if _, err := manager.ExchangeOAuthCode("one.example", fixture.client.ID, fixture.authorization.Resource, fixture.authorization.RedirectURI, fixture.code, strings.Repeat("v", 43)); err == nil {
		t.Fatal("authorization code replay accepted")
	}
	session, capability, err := manager.ValidateOAuth("one.example", fixture.authorization.Resource, tokens.AccessToken)
	if err != nil || !reflect.DeepEqual(session.Scopes, StandardScopes()) || capability.Domain != "one.example" {
		t.Fatal("OAuth did not use existing capability", err)
	}
	if _, _, err := manager.ValidateOAuth("two.example", fixture.authorization.Resource, tokens.AccessToken); err == nil {
		t.Fatal("cross-site bearer accepted")
	}
	if _, _, err := manager.ValidateOAuth("one.example", "https://one.example/other", tokens.AccessToken); err == nil {
		t.Fatal("wrong resource accepted")
	}
	legacyToken, _, err := manager.IssueFor("one.example", "owner@example.org", StandardScopes())
	if err != nil {
		t.Fatal(err)
	}
	legacySession, err := manager.Exchange(legacyToken, "one.example")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := manager.ValidateOAuth("one.example", fixture.authorization.Resource, legacySession.Token); err == nil {
		t.Fatal("legacy bearer used as an OAuth resource token")
	}
}

func TestOAuthRestrictionsAndRevocationUseExistingCapabilityOwner(t *testing.T) {
	manager := NewManager()
	defer manager.Close()
	fixture := approveOAuthFixture(t, manager, "one.example", beginOAuthFixture(t, manager, "one.example", StandardScopes()), StandardScopes())
	tokens := exchangeOAuthFixture(t, manager, "one.example", fixture)
	session, capability, err := manager.ValidateOAuth("one.example", fixture.authorization.Resource, tokens.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	listed, err := manager.List("owner@example.org", "one.example")
	if err != nil || len(listed) != 1 {
		t.Fatal("OAuth session missing from existing access management", err)
	}
	if err := manager.RestrictSession(listed[0].ID, "owner@example.org", "one.example", []string{ScopeRead}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-session.Done:
	default:
		t.Fatal("restriction did not cancel active work")
	}
	refreshed, err := manager.RefreshOAuth("one.example", fixture.client.ID, fixture.authorization.Resource, tokens.RefreshToken)
	if err != nil || refreshed.Scope != ScopeRead {
		t.Fatal("refresh widened restricted permissions", err)
	}
	active, _, err := manager.ValidateOAuth("one.example", fixture.authorization.Resource, refreshed.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.RevokeCapabilityID(capability.ID, "owner@example.org", "one.example"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-active.Done:
	default:
		t.Fatal("capability revocation did not cancel MCP work")
	}
	if _, _, err := manager.ValidateOAuth("one.example", fixture.authorization.Resource, refreshed.AccessToken); err == nil {
		t.Fatal("revoked OAuth session accepted")
	}
	if _, err := manager.RefreshOAuth("one.example", fixture.client.ID, fixture.authorization.Resource, refreshed.RefreshToken); err == nil {
		t.Fatal("revoked capability renewed through OAuth")
	}
}

func TestOAuthRefreshPersistsHashesAndDetectsReplayAfterRestart(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "security", "ai-capabilities.json")
	manager := NewPersistentManager(filename)
	fixture := approveOAuthFixture(t, manager, "one.example", beginOAuthFixture(t, manager, "one.example", StandardScopes()), StandardScopes())
	tokens := exchangeOAuthFixture(t, manager, "one.example", fixture)
	manager.Close()
	for _, name := range []string{filename, filename + ".oauth"} {
		encoded, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{tokens.AccessToken, tokens.RefreshToken, fixture.code, fixture.pairing.VerificationCode} {
			if strings.Contains(string(encoded), secret) {
				t.Fatal("OAuth credentials persisted in plaintext")
			}
		}
	}
	manager = NewPersistentManager(filename)
	defer manager.Close()
	if _, _, err := manager.ValidateOAuth("one.example", fixture.authorization.Resource, tokens.AccessToken); err == nil {
		t.Fatal("short-lived session unexpectedly persisted")
	}
	refreshed, err := manager.RefreshOAuth("one.example", fixture.client.ID, fixture.authorization.Resource, tokens.RefreshToken)
	if err != nil {
		t.Fatal("refresh did not reconnect after restart", err)
	}
	if _, err := manager.RefreshOAuth("two.example", fixture.client.ID, fixture.authorization.Resource, refreshed.RefreshToken); err == nil {
		t.Fatal("refresh crossed site boundary")
	}
	session, _, err := manager.ValidateOAuth("one.example", fixture.authorization.Resource, refreshed.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.RefreshOAuth("one.example", fixture.client.ID, fixture.authorization.Resource, tokens.RefreshToken); err == nil {
		t.Fatal("refresh replay was accepted")
	}
	select {
	case <-session.Done:
	default:
		t.Fatal("refresh replay did not revoke its token family")
	}
	if _, err := manager.RefreshOAuth("one.example", fixture.client.ID, fixture.authorization.Resource, refreshed.RefreshToken); err == nil {
		t.Fatal("replayed family remained renewable")
	}
}

func TestOAuthApprovalCannotEscalateRequestedScopesAndDenialIsTerminal(t *testing.T) {
	manager := NewManager()
	defer manager.Close()
	fixture := approveOAuthFixture(t, manager, "one.example", beginOAuthFixture(t, manager, "one.example", []string{ScopeRead}), StandardScopes())
	tokens := exchangeOAuthFixture(t, manager, "one.example", fixture)
	if tokens.Scope != ScopeRead {
		t.Fatal("approval widened client-requested scopes")
	}
	denied := beginOAuthFixture(t, manager, "one.example", StandardScopes())
	if err := manager.DecidePairing("one.example", "owner@example.org", denied.pairing.VerificationCode, false, nil); err != nil {
		t.Fatal(err)
	}
	callback, err := manager.OAuthApprovalRedirect("one.example", denied.pairing.VerificationCode)
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(callback)
	if parsed.Query().Get("error") != "access_denied" || parsed.Query().Get("code") != "" {
		t.Fatal("denial produced a grant")
	}
	if err := manager.DecidePairing("one.example", "owner@example.org", denied.pairing.VerificationCode, true, StandardScopes()); err == nil {
		t.Fatal("denied approval was reused")
	}
}

func TestOAuthRejectsInvalidRedirectsScopesAndStorage(t *testing.T) {
	manager := NewManager()
	defer manager.Close()
	for _, address := range []string{"javascript:alert(1)", "http://external.example/callback", "https://user:secret@example.org/callback", "https://example.org/callback#fragment"} {
		if _, err := manager.RegisterOAuthClient("one.example", OAuthClient{RedirectURIs: []string{address}}); err == nil {
			t.Fatal("unsafe redirect registered", address)
		}
	}
	fixture := beginOAuthFixture(t, manager, "one.example", StandardScopes())
	for _, authorization := range []OAuthAuthorization{
		{ClientID: fixture.client.ID, RedirectURI: "https://evil.example/callback", Resource: fixture.authorization.Resource, Challenge: fixture.authorization.Challenge},
		{ClientID: fixture.client.ID, RedirectURI: fixture.authorization.RedirectURI, Resource: fixture.authorization.Resource, Challenge: "invalid"},
		{ClientID: fixture.client.ID, RedirectURI: fixture.authorization.RedirectURI, Resource: fixture.authorization.Resource, Challenge: fixture.authorization.Challenge, Scopes: []string{"shell"}},
	} {
		if _, err := manager.BeginOAuth("one.example", authorization); err == nil {
			t.Fatal("invalid authorization accepted")
		}
	}
	filename := filepath.Join(t.TempDir(), "capabilities.json")
	if err := os.WriteFile(filename+".oauth", []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	invalid := NewPersistentManager(filename)
	defer invalid.Close()
	if _, err := invalid.RegisterOAuthClient("one.example", fixture.client); err == nil {
		t.Fatal("corrupt OAuth storage did not fail closed")
	}
}

func TestOAuthExpiredCodesAndPublicResponseOwnership(t *testing.T) {
	manager := NewManager()
	defer manager.Close()
	fixture := beginOAuthFixture(t, manager, "one.example", StandardScopes())
	fixture.client.RedirectURIs[0] = "https://evil.example/callback"
	stored, err := manager.OAuthClient("one.example", fixture.client.ID)
	if err != nil || stored.RedirectURIs[0] != "https://client.example/callback" {
		t.Fatal("client response retained shared metadata", err)
	}
	encoded, err := json.Marshal(fixture.pairing)
	if err != nil || strings.Contains(string(encoded), "device_code") {
		t.Fatal("browser pairing contains private transport credentials", err)
	}
	state := &oauthState{Clients: map[string]OAuthClient{"expired": {Expires: time.Now().Add(-time.Second)}, "active": {Expires: time.Now().Add(time.Hour)}}, Refresh: map[string]oauthRefresh{}, Approvals: map[string]oauthApproval{}, Codes: map[string]oauthCode{tokenHash("expired"): {Expires: time.Now().Add(-time.Second)}}}
	response := manager.handleOAuth(oauthRequest{action: "code", domain: "one.example", token: "expired"}, state, map[string]Capability{}, map[string]Session{}, map[string]chan struct{}{}, map[string]pairingState{})
	if response.err == nil || len(state.Codes) != 0 {
		t.Fatal("expired code remained usable")
	}
	if len(state.Clients) != 1 || state.Clients["active"].Expires.IsZero() {
		t.Fatal("unused registrations did not expire independently of active clients")
	}
}
