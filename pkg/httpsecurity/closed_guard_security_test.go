package httpsecurity

import (
	"strings"
	"testing"
	"time"
)

func TestSecurityBoundaryClosedAttackGuardFailsClosedWithoutBlocking(t *testing.T) {
	guard, err := NewAttackGuard("")
	if err != nil {
		t.Fatal(err)
	}
	guard.Close()

	now := time.Now().UTC()
	expires := now.Add(time.Hour)

	assertBusy := func(name string, err error) {
		t.Helper()
		if err == nil || !strings.Contains(err.Error(), "busy") {
			t.Fatalf("SECURITY: %s after Close returned %v, want busy error", name, err)
		}
	}

	_, err = guard.Add("203.0.113.120", "manual", "test", "manual", expires, now)
	assertBusy("Add", err)
	assertBusy("Update", guard.Update("203.0.113.120", "manual", "test"))
	assertBusy("Remove", guard.Remove("203.0.113.120"))
	_, _, err = guard.ClaimIncidentReport("203.0.113.120", "incident", now)
	assertBusy("ClaimIncidentReport", err)
	_, err = guard.FinishIncidentReport("203.0.113.120", "incident", "done", true, now)
	assertBusy("FinishIncidentReport", err)
	assertBusy("ApplyGlobal", guard.ApplyGlobal("203.0.113.120", "scanner-client", "test", now, expires))
	assertBusy("Allow", guard.Allow("203.0.113.120", "test"))
	assertBusy("Disallow", guard.Disallow("203.0.113.120"))
	assertBusy("TrustAdminIP", guard.TrustAdminIP("example.org", "203.0.113.120", now))
	if guard.IsAllowed("203.0.113.120") {
		t.Fatal("SECURITY: closed guard reported address allowlisted")
	}
	if guard.AdminIPTrusted("example.org", "203.0.113.120", now) {
		t.Fatal("SECURITY: closed guard reported admin IP trusted")
	}
	if guard.ClaimAdminIPLookup("example.org", "203.0.113.120", now) {
		t.Fatal("SECURITY: closed guard granted admin lookup claim")
	}
	if _, err := guard.Allowlist(); err == nil {
		t.Fatal("SECURITY: closed guard returned allowlist")
	}
	if _, err := guard.Settings(); err == nil {
		t.Fatal("SECURITY: closed guard returned settings")
	}
	if err := guard.SetSettings(SecuritySettings{AutoBlock: true}); err == nil {
		t.Fatal("SECURITY: closed guard accepted settings change")
	}
	if _, err := guard.Snapshot(now); err == nil {
		t.Fatal("SECURITY: closed guard returned snapshot")
	}
	if _, err := guard.persistenceSnapshot(now); err == nil {
		t.Fatal("SECURITY: closed guard returned persistence snapshot")
	}
}

func TestSecurityBoundaryAttackGuardRejectsInvalidAdminTrustIdentity(t *testing.T) {
	guard, err := NewAttackGuard("")
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()

	for _, tc := range []struct {
		domain string
		ip     string
	}{
		{"", "203.0.113.1"},
		{"example.org", "not-an-ip"},
		{" ", "203.0.113.1"},
	} {
		if err := guard.TrustAdminIP(tc.domain, tc.ip, time.Now().UTC()); err == nil {
			t.Fatalf("SECURITY: invalid admin trust identity accepted domain=%q ip=%q", tc.domain, tc.ip)
		}
	}
}
