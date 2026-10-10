package httpsecurity

import (
	"testing"
	"time"
)

func TestBlockEvidenceDistinguishesInitialRepeatAndAllowlistedRequests(t *testing.T) {
	guard, err := NewAttackGuard("")
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	_, blocked, repeat := guard.ObserveSiteIncidentEvidence("site.example", "192.0.2.1", "repository", "/.git/config", now)
	if !blocked || repeat {
		t.Fatalf("first block: blocked=%v repeat=%v", blocked, repeat)
	}
	_, blocked, allowed, repeat := guard.ObserveSiteRequestFastEvidence("site.example", "192.0.2.1", "/", "GET", false, now.Add(time.Second))
	if !blocked || allowed || !repeat {
		t.Fatalf("repeat request: blocked=%v allowed=%v repeat=%v", blocked, allowed, repeat)
	}
	_, blocked, repeat = guard.ObserveSiteIncidentEvidence("site.example", "192.0.2.1", "repository", "/.git/config", now.Add(2*time.Second))
	if !blocked || !repeat {
		t.Fatal("repeated attack lost prior-block evidence")
	}
	if err := guard.Allow("192.0.2.1", "trusted"); err != nil {
		t.Fatal(err)
	}
	_, blocked, allowed, repeat = guard.ObserveSiteRequestFastEvidence("site.example", "192.0.2.1", "/", "GET", false, now.Add(3*time.Second))
	if blocked || !allowed || repeat {
		t.Fatalf("allowlist: blocked=%v allowed=%v repeat=%v", blocked, allowed, repeat)
	}
}
