package aicapability

import (
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
)

func TestRoutedFacadeRejectsUnknownSitesAndRevokesTheOwningSession(t *testing.T) {
	first := NewManager()
	second := NewManager()
	defer first.Close()
	defer second.Close()
	resolve := func(domain string) (*Manager, error) {
		switch domain {
		case "one.example":
			return first, nil
		case "two.example":
			return second, nil
		default:
			return nil, errors.New("unregistered site")
		}
	}
	facade := NewRoutedManager(resolve, func() []string { return []string{"one.example", "two.example"} })
	if _, _, err := facade.Issue("unknown.example", []string{ScopeRead}); err == nil {
		t.Fatal("facade admitted an unregistered site")
	}
	firstToken, _, err := facade.Issue("one.example", []string{ScopeRead})
	if err != nil {
		t.Fatal(err)
	}
	secondToken, _, err := facade.Issue("two.example", []string{ScopeRead})
	if err != nil {
		t.Fatal(err)
	}
	firstSession, err := facade.Exchange(firstToken, "one.example")
	if err != nil {
		t.Fatal(err)
	}
	secondSession, err := facade.Exchange(secondToken, "two.example")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := facade.Exchange(firstToken, "two.example"); err == nil {
		t.Fatal("facade crossed the site boundary")
	}
	if err := facade.Revoke(secondToken); err != nil {
		t.Fatal(err)
	}
	select {
	case <-secondSession.Done:
	default:
		t.Fatal("routed token-only revoke left active work alive")
	}
	if _, err := facade.Lookup(firstSession.Token, "one.example"); err != nil {
		t.Fatal("another site's revoke interrupted this site", err)
	}
	if err := facade.Revoke("missing-token"); err == nil {
		t.Fatal("facade accepted an unknown token")
	}
	facade.Close()
	if _, err := first.Exchange(firstToken, "one.example"); err != nil {
		t.Fatal("facade close terminated its independently owned participant", err)
	}
}

func TestSQLSecurityReplacementRollsBackBothDocumentsAfterInsertFailure(t *testing.T) {
	database, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	database.SetMaxOpenConns(1)
	if err := InitSQL(database); err != nil {
		t.Fatal(err)
	}
	previousCapabilities := []byte(`{"grant":"revoked"}`)
	previousOAuth := []byte(`{"refresh":"used"}`)
	if err := SaveSQL(database, previousCapabilities, previousOAuth); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`CREATE TRIGGER reject_security_replacement BEFORE INSERT ON ai_security_state BEGIN SELECT RAISE(ABORT,'simulated commit failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := SaveSQL(database, []byte(`{}`), []byte(`{}`)); err == nil {
		t.Fatal("failed replacement reported success")
	}
	capabilities, oauth, err := LoadSQL(database)
	if err != nil || string(capabilities) != string(previousCapabilities) || string(oauth) != string(previousOAuth) {
		t.Fatal("partial replacement lost revocation or refresh replay state", err)
	}
}

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
