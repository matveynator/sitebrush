package main

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

func TestAdminIPAllowlistResolvesVerifiedAliasBeforeSessionLookup(t *testing.T) {
	application, database := newTestApplication(t)
	for _, statement := range []string{
		`INSERT INTO users(domain,email,password,is_admin) VALUES('example.org','owner@example.org','secret',1)`,
		`INSERT INTO sessions(token,user_email,created_at,client_ip,security_version) VALUES('active','example.org|owner@example.org','2026-09-25T00:00:00Z','192.0.2.1',1)`,
		`INSERT INTO domain_aliases(primary_domain,alias_domain,verification_token,is_verified,dns_a_ok) VALUES('example.org','alias.example.org','verified',1,1)`,
		`INSERT INTO admin_ip_policies(domain,email,enabled_at) VALUES('example.org','owner@example.org',1)`,
		`INSERT INTO admin_allowed_ips(domain,email,client_ip,added_at) VALUES('example.org','owner@example.org','192.0.2.1',1)`,
	} {
		if _, err := database.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	request := httptest.NewRequest(http.MethodGet, "https://alias.example.org/?profile", nil)
	request.RemoteAddr = "192.0.2.2:1234"
	request = request.WithContext(contextWithDomain(request.Context(), "alias.example.org"))
	request.AddCookie(&http.Cookie{Name: "sitebrush_session", Value: "active"})
	if !application.enforceAdminIPAllowlist(httptest.NewRecorder(), request, "alias.example.org") {
		t.Fatal("unlisted alias request stopped instead of continuing as a guest")
	}
	if _, err := request.Cookie("sitebrush_session"); err == nil {
		t.Fatal("unlisted IP retained the primary-domain administrator session through its alias")
	}
	if application.isAdminRequest(request) {
		t.Fatal("unlisted alias IP retained administrator access")
	}
}

func TestResetAdministratorIPAccessRevokesOnlySelectedAdministrator(t *testing.T) {
	_, database := newTestApplication(t)
	for _, statement := range []string{
		`INSERT INTO users(domain,email,password,is_admin) VALUES('example.org','owner@example.org','secret',1)`,
		`INSERT INTO users(domain,email,password,is_admin) VALUES('example.org','other@example.org','secret',1)`,
		`INSERT INTO sessions(token,user_email,created_at) VALUES('owner-session','example.org|owner@example.org','now')`,
		`INSERT INTO sessions(token,user_email,created_at) VALUES('other-session','example.org|other@example.org','now')`,
		`INSERT INTO account_login_codes(token,domain,email) VALUES('owner-code','example.org','owner@example.org')`,
		`INSERT INTO account_totp_challenges(token,domain,email) VALUES('owner-totp','example.org','owner@example.org')`,
		`INSERT INTO account_webauthn_challenges(token,domain,email) VALUES('owner-passkey','example.org','owner@example.org')`,
		`INSERT INTO email_confirmations(token,domain,email,current_email) VALUES('owner-confirmation','example.org','owner@example.org','owner@example.org')`,
		`INSERT INTO account_session_ips(session_token,domain,email,client_ip,used_at) VALUES('owner-session','example.org','owner@example.org','192.0.2.1',1)`,
		`INSERT INTO admin_ip_policies(domain,email,enabled_at) VALUES('example.org','owner@example.org',1)`,
		`INSERT INTO admin_allowed_ips(domain,email,client_ip,added_at) VALUES('example.org','owner@example.org','192.0.2.1',1)`,
		`INSERT INTO admin_ip_policies(domain,email,enabled_at) VALUES('example.org','other@example.org',1)`,
		`INSERT INTO admin_allowed_ips(domain,email,client_ip,added_at) VALUES('example.org','other@example.org','198.51.100.1',1)`,
	} {
		if _, err := database.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	transaction, err := database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := resetAdministratorIPAccess(context.Background(), transaction, "example.org", "owner@example.org", ""); err != nil {
		_ = transaction.Rollback()
		t.Fatal(err)
	}
	if err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}
	for table, expected := range map[string]int{
		"sessions":                    1,
		"account_login_codes":         0,
		"account_totp_challenges":     0,
		"account_webauthn_challenges": 0,
		"email_confirmations":         0,
		"account_session_ips":         0,
		"admin_ip_policies":           1,
		"admin_allowed_ips":           1,
	} {
		var actual int
		query := `SELECT COUNT(1) FROM ` + table
		if err := database.QueryRow(query).Scan(&actual); err != nil || actual != expected {
			t.Errorf("%s count=%d, expected %d: %v", table, actual, expected, err)
		}
	}
	var retainedSession string
	if err := database.QueryRow(`SELECT token FROM sessions`).Scan(&retainedSession); err != nil || retainedSession != "other-session" {
		t.Errorf("other administrator session=%q: %v", retainedSession, err)
	}
	var retainedRule string
	if err := database.QueryRow(`SELECT client_ip FROM admin_allowed_ips`).Scan(&retainedRule); err != nil || retainedRule != "198.51.100.1" {
		t.Errorf("other administrator rule=%q: %v", retainedRule, err)
	}
}

func TestResetAdministratorIPAccessCanSetCIDRAndRejectsNonAdmin(t *testing.T) {
	_, database := newTestApplication(t)
	if _, err := database.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES('example.org','owner@example.org','secret',1),('example.org','member@example.org','secret',0)`); err != nil {
		t.Fatal(err)
	}
	transaction, err := database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := resetAdministratorIPAccess(context.Background(), transaction, "example.org", "owner@example.org", "192.0.2.0/24"); err != nil {
		_ = transaction.Rollback()
		t.Fatal(err)
	}
	if err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}
	var rule string
	if err := database.QueryRow(`SELECT client_ip FROM admin_allowed_ips WHERE domain='example.org' AND email='owner@example.org'`).Scan(&rule); err != nil || rule != "192.0.2.0/24" {
		t.Fatalf("recovery rule=%q: %v", rule, err)
	}
	transaction, err = database.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := resetAdministratorIPAccess(context.Background(), transaction, "example.org", "member@example.org", ""); err == nil {
		t.Fatal("allowed resetting access for a non-administrator")
	}
	_ = transaction.Rollback()
	var policyCount int
	if err := database.QueryRow(`SELECT COUNT(1) FROM admin_ip_policies`).Scan(&policyCount); err != nil || policyCount != 1 {
		t.Fatalf("failed reset changed policy count=%d: %v", policyCount, err)
	}
}

func TestRunAdminIPResetCommandPersistsCIDR(t *testing.T) {
	storagePath := t.TempDir()
	siteDirectory := filepath.Join(storagePath, "sites")
	if err := os.MkdirAll(siteDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	databasePath := filepath.Join(siteDirectory, "example.org.db")
	database, err := sql.Open("sqlite3", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	application := &App{db: database, storagePath: storagePath}
	if err := application.migrate(context.Background()); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES('example.org','owner@example.org','secret',1)`); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	if err := runAdminIPResetCommand(context.Background(), &output, storagePath, filepath.Join(storagePath, "sitebrush.db"), "example.org", "owner@example.org", "192.0.2.17/24"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "192.0.2.0/24") {
		t.Fatalf("command output did not include normalized recovery CIDR: %q", output.String())
	}
	database, err = sql.Open("sqlite3", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var allowedRule string
	if err := database.QueryRow(`SELECT client_ip FROM admin_allowed_ips WHERE domain='example.org' AND email='owner@example.org'`).Scan(&allowedRule); err != nil || allowedRule != "192.0.2.0/24" {
		t.Fatalf("command did not persist the recovery CIDR %q: %v", allowedRule, err)
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
