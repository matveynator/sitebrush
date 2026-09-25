package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAdminIPAllowlistBootstrapsOnceAndRejectsOtherAddresses(t *testing.T) {
	application, database := newTestApplication(t)
	for _, statement := range []string{
		`INSERT INTO users(domain,email,password,is_admin) VALUES('example.org','owner@example.org','secret',1)`,
		`INSERT INTO sessions(token,user_email,created_at,client_ip,security_version) VALUES('active','example.org|owner@example.org','2026-09-25T00:00:00Z','192.0.2.1',1)`,
	} {
		if _, err := database.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	firstRequest := httptest.NewRequest(http.MethodGet, "https://example.org/?profile", nil)
	firstRequest.RemoteAddr = "192.0.2.1:1234"
	firstRequest.AddCookie(&http.Cookie{Name: "sitebrush_session", Value: "active"})
	if !application.enforceAdminIPAllowlist(httptest.NewRecorder(), firstRequest, "example.org") {
		t.Fatal("bootstrap request stopped")
	}
	var allowedIP string
	if err := database.QueryRow(`SELECT client_ip FROM admin_allowed_ips WHERE domain='example.org' AND email='owner@example.org'`).Scan(&allowedIP); err != nil || allowedIP != "192.0.2.1" {
		t.Fatalf("bootstrap IP %q: %v", allowedIP, err)
	}
	secondRequest := httptest.NewRequest(http.MethodGet, "https://example.org/?profile", nil)
	secondRequest.RemoteAddr = "192.0.2.2:1234"
	secondRequest.AddCookie(&http.Cookie{Name: "sitebrush_session", Value: "active"})
	if !application.enforceAdminIPAllowlist(httptest.NewRecorder(), secondRequest, "example.org") {
		t.Fatal("guest request stopped")
	}
	if _, err := secondRequest.Cookie("sitebrush_session"); err == nil {
		t.Fatal("unlisted IP retained administrator session")
	}
	if application.isAdminRequest(secondRequest) {
		t.Fatal("unlisted IP kept administrator access")
	}
	var count int
	if err := database.QueryRow(`SELECT COUNT(1) FROM admin_allowed_ips WHERE domain='example.org'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("unlisted request changed allowlist count=%d err=%v", count, err)
	}
	if err := application.updateAdminIPAllowlist(firstRequest, "example.org", "owner@example.org", "admin_ip_add", "192.0.2.0/24"); err != nil {
		t.Fatalf("allow subnet containing the current valid session: %v", err)
	}
	subnetRequest := httptest.NewRequest(http.MethodGet, "https://example.org/?profile", nil)
	subnetRequest.RemoteAddr = "192.0.2.88:1234"
	subnetRequest.AddCookie(&http.Cookie{Name: "sitebrush_session", Value: "active"})
	if !application.enforceAdminIPAllowlist(httptest.NewRecorder(), subnetRequest, "example.org") {
		t.Fatal("subnet request stopped")
	}
	if _, err := subnetRequest.Cookie("sitebrush_session"); err != nil {
		t.Fatal("CIDR-allowed session was removed")
	}
}

func TestAdminIPAllowlistChangesOnlyUseSessionCandidates(t *testing.T) {
	application, database := newTestApplication(t)
	if _, err := database.Exec(`INSERT INTO account_session_ips(session_token,domain,email,client_ip,used_at) VALUES('old','example.org','owner@example.org','192.0.2.2',1)`); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "https://example.org/?profile", nil)
	request.RemoteAddr = "192.0.2.1:1234"
	if err := application.updateAdminIPAllowlist(request, "example.org", "owner@example.org", "admin_ip_add", "192.0.2.2"); err != nil {
		t.Fatalf("add valid session candidate: %v", err)
	}
	if err := application.updateAdminIPAllowlist(request, "example.org", "owner@example.org", "admin_ip_add", "192.0.2.3"); err == nil {
		t.Fatal("accepted IP that was never used by a valid session")
	}
	if err := application.updateAdminIPAllowlist(request, "example.org", "owner@example.org", "admin_ip_add", "198.51.100.0/24"); err == nil {
		t.Fatal("accepted subnet that contains no valid session address")
	}
	if err := application.updateAdminIPAllowlist(request, "example.org", "owner@example.org", "admin_ip_add", "192.0.2.0/24"); err != nil {
		t.Fatalf("add subnet containing a valid session address: %v", err)
	}
	if err := application.updateAdminIPAllowlist(request, "example.org", "owner@example.org", "admin_ip_remove", "192.0.2.2"); err != nil {
		t.Fatalf("remove non-current address while subnet remains: %v", err)
	}
	if err := application.updateAdminIPAllowlist(request, "example.org", "owner@example.org", "admin_ip_add", "192.0.2.1"); err != nil {
		t.Fatalf("add current IP: %v", err)
	}
	if err := application.updateAdminIPAllowlist(request, "example.org", "owner@example.org", "admin_ip_remove", "192.0.2.1"); err == nil {
		t.Fatal("removed the current administrator IP")
	}
}

func TestAdminIPAllowlistCannotRemoveLastNonCurrentAddress(t *testing.T) {
	application, database := newTestApplication(t)
	for _, allowedIP := range []string{"198.51.100.1", "203.0.113.1"} {
		if _, err := database.Exec(`INSERT INTO admin_allowed_ips(domain,email,client_ip,added_at) VALUES('example.org','owner@example.org',?,1)`, allowedIP); err != nil {
			t.Fatal(err)
		}
	}
	request := httptest.NewRequest(http.MethodPost, "https://example.org/?profile", nil)
	request.RemoteAddr = "192.0.2.1:1234"
	if err := application.updateAdminIPAllowlist(request, "example.org", "owner@example.org", "admin_ip_remove", "198.51.100.1"); err != nil {
		t.Fatalf("remove one of two non-current addresses: %v", err)
	}
	if err := application.updateAdminIPAllowlist(request, "example.org", "owner@example.org", "admin_ip_remove", "203.0.113.1"); err == nil {
		t.Fatal("removed the final administrator IP")
	}
}

func TestAdminIPAllowlistDoesNotRebootstrapAnEnabledEmptyPolicy(t *testing.T) {
	application, database := newTestApplication(t)
	for _, statement := range []string{
		`INSERT INTO users(domain,email,password,is_admin) VALUES('example.org','owner@example.org','secret',1)`,
		`INSERT INTO sessions(token,user_email,created_at,client_ip,security_version) VALUES('active','example.org|owner@example.org','2026-09-25T00:00:00Z','192.0.2.1',1)`,
		`INSERT INTO admin_ip_policies(domain,email,enabled_at) VALUES('example.org','owner@example.org',1)`,
	} {
		if _, err := database.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	request := httptest.NewRequest(http.MethodGet, "https://example.org/?profile", nil)
	request.RemoteAddr = "192.0.2.1:1234"
	request.AddCookie(&http.Cookie{Name: "sitebrush_session", Value: "active"})
	if !application.enforceAdminIPAllowlist(httptest.NewRecorder(), request, "example.org") {
		t.Fatal("request stopped instead of continuing as a guest")
	}
	if _, err := request.Cookie("sitebrush_session"); err == nil {
		t.Fatal("empty enabled policy unexpectedly bootstrapped an administrator IP")
	}
	var allowedCount int
	if err := database.QueryRow(`SELECT COUNT(1) FROM admin_allowed_ips WHERE domain='example.org'`).Scan(&allowedCount); err != nil || allowedCount != 0 {
		t.Fatalf("unexpected allowlist count=%d err=%v", allowedCount, err)
	}
}

func TestAdminIPAllowlistBlocksAccountPageForUnlistedSession(t *testing.T) {
	application, database := newTestApplication(t)
	for _, statement := range []string{
		`INSERT INTO users(domain,email,password,is_admin) VALUES('example.org','owner@example.org','secret',1)`,
		`INSERT INTO sessions(token,user_email,created_at,client_ip,security_version) VALUES('active','example.org|owner@example.org','2026-09-25T00:00:00Z','192.0.2.1',1)`,
		`INSERT INTO admin_ip_policies(domain,email,enabled_at) VALUES('example.org','owner@example.org',1)`,
		`INSERT INTO admin_allowed_ips(domain,email,client_ip,added_at) VALUES('example.org','owner@example.org','192.0.2.1',1)`,
	} {
		if _, err := database.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	request := httptest.NewRequest(http.MethodGet, "https://example.org/?profile", nil)
	request.RemoteAddr = "192.0.2.2:1234"
	request.AddCookie(&http.Cookie{Name: "sitebrush_session", Value: "active"})
	if !application.enforceAdminIPAllowlist(httptest.NewRecorder(), request, "example.org") {
		t.Fatal("direct allowlist check stopped")
	}
	if _, err := request.Cookie("sitebrush_session"); err == nil {
		t.Fatal("direct allowlist check retained the unlisted session")
	}
	request = httptest.NewRequest(http.MethodGet, "https://example.org/?profile", nil)
	request.RemoteAddr = "192.0.2.2:1234"
	request = request.WithContext(contextWithDomain(context.Background(), "example.org"))
	request.AddCookie(&http.Cookie{Name: "sitebrush_session", Value: "active"})
	response := httptest.NewRecorder()
	application.route(response, request)
	if response.Code != http.StatusFound || !strings.HasPrefix(response.Header().Get("Location"), "/?login") {
		t.Fatalf("unlisted account page response status=%d location=%q body=%q", response.Code, response.Header().Get("Location"), response.Body.String())
	}
	if strings.Contains(response.Body.String(), "Administrator access IPs") {
		t.Fatal("unlisted address received the account administration page")
	}
}

func TestCanonicalAccountIPNormalizesMappedIPv4(t *testing.T) {
	if actual := canonicalAccountIP("::ffff:192.0.2.9"); actual != "192.0.2.9" {
		t.Fatalf("mapped IPv4 canonicalized to %q", actual)
	}
	if actual := canonicalAccountIP("invalid"); actual != "" {
		t.Fatalf("invalid address canonicalized to %q", actual)
	}
	if actual, err := canonicalAdminIPRule("192.0.2.17/24"); err != nil || actual != "192.0.2.0/24" {
		t.Fatalf("IPv4 subnet canonicalized to %q: %v", actual, err)
	}
	if !adminIPRuleContains("192.0.2.0/24", "192.0.2.250") || adminIPRuleContains("192.0.2.0/24", "192.0.3.1") {
		t.Fatal("CIDR membership check accepted the wrong addresses")
	}
	if actual, err := canonicalAdminIPRule("::ffff:192.0.2.17/120"); err != nil || actual != "192.0.2.0/24" {
		t.Fatalf("mapped IPv4 subnet canonicalized to %q: %v", actual, err)
	}
	if _, err := canonicalAdminIPRule("::ffff:0:0/80"); err == nil {
		t.Fatal("accepted an ambiguous broad IPv4-mapped subnet")
	}
	if _, err := canonicalAdminIPRule("192.0.0.0/8"); err == nil {
		t.Fatal("accepted an overly broad IPv4 subnet")
	}
	if _, err := canonicalAdminIPRule("2001:db8::/32"); err == nil {
		t.Fatal("accepted an overly broad IPv6 subnet")
	}
}
