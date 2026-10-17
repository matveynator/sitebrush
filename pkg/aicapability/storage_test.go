package aicapability

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestCorruptReloadStopsActiveWorkAndRecoveryDoesNotReviveSessions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "control")
	manager := NewPersistentManager(path)
	defer manager.Close()
	token, _, err := manager.Issue("example.org", []string{ScopeRead})
	if err != nil {
		t.Fatal(err)
	}
	session, err := manager.Exchange(token, "example.org")
	if err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", path+".db")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	capabilities, oauth, err := LoadSQL(database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE ai_security_state SET capabilities='null' WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if err := manager.Reload(); err == nil {
		t.Fatal("corrupt security state accepted")
	}
	select {
	case <-session.Done:
	default:
		t.Fatal("active work survived a failed security reload")
	}
	if _, err := manager.Exchange(token, "example.org"); err == nil {
		t.Fatal("corrupt control state issued access")
	}
	if err := SaveSQL(database, capabilities, oauth); err != nil {
		t.Fatal(err)
	}
	if err := manager.Reload(); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Lookup(session.Token, "example.org"); err == nil {
		t.Fatal("recovery revived an old bearer")
	}
	if _, err := manager.Exchange(token, "example.org"); err != nil {
		t.Fatal("valid durable grant lost after recovery", err)
	}
}

func TestLegacyImportRejectsBrokenBindingsBeforeReturningAnySite(t *testing.T) {
	capabilityID := tokenHash("legacy-capability")
	validCapabilities := map[string]Capability{capabilityID: {ID: capabilityID, Domain: "example.org", Scopes: []string{ScopeRead}, Revoked: true}}
	encode := func(document any) []byte {
		t.Helper()
		encoded, err := json.Marshal(document)
		if err != nil {
			t.Fatal(err)
		}
		return encoded
	}
	capabilities := encode(validCapabilities)
	for _, test := range []struct {
		name                string
		capabilities, oauth []byte
	}{
		{"malformed capabilities", []byte("{"), nil},
		{"null capabilities", []byte("null"), nil},
		{"null OAuth", capabilities, []byte("null")},
		{"malformed OAuth", capabilities, []byte("{")},
		{"raw credential", encode(map[string]Capability{"secret": {ID: "secret", Domain: "example.org"}}), nil},
		{"wrong capability ID", encode(map[string]Capability{capabilityID: {ID: tokenHash("another"), Domain: "example.org"}}), nil},
		{"noncanonical site", encode(map[string]Capability{capabilityID: {ID: capabilityID, Domain: "EXAMPLE.ORG"}}), nil},
		{"wrong client binding", capabilities, encode(oauthState{Clients: map[string]OAuthClient{"wrong": {ID: "client", Domain: "example.org"}}, Refresh: map[string]oauthRefresh{}})},
		{"raw refresh", capabilities, encode(oauthState{Clients: map[string]OAuthClient{}, Refresh: map[string]oauthRefresh{"secret": {Domain: "example.org", CapabilityID: capabilityID}}})},
		{"cross-site refresh", capabilities, encode(oauthState{Clients: map[string]OAuthClient{}, Refresh: map[string]oauthRefresh{tokenHash("refresh"): {Domain: "other.example", CapabilityID: capabilityID}}})},
	} {
		t.Run(test.name, func(t *testing.T) {
			if sites, err := SplitLegacy(test.capabilities, test.oauth); err == nil || sites != nil {
				t.Fatal("invalid source returned a partial import", err)
			}
		})
	}
	sites, err := SplitLegacy(capabilities, nil)
	if err != nil {
		t.Fatal(err)
	}
	var imported map[string]Capability
	if err := json.Unmarshal(sites["example.org"][0], &imported); err != nil || !imported[capabilityID].Revoked {
		t.Fatal("legacy import lost revocation", err)
	}
}
