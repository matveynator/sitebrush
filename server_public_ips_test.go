package main

import (
	"context"
	"strings"

	"github.com/matveynator/sitebrush/v2/pkg/serverip"
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

func TestRegistrationDNSGateUsesManualWebIPWithoutChangingMailDiscovery(t *testing.T) {
	previousExternal := lookupServerExternalIP
	previousInterfaces := lookupServerInterfaceIPs
	previousRecords := lookupIPRecords
	previousTXT := lookupTXTRecords
	t.Cleanup(func() {
		lookupServerExternalIP = previousExternal
		lookupServerInterfaceIPs = previousInterfaces
		lookupIPRecords = previousRecords
		lookupTXTRecords = previousTXT
	})
	outgoingLookups := 0
	lookupServerExternalIP = func(context.Context) (string, error) { outgoingLookups++; return "195.201.87.33", nil }
	lookupServerInterfaceIPs = func() ([]net.IP, error) { return []net.IP{net.ParseIP("195.201.87.33")}, nil }
	for _, manualIP := range []string{"46.4.4.46", "2a01:4f8:13b:4224::2"} {
		t.Run(manualIP, func(t *testing.T) {
			application := newRouterTestApplication(t)
			stop := make(chan struct{})
			defer close(stop)
			application.webPublicIPs = serverip.Start(serverip.Settings{Manual: []string{manualIP}, Preferred: manualIP}, nil, stop)
			lookupIPRecords = func(domain string) ([]net.IP, error) {
				if domain == "sender.example" {
					return []net.IP{net.ParseIP("195.201.87.33")}, nil
				}
				return []net.IP{net.ParseIP(manualIP)}, nil
			}
			lookupTXTRecords = func(string) ([]string, error) { return []string{"v=spf1 ip4:195.201.87.33 ~all"}, nil }
			before := outgoingLookups
			confirmation := EmailConfirmation{Domain: "inbound.example", LanguageCode: "en"}
			if setup, required := application.registrationConfirmationDNSSetup(context.Background(), confirmation); required {
				t.Fatalf("manual web IP rejected: %+v", setup)
			}
			if outgoingLookups != before {
				t.Fatal("registration consulted outgoing mail IP discovery")
			}
			if setup, required := application.emailDNSSetupView(context.Background(), "sender.example", "sitebrush@sender.example", "en"); required {
				t.Fatalf("outgoing mail discovery changed: %+v", setup)
			}
			if outgoingLookups != before+1 {
				t.Fatal("mail stopped using outgoing IP discovery")
			}
			lookupIPRecords = func(string) ([]net.IP, error) { return []net.IP{net.ParseIP(manualIP), net.ParseIP("1.1.1.1")}, nil }
			setup, required := application.registrationConfirmationDNSSetup(context.Background(), confirmation)
			if !required || !strings.Contains(setup.PlainText, manualIP) {
				t.Fatalf("foreign record accepted or manual hint missing: %+v required=%v", setup, required)
			}
		})
	}
}

func TestDomainSettingsAliasSuggestionsUseSelectedIPRecordType(t *testing.T) {
	for _, ip := range []string{"46.4.4.46", "2a01:4f8:13b:4224::2"} {
		t.Run(ip, func(t *testing.T) {
			application, _ := newTestApplication(t)
			request := httptest.NewRequest(http.MethodGet, "http://localhost/?settings", nil)
			request.Header.Set("Accept-Language", "en")
			response := httptest.NewRecorder()
			recordType := publicIPDNSRecordType(ip)
			application.render(response, request, "domain_settings.html", map[string]any{
				"Domain": "localhost", "SelectedDomain": "example.com", "ExternalIP": ip, "ExternalIPRecordType": recordType,
				"Aliases":    []DomainAlias{{Domain: "alias.example", DNSHostName: "alias", VerificationToken: "proof"}},
				"ReturnPath": "/", "AutomaticSSL": DomainAutomaticSSLSetting{}, "AutomaticSSLStatus": automaticSSLStatusView(DomainAutomaticSSLSetting{}, nil),
			})
			if response.Code != http.StatusOK {
				t.Fatalf("template status=%d body=%s", response.Code, response.Body.String())
			}
			expected := `<span class="dns-record-type">` + recordType + `</span>`
			if count := strings.Count(response.Body.String(), expected); count != 2 {
				t.Fatalf("want main and alias %s suggestions, got %d: %s", recordType, count, response.Body.String())
			}
		})
	}
}
