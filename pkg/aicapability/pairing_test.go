package aicapability

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPairingSeparatesBrowserApprovalFromOneTimeDeviceClaim(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "grants.json")
	manager := NewPersistentManager(statePath)
	pairing, err := manager.BeginPairing("Example.org.")
	if err != nil {
		t.Fatal(err)
	}
	if pairing.DeviceCode == "" || pairing.VerificationCode == "" || pairing.DeviceCode == pairing.VerificationCode || pairing.Status != "pending" || time.Until(pairing.Expires) > PairingLifetime {
		t.Fatalf("invalid pairing: %+v", pairing)
	}
	if _, err := manager.ClaimPairing("example.org", pairing.DeviceCode); !errors.Is(err, ErrPairingPending) {
		t.Fatalf("pending claim: %v", err)
	}
	if _, err := manager.ClaimPairing("example.org", pairing.DeviceCode); !errors.Is(err, ErrPairingSlowDown) {
		t.Fatalf("poll interval was ignored: %v", err)
	}
	if _, err := manager.PairingForApproval("other.example", pairing.VerificationCode); err == nil {
		t.Fatal("approval crossed sites")
	}
	if err := manager.DecidePairing("example.org", "owner@example.org", pairing.DeviceCode, true, StandardScopes()); err == nil {
		t.Fatal("device secret approved its own grant")
	}
	if err := manager.DecidePairing("example.org", "", pairing.VerificationCode, true, StandardScopes()); err == nil {
		t.Fatal("anonymous approval accepted")
	}
	if err := manager.DecidePairing("example.org", "owner@example.org", pairing.VerificationCode, true, []string{"server"}); err == nil {
		t.Fatal("server scope accepted")
	}
	if err := manager.DecidePairing("example.org", "owner@example.org", pairing.VerificationCode, true, StandardScopes()); err != nil {
		t.Fatal(err)
	}
	browserView, err := manager.PairingForApproval("example.org", pairing.VerificationCode)
	encoded, _ := json.Marshal(browserView)
	if err != nil || browserView.DeviceCode != "" || strings.Contains(string(encoded), pairing.DeviceCode) {
		t.Fatalf("browser leaked device secret: %s %v", encoded, err)
	}
	if _, err := manager.ClaimPairing("example.org", pairing.VerificationCode); err == nil {
		t.Fatal("browser identifier claimed a grant")
	}
	if _, err := manager.ClaimPairing("other.example", pairing.DeviceCode); err == nil {
		t.Fatal("device claim crossed sites")
	}
	token, err := manager.ClaimPairing("example.org", pairing.DeviceCode)
	if err != nil || token == "" {
		t.Fatalf("claim: %v", err)
	}
	if _, err := manager.ClaimPairing("example.org", pairing.DeviceCode); !errors.Is(err, ErrPairingInvalid) {
		t.Fatalf("code reused: %v", err)
	}
	capability, err := manager.ValidateCapability(token, "example.org")
	if err != nil || capability.PagePath != "" || capability.Owner != "owner@example.org" || len(capability.Scopes) != 4 {
		t.Fatalf("grant: %+v %v", capability, err)
	}
	stored, err := os.ReadFile(statePath + ".db")
	if err != nil || strings.Contains(string(stored), token) || strings.Contains(string(stored), pairing.DeviceCode) || strings.Contains(string(stored), pairing.VerificationCode) {
		t.Fatalf("persisted secrets: %s %v", stored, err)
	}
	manager.Close()
	restored := NewPersistentManager(statePath)
	defer restored.Close()
	session, err := restored.Exchange(token, "example.org")
	if err != nil {
		t.Fatal(err)
	}
	if err := restored.RevokeCapabilityID(capability.ID, "another@example.org", "example.org"); err == nil {
		t.Fatal("another owner revoked grant")
	}
	if err := restored.RevokeCapabilityID(capability.ID, "owner@example.org", "example.org"); err != nil {
		t.Fatal(err)
	}
	if _, err := restored.ValidateSession(session.Token, token, "example.org"); err == nil {
		t.Fatal("revoked grant retained session")
	}
}

func TestPairingDenialAndCapacityDoNotIssueGrants(t *testing.T) {
	manager := NewManager()
	defer manager.Close()
	pairing, err := manager.BeginPairing("example.org")
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.DecidePairing("example.org", "owner@example.org", pairing.VerificationCode, false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ClaimPairing("example.org", pairing.DeviceCode); !errors.Is(err, ErrPairingDenied) {
		t.Fatalf("denied claim: %v", err)
	}
	grants, err := manager.ListCapabilities("owner@example.org", "example.org")
	if err != nil || len(grants) != 0 {
		t.Fatalf("denial issued grants: %+v %v", grants, err)
	}
	for index := 1; index < 512; index++ {
		if _, err := manager.BeginPairing("example.org"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := manager.BeginPairing("example.org"); !errors.Is(err, ErrPairingBusy) {
		t.Fatalf("unbounded pairing queue: %v", err)
	}
}

func TestPairingExpiryAndPersistenceFailureFailClosed(t *testing.T) {
	manager := NewManager()
	defer manager.Close()
	pairings := map[string]pairingState{"expired": {Pairing: Pairing{Domain: "example.org", Expires: time.Now().Add(-time.Second), Status: "pending"}}}
	prunePairings(pairings, time.Now())
	if len(pairings) != 0 {
		t.Fatal("expired request survived")
	}
	failure := make(chan struct{})
	close(failure)
	failing := NewStoredManager(failingSaveStorage{fail: failure})
	defer failing.Close()
	pairing, err := failing.BeginPairing("example.org")
	if err != nil {
		t.Fatal(err)
	}
	if err := failing.DecidePairing("example.org", "owner@example.org", pairing.VerificationCode, true, StandardScopes()); err == nil {
		t.Fatal("approval ignored persistence failure")
	}
	if _, err := failing.ClaimPairing("example.org", pairing.DeviceCode); !errors.Is(err, ErrPairingPending) {
		t.Fatalf("failed approval released grant: %v", err)
	}
}
