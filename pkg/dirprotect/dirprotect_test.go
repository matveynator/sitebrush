package dirprotect

import (
	"strings"
	"testing"
	"time"
)

func TestFindBestRuleUsesPathBoundaries(t *testing.T) {
	rules := []Rule{
		{Domain: "localhost", Path: "/passport", PasswordHash: mustHashForTest(t, "secret")},
		{Domain: "localhost", Path: "/passport/deep", PasswordHash: mustHashForTest(t, "deeper")},
	}

	rule, found := FindBestRule("localhost", "/passport/deep/page", rules)
	if !found {
		t.Fatal("rule not found")
	}
	if rule.Path != "/passport/deep" {
		t.Fatalf("rule path = %q, want %q", rule.Path, "/passport/deep")
	}

	if _, found := FindBestRule("localhost", "/passports", rules); found {
		t.Fatal("sibling path matched protected prefix")
	}
}

func TestPrefixFileRoundTrip(t *testing.T) {
	rule := Rule{Domain: "localhost", Path: "/passport", PasswordHash: mustHashForTest(t, "secret")}
	body := PrefixFileBody([]Rule{rule})
	parsedRule, found := FindBestRuleInPrefixData("localhost", "/passport/one", body)
	if !found {
		t.Fatal("prefix rule not found")
	}
	if parsedRule.Path != rule.Path || parsedRule.PasswordHash != rule.PasswordHash {
		t.Fatalf("parsed rule = %#v, want %#v", parsedRule, rule)
	}
	if !Matches(parsedRule.PasswordHash, "secret") {
		t.Fatal("password did not match parsed hash")
	}
}

func TestBoundSessionTokenRequiresSameClientConditions(t *testing.T) {
	rule := Rule{Domain: "Example.COM.", Path: "/passport", PasswordHash: mustHashForTest(t, "secret")}
	issuedAt := time.Unix(1_700_000_000, 0).UTC()
	token := BoundSessionToken(rule, "198.51.100.10", "Test Browser", issuedAt)
	if !strings.HasPrefix(token, "v2:") {
		t.Fatalf("token = %q, want v2 token", token)
	}
	if !BoundSessionTokenValid(rule, token, "198.51.100.10", "Test Browser", issuedAt.Add(time.Minute), time.Hour) {
		t.Fatal("token should be valid for the same IP and browser within TTL")
	}
	if BoundSessionTokenValid(rule, token, "198.51.100.11", "Test Browser", issuedAt.Add(time.Minute), time.Hour) {
		t.Fatal("token should not survive an IP address change")
	}
	if !BoundSessionTokenValid(rule, token, "198.51.100.10", "Other Browser", issuedAt.Add(time.Minute), time.Hour) {
		t.Fatal("token should stay valid for another browser on the same IP")
	}
}

func TestBoundSessionTokenExpiresAfterTTL(t *testing.T) {
	rule := Rule{Domain: "localhost", Path: "/passport", PasswordHash: mustHashForTest(t, "secret")}
	issuedAt := time.Unix(1_700_000_000, 0).UTC()
	token := BoundSessionToken(rule, "198.51.100.10", "Test Browser", issuedAt)
	if BoundSessionTokenValid(rule, token, "198.51.100.10", "Test Browser", issuedAt.Add(time.Hour+time.Second), time.Hour) {
		t.Fatal("token should expire after the configured TTL")
	}
}

func TestBoundSessionTokenRejectsMalformedAndFutureTokens(t *testing.T) {
	rule := Rule{Domain: "localhost", Path: "/passport", PasswordHash: "hash"}
	now := time.Unix(1_700_000_000, 0).UTC()
	for _, token := range []string{"", "v1:1:x", "v2:bad:x", "v2:1", "v2:1:x:y"} {
		if BoundSessionTokenValid(rule, token, "127.0.0.1", "", now, time.Hour) {
			t.Errorf("malformed token %q accepted", token)
		}
	}
	validToken := BoundSessionToken(rule, "127.0.0.1", "", now.Add(6*time.Minute))
	if BoundSessionTokenValid(rule, validToken, "127.0.0.1", "", now, time.Hour) {
		t.Fatal("token issued too far in the future was accepted")
	}
	validToken = BoundSessionToken(rule, "127.0.0.1", "", now)
	if BoundSessionTokenValid(rule, validToken, "127.0.0.1", "", now, 0) || BoundSessionTokenValid(rule, validToken, "127.0.0.1", "", now, -time.Second) {
		t.Fatal("token accepted with a non-positive TTL")
	}
}

func TestCookieAndFailureDomainKeysNormalizeInputs(t *testing.T) {
	if CookieName("Example.COM.", "//secure/../passport") != CookieName("example.com", "/passport") {
		t.Fatal("cookie name did not normalize domain and path")
	}
	if CookieName("example.com", "/one") == CookieName("example.com", "/two") {
		t.Fatal("different protected paths share a cookie name")
	}
	if got := FailureDomain("Example.COM.", "//secure/../passport"); got != "example.com|page-password|/passport" {
		t.Fatalf("failure domain = %q", got)
	}
	if got := FailureDomainPrefix(""); got != "localhost|page-password|" {
		t.Fatalf("empty-domain prefix = %q", got)
	}
}

func TestHashUsesPasswordHashingAlgorithm(t *testing.T) {
	password := strings.Repeat("long password ", 20)
	passwordHash := mustHashForTest(t, password)
	if strings.HasPrefix(passwordHash, "sha256:") {
		t.Fatalf("password hash uses legacy fast digest: %s", passwordHash)
	}
	if !Matches(passwordHash, password) {
		t.Fatal("password hash did not match its password")
	}
	if Matches(passwordHash, "incorrect") {
		t.Fatal("password hash matched an incorrect password")
	}
	if Matches("sha256:09e591c2a216e7264dd10d10b65a0092d7a0c5b5c9927642df8c29c10c30aeb2", "secret") {
		t.Fatal("legacy fast password digest was accepted")
	}
}

func mustHashForTest(t *testing.T, password string) string {
	t.Helper()
	passwordHash, err := Hash(password)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	return passwordHash
}


// BEGIN protected-page stealth boundary tests.

func TestBoundSessionTokenCannotCrossDomainOrProtectedPath(t *testing.T) {
	issuedAt := time.Unix(1_700_000_000, 0).UTC()
	originalRule := Rule{Domain: "example.org", Path: "/private", PasswordHash: mustHashForTest(t, "secret")}
	token := BoundSessionToken(originalRule, "198.51.100.10", "Browser", issuedAt)

	crossDomainRule := originalRule
	crossDomainRule.Domain = "alias.example.org"
	if BoundSessionTokenValid(crossDomainRule, token, "198.51.100.10", "Browser", issuedAt.Add(time.Minute), time.Hour) {
		t.Fatal("protected-page token crossed to another domain")
	}

	crossPathRule := originalRule
	crossPathRule.Path = "/another-private-area"
	if BoundSessionTokenValid(crossPathRule, token, "198.51.100.10", "Browser", issuedAt.Add(time.Minute), time.Hour) {
		t.Fatal("protected-page token crossed to another protected path")
	}
}

func TestFindBestRuleDoesNotCrossDomain(t *testing.T) {
	rules := []Rule{
		{Domain: "example.org", Path: "/private", PasswordHash: mustHashForTest(t, "one")},
		{Domain: "other.example.org", Path: "/private", PasswordHash: mustHashForTest(t, "two")},
	}

	rule, found := FindBestRule("example.org", "/private/page", rules)
	if !found {
		t.Fatal("expected protected rule was not found")
	}
	if rule.Domain != "example.org" || !Matches(rule.PasswordHash, "one") {
		t.Fatalf("wrong domain rule selected: %#v", rule)
	}
}

// END protected-page stealth boundary tests.
