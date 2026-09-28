package aicapability

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCapabilityIsScopedAndRevocable(t *testing.T) {
	manager := NewManager()
	defer manager.Close()
	token, capability, err := manager.Issue("Example.org.", []string{ScopeRead, ScopeWrite})
	if err != nil || token == "" || capability.Domain != "example.org" {
		t.Fatalf("token=%q capability=%+v err=%v", token, capability, err)
	}
	session, err := manager.Exchange(token, "example.org")
	if err != nil || session.Token == "" {
		t.Fatalf("session=%+v err=%v", session, err)
	}
	if _, err := manager.Exchange(token, "other.example"); err == nil {
		t.Fatal("capability crossed domains")
	}
	if capability, err := manager.ValidateCapability(token, "example.org"); err != nil || capability.Domain != "example.org" {
		t.Fatalf("capability validation failed: %+v %v", capability, err)
	}
	otherToken, _, err := manager.Issue("example.org", []string{ScopeRead})
	if err != nil {
		t.Fatal(err)
	}
	otherSession, err := manager.Exchange(otherToken, "example.org")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ValidateSession(session.Token, otherToken, "example.org"); err == nil {
		t.Fatal("session crossed capability links")
	}
	if validated, err := manager.ValidateSession(session.Token, token, "example.org"); err != nil || validated.Token != session.Token {
		t.Fatalf("bound session rejected: %+v %v", validated, err)
	}
	_ = otherSession
	if err := manager.Revoke(token); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Lookup(session.Token, "example.org"); err == nil {
		t.Fatal("revoked capability kept an active session")
	}
}

func TestManifestJSONIsMachineReadable(t *testing.T) {
	manifest, err := ManifestJSON(Manifest{Protocol: "sitebrush-ai-editor/v1", Domain: "example.org", DocumentationURL: "https://example.org/ai-docs?ai_token=secret", Operations: []string{"list_pages", "publish"}})
	if err != nil || len(manifest) == 0 {
		t.Fatalf("manifest=%q err=%v", manifest, err)
	}
	if !strings.Contains(string(manifest), "\"documentation_url\"") || !strings.Contains(string(manifest), "ai_token") {
		t.Fatalf("documentation URL missing from manifest: %s", manifest)
	}
}

func TestManifestResponseSupportsPlainTextAndNoStore(t *testing.T) {
	manifest := Manifest{Instructions: "use exchange", Protocol: "sitebrush-ai-editor/v1"}
	request := httptest.NewRequest("GET", "https://example.org", nil)
	request.Header.Set("Accept", "text/plain")
	response := httptest.NewRecorder()
	ManifestResponse(response, request, manifest)
	if response.Header().Get("Cache-Control") != "no-store" || response.Header().Get("Referrer-Policy") != "no-referrer" || response.Body.String() != manifest.Instructions {
		t.Fatalf("headers=%v body=%q", response.Header(), response.Body.String())
	}
	jsonRequest := httptest.NewRequest("GET", "https://example.org", nil)
	jsonResponse := httptest.NewRecorder()
	ManifestResponse(jsonResponse, jsonRequest, manifest)
	if jsonResponse.Header().Get("Content-Type") != "application/json; charset=utf-8" {
		t.Fatal("JSON content type missing")
	}
}

func TestCapabilityRejectsUnknownRevokeAndClosedManager(t *testing.T) {
	manager := NewManager()
	if err := manager.Revoke("unknown"); err == nil {
		t.Fatal("unknown capability revoked")
	}
	manager.Close()
	if _, _, err := manager.Issue("example.org", []string{ScopeRead}); err == nil {
		t.Fatal("closed manager issued capability")
	}
}

func TestPersistentManagerIgnoresCorruptStateAndCloseIsSafeAfterStop(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "corrupt", "capabilities.json")
	if err := os.MkdirAll(filepath.Dir(statePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, []byte("not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := NewPersistentManager(statePath)
	if _, err := manager.Exchange("invalid", "example.org"); err == nil {
		t.Fatal("corrupt state created a capability")
	}
	manager.Close()
	manager.Close()
}

func TestPersistentManagerKeepsRevocableCapabilityHash(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "capabilities.json")
	firstManager := NewPersistentManager(statePath)
	token, _, err := firstManager.Issue("example.org", []string{ScopeRead})
	if err != nil {
		t.Fatal(err)
	}
	firstManager.Close()
	secondManager := NewPersistentManager(statePath)
	defer secondManager.Close()
	if _, err := secondManager.Exchange(token, "example.org"); err != nil {
		t.Fatalf("persisted capability was not restored: %v", err)
	}
	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), token) {
		t.Fatal("plaintext capability token was persisted")
	}
}

func TestOwnedSessionsCanBeRestrictedExtendedAndRevoked(t *testing.T) {
	manager := NewManager()
	defer manager.Close()
	token, capability, err := manager.IssueFor("example.org", "owner@example.org", []string{ScopeRead, ScopeWrite, ScopePublish})
	if err != nil {
		t.Fatal(err)
	}
	session, err := manager.Exchange(token, "example.org")
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := manager.List("owner@example.org", "example.org")
	if err != nil || len(sessions) != 1 || sessions[0].CapabilityID != capability.ID {
		t.Fatalf("sessions=%+v err=%v", sessions, err)
	}
	sessionID := sessions[0].ID
	if err := manager.RestrictSession(sessionID, "owner@example.org", "example.org", []string{ScopeRead}); err != nil {
		t.Fatal(err)
	}
	validated, err := manager.ValidateSession(session.Token, token, "example.org")
	if err != nil || len(validated.Scopes) != 1 || validated.Scopes[0] != ScopeRead {
		t.Fatalf("restricted session=%+v err=%v", validated, err)
	}
	if err := manager.RestrictSession(sessionID, "other@example.org", "example.org", nil); err == nil {
		t.Fatal("another owner restricted the session")
	}
	if err := manager.ExtendSession(sessionID, "owner@example.org", "example.org", time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := manager.RevokeSession(sessionID, "owner@example.org", "example.org"); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Lookup(session.Token, "example.org"); err == nil {
		t.Fatal("revoked session remained active")
	}
	if err := manager.RevokeCapabilityID(capability.ID, "owner@example.org", "example.org"); err != nil {
		t.Fatal(err)
	}
}

func TestCapabilityKeepsPageAndTaskContextOutOfTheURL(t *testing.T) {
	manager := NewManager()
	defer manager.Close()
	token, capability, err := manager.IssueForTask("example.org", "owner@example.org", []string{ScopeRead}, "/hike", "Create a photo story")
	if err != nil {
		t.Fatal(err)
	}
	if capability.PagePath != "/hike" || capability.Task != "Create a photo story" || strings.Contains(token, "hike") || strings.Contains(token, "photo") {
		t.Fatalf("unsafe capability context: %+v token=%q", capability, token)
	}
	validated, err := manager.ValidateCapability(token, "example.org")
	if err != nil || validated.PagePath != "/hike" || validated.Task != "Create a photo story" {
		t.Fatalf("context was not retained: %+v err=%v", validated, err)
	}
}


func TestCapabilityManagerRejectsInvalidSessionAdministration(t *testing.T) {
	manager := NewManager()
	defer manager.Close()

	token, capability, err := manager.IssueFor("example.org", "owner@example.org", []string{ScopeRead, ScopeWrite})
	if err != nil {
		t.Fatal(err)
	}
	session, err := manager.Exchange(token, "example.org")
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := manager.List("owner@example.org", "example.org")
	if err != nil || len(sessions) != 1 {
		t.Fatalf("sessions=%+v err=%v", sessions, err)
	}
	sessionID := sessions[0].ID

	if other, err := manager.List("other@example.org", "example.org"); err != nil || len(other) != 0 {
		t.Fatalf("other owner sessions=%+v err=%v", other, err)
	}
	if otherDomain, err := manager.List("owner@example.org", "other.example"); err != nil || len(otherDomain) != 0 {
		t.Fatalf("other domain sessions=%+v err=%v", otherDomain, err)
	}
	for _, duration := range []time.Duration{0, -time.Second, 25 * time.Hour} {
		if err := manager.ExtendSession(sessionID, "owner@example.org", "example.org", duration); err == nil {
			t.Fatalf("invalid extension %v accepted", duration)
		}
	}
	if err := manager.RestrictSession(sessionID, "owner@example.org", "example.org", []string{ScopeRead, ScopePublish}); err == nil {
		t.Fatal("session scopes were expanded")
	}
	if err := manager.RevokeSession("missing", "owner@example.org", "example.org"); err == nil {
		t.Fatal("missing session was revoked")
	}
	if err := manager.RevokeCapabilityID(capability.ID, "other@example.org", "example.org"); err == nil {
		t.Fatal("another owner revoked capability")
	}
	if _, err := manager.ValidateSession(session.Token, "", "example.org"); err == nil {
		t.Fatal("session accepted without capability binding")
	}
}

func TestCapabilityPersistenceFailsClosed(t *testing.T) {
	parentFile := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(parentFile, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := NewPersistentManager(filepath.Join(parentFile, "capabilities.json"))
	defer manager.Close()
	if token, capability, err := manager.Issue("example.org", []string{ScopeRead}); err == nil || token != "" || capability.ID != "" {
		t.Fatalf("persistent issue unexpectedly succeeded: token=%q capability=%+v err=%v", token, capability, err)
	}
}

func TestCapabilityRevokeRestoresStateWhenPersistenceFails(t *testing.T) {
	root := t.TempDir()
	statePath := filepath.Join(root, "state", "capabilities.json")
	manager := NewPersistentManager(statePath)
	defer manager.Close()

	token, capability, err := manager.IssueFor("example.org", "owner@example.org", []string{ScopeRead})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Dir(statePath), filepath.Join(root, "state-old")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Dir(statePath), []byte("block directory recreation"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := manager.Revoke(token); err == nil {
		t.Fatal("revoke succeeded even though capability state could not be persisted")
	}
	if _, err := manager.ValidateCapability(token, "example.org"); err != nil {
		t.Fatalf("failed revoke did not restore in-memory capability: %v", err)
	}
	if err := manager.RevokeCapabilityID(capability.ID, "owner@example.org", "example.org"); err == nil {
		t.Fatal("capability-id revoke succeeded even though state could not be persisted")
	}
	if _, err := manager.ValidateCapability(token, "example.org"); err != nil {
		t.Fatalf("failed capability-id revoke did not restore state: %v", err)
	}
}

func TestCapabilityHelpersAndNilManagerAreSafe(t *testing.T) {
	if normalizeDomain(" Example.ORG. ") != "example.org" {
		t.Fatal("domain normalization failed")
	}
	if !scopesAreSubset([]string{ScopeRead}, []string{ScopeRead, ScopeWrite}) {
		t.Fatal("valid scope subset rejected")
	}
	if scopesAreSubset([]string{ScopePublish}, []string{ScopeRead, ScopeWrite}) {
		t.Fatal("scope expansion accepted")
	}

	sessions := map[string]Session{
		"abcdef-one": {Domain: "example.org"},
		"abcdef-two": {Domain: "example.org"},
	}
	if _, _, found := findSession(sessions, "abcdef"); found {
		t.Fatal("ambiguous session prefix was accepted")
	}
	if _, _, found := findSession(sessions, ""); found {
		t.Fatal("empty session id was accepted")
	}
	if key, _, found := findSession(sessions, "abcdef-one"); !found || key != "abcdef-one" {
		t.Fatalf("exact session id was not found: key=%q found=%v", key, found)
	}

	var manager *Manager
	manager.Close()
	if _, _, err := manager.Issue("example.org", []string{ScopeRead}); err == nil {
		t.Fatal("nil manager issued capability")
	}
}

func TestPersistentManagerSkipsRevokedCapabilities(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "capabilities.json")
	token := "revoked-secret"
	stored := map[string]Capability{
		tokenHash(token): {ID: tokenHash(token), Domain: "example.org", Scopes: []string{ScopeRead}, Revoked: true, Created: time.Now().UTC()},
	}
	data, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	manager := NewPersistentManager(statePath)
	defer manager.Close()
	if _, err := manager.Exchange(token, "example.org"); err == nil {
		t.Fatal("revoked persisted capability was restored")
	}
}
