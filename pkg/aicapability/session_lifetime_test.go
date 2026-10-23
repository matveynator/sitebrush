package aicapability

import (
	"testing"
	"time"
)

func TestSessionContactClosesOnRestrictionRevocationAndShutdown(t *testing.T) {
	manager := NewManager()
	token, capability, err := manager.IssueFor("example.org", "owner@example.org", StandardScopes())
	if err != nil {
		t.Fatal(err)
	}
	first, err := manager.Exchange(token, "example.org")
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Exchange(token, "example.org")
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.RestrictSession(tokenHash(first.Token), "owner@example.org", "example.org", []string{ScopeRead}); err != nil {
		t.Fatal(err)
	}
	for _, session := range []Session{first, second} {
		select {
		case <-session.Done:
		default:
			t.Fatal("old session work retained its contact after restriction")
		}
	}
	narrowed, err := manager.ValidateSession(first.Token, token, "example.org")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-narrowed.Done:
		t.Fatal("narrowed contact is closed")
	default:
	}
	if len(narrowed.Scopes) != 1 || narrowed.Scopes[0] != ScopeRead {
		t.Fatal("restriction lost")
	}
	if err := manager.RevokeCapabilityID(capability.ID, "owner@example.org", "example.org"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-narrowed.Done:
	default:
		t.Fatal("revoked contact remained open")
	}
	newToken, _, err := manager.IssueFor("example.org", "owner@example.org", StandardScopes())
	if err != nil {
		t.Fatal(err)
	}
	last, err := manager.Exchange(newToken, "example.org")
	if err != nil {
		t.Fatal(err)
	}
	manager.Close()
	select {
	case <-last.Done:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not cancel work")
	}
}

func TestExpiredSessionContactIsClosedByOwner(t *testing.T) {
	now := time.Now()
	stop := make(chan struct{})
	sessions := map[string]Session{"expired": {Done: stop, Expires: now}}
	stops := map[string]chan struct{}{"expired": stop}
	pruneSessions(sessions, stops, now)
	select {
	case <-stop:
	default:
		t.Fatal("expired contact remained open")
	}
	if len(sessions) != 0 || len(stops) != 0 {
		t.Fatal("expired session retained")
	}
}

func TestPublicDemoSessionResetIsDomainBound(t *testing.T) {
	manager := NewManager()
	defer manager.Close()
	demo, _, created, err := manager.PublicDemoSession("demo.example", "demo@example.org", time.Minute)
	if err != nil || !created {
		t.Fatal(err)
	}
	same, _, created, err := manager.PublicDemoSession("demo.example", "demo@example.org", time.Minute)
	if err != nil || created || same.Done != demo.Done {
		t.Fatal("demo request created another grant", err)
	}
	token, _, err := manager.Issue("ordinary.example", StandardScopes())
	if err != nil {
		t.Fatal(err)
	}
	ordinary, err := manager.Exchange(token, "ordinary.example")
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.RevokeDomain("demo.example"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-demo.Done:
	default:
		t.Fatal("reset did not cancel demo jobs")
	}
	select {
	case <-ordinary.Done:
		t.Fatal("demo reset revoked another site")
	default:
	}
	next, _, created, err := manager.PublicDemoSession("demo.example", "demo@example.org", time.Minute)
	if err != nil || !created || next.CapabilityID == demo.CapabilityID {
		t.Fatal("reset reused an obsolete demo grant", err)
	}
}
