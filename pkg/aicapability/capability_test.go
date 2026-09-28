package aicapability

import (
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
	manifest, err := ManifestJSON(Manifest{Protocol: "sitebrush-ai-editor/v1", Domain: "example.org", Operations: []string{"list_pages", "publish"}})
	if err != nil || len(manifest) == 0 {
		t.Fatalf("manifest=%q err=%v", manifest, err)
	}
}

func TestManifestResponseSupportsPlainTextAndNoStore(t *testing.T) {
	manifest := Manifest{Instructions: "use exchange", Protocol: "sitebrush-ai-editor/v1"}
	request := httptest.NewRequest("GET", "https://example.org", nil)
	request.Header.Set("Accept", "text/plain")
	response := httptest.NewRecorder()
	ManifestResponse(response, request, manifest)
	if response.Header().Get("Cache-Control") != "no-store" || response.Body.String() != manifest.Instructions {
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
