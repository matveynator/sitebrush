package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPublicTrialWildcardAcceptsServerAliasAndRejectsForeignRecord(t *testing.T) {
	previous := lookupIPRecords
	t.Cleanup(func() { lookupIPRecords = previous })
	addresses := []net.IP{net.ParseIP("195.201.87.33"), net.ParseIP("46.4.4.46"), net.ParseIP("2a01:4f8:13b:4224::2")}
	lookupIPRecords = func(string) ([]net.IP, error) { return []net.IP{net.ParseIP("46.4.4.46")}, nil }
	if result := checkPublicTrialWildcardAddresses("sitebrush.com", addresses); !result.Enabled {
		t.Fatalf("alias rejected: %+v", result)
	}
	lookupIPRecords = func(string) ([]net.IP, error) { return []net.IP{net.ParseIP("46.4.4.46"), net.ParseIP("1.1.1.1")}, nil }
	if result := checkPublicTrialWildcardAddresses("sitebrush.com", addresses); result.Enabled || result.ReasonKey != "public_trial_disabled_wildcard_mismatch" {
		t.Fatalf("foreign record accepted: %+v", result)
	}
}

func TestPublicIPSettingsRejectsUnauthenticatedMutation(t *testing.T) {
	application := newRouterTestApplication(t)
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		request := httptest.NewRequest(method, "https://sitebrush.example/?public_ip_settings_ws", nil)
		request.Header.Set("Origin", "https://attacker.example")
		response := httptest.NewRecorder()
		application.publicIPSettingsWebSocket(response, request)
		if response.Code != http.StatusForbidden {
			t.Fatalf("%s status=%d", method, response.Code)
		}
	}
}

func TestPublicIPDNSHintType(t *testing.T) {
	if publicIPDNSRecordType("46.4.4.46") != "A" || publicIPDNSRecordType("2a01:4f8:13b:4224::2") != "AAAA" {
		t.Fatal("wrong DNS record type")
	}
}

func TestPublicIPSettingsRejectsOwnerCrossOriginAndForeignDomain(t *testing.T) {
	application, _ := newUpdateTestApplication(t)
	cookie := newAdminSessionCookie(t, application, "owner@example.com")
	for _, scenario := range []struct{ host, origin string }{{"localhost", "http://attacker.example"}, {"foreign.example", "http://foreign.example"}} {
		request := httptest.NewRequest(http.MethodGet, "http://"+scenario.host+"/?public_ip_settings_ws", nil)
		request.AddCookie(cookie)
		request = request.WithContext(contextWithDomain(request.Context(), scenario.host))
		request.Header.Set("Origin", scenario.origin)
		response := httptest.NewRecorder()
		application.publicIPSettingsWebSocket(response, request)
		if response.Code != http.StatusForbidden {
			t.Fatalf("host=%s origin=%s status=%d", scenario.host, scenario.origin, response.Code)
		}
	}
}
