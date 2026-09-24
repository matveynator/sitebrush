package dirprotect

import (
	"strings"
	"testing"
	"time"
)

func TestSecurityBoundarySessionTokenRejectsTamperingAndRuleSubstitution(t *testing.T) {
	now := time.Unix(1_800_000_000, 0).UTC()
	rule := Rule{
		Domain:       "example.com",
		Path:         "/private",
		PasswordHash: mustHashForTest(t, "secret"),
	}
	token := BoundSessionToken(rule, "203.0.113.10", "browser", now)
	tampered := []byte(token)
	if tampered[len(tampered)-1] == '0' {
		tampered[len(tampered)-1] = '1'
	} else {
		tampered[len(tampered)-1] = '0'
	}

	cases := []struct {
		name string
		rule Rule
		ip   string
		tok  string
	}{
		{name: "signature tamper", rule: rule, ip: "203.0.113.10", tok: string(tampered)},
		{name: "other ip", rule: rule, ip: "203.0.113.11", tok: token},
		{name: "other domain", rule: Rule{Domain: "evil.example", Path: rule.Path, PasswordHash: rule.PasswordHash}, ip: "203.0.113.10", tok: token},
		{name: "other path", rule: Rule{Domain: rule.Domain, Path: "/private-other", PasswordHash: rule.PasswordHash}, ip: "203.0.113.10", tok: token},
		{name: "other password rule", rule: Rule{Domain: rule.Domain, Path: rule.Path, PasswordHash: mustHashForTest(t, "other")}, ip: "203.0.113.10", tok: token},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if BoundSessionTokenValid(tc.rule, tc.tok, tc.ip, "browser", now.Add(time.Minute), time.Hour) {
				t.Fatal("SECURITY: tampered or rebound page-password session token was accepted")
			}
		})
	}
}

func TestSecurityBoundaryProtectedPrefixCannotMatchSiblingOrTraversalAlias(t *testing.T) {
	protected := "/admin"
	for _, candidate := range []string{
		"/administrator",
		"/administer",
		"/public/admin",
	} {
		if HasProtectedPrefix(candidate, protected) {
			t.Fatalf("SECURITY: protected prefix %q matched sibling path %q", protected, candidate)
		}
	}
	if !HasProtectedPrefix("/public/../admin/settings", protected) {
		t.Fatal("SECURITY: normalized traversal alias escaped the protected /admin prefix")
	}
}

func TestSecurityBoundaryPrefixParserDoesNotCreatePasswordlessProtection(t *testing.T) {
	for _, raw := range []string{"", "   ", "/admin", "/admin\t   "} {
		rule, ok := ParsePrefixLine("example.com", raw)
		if ok && strings.TrimSpace(rule.PasswordHash) != "" {
			t.Fatalf("unexpected password hash parsed from %q", raw)
		}
	}
	if _, found := FindBestRuleInPrefixData("example.com", "/admin/page", []byte("/admin\n")); found {
		t.Fatal("SECURITY: passwordless prefix entry created a valid protection rule")
	}
}

func TestSecurityBoundarySessionTokenRejectsReplayOutsideTimeWindow(t *testing.T) {
	rule := Rule{Domain: "example.com", Path: "/private", PasswordHash: mustHashForTest(t, "secret")}
	issued := time.Unix(1_800_000_000, 0).UTC()
	token := BoundSessionToken(rule, "198.51.100.8", "", issued)

	if BoundSessionTokenValid(rule, token, "198.51.100.8", "", issued.Add(-6*time.Minute), time.Hour) {
		t.Fatal("SECURITY: token from too far in the future was accepted")
	}
	if BoundSessionTokenValid(rule, token, "198.51.100.8", "", issued.Add(2*time.Hour), time.Hour) {
		t.Fatal("SECURITY: expired page-password token was replayed successfully")
	}
}
