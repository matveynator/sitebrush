package aicapability

import (
	"reflect"
	"testing"
)

func TestOwnershipCapabilityAndSessionRepliesCannotChangeStoredScopes(t *testing.T) {
	manager := NewManager()
	defer manager.Close()
	scopes := []string{ScopeRead}
	token, capability, err := manager.Issue("one.example", scopes)
	if err != nil {
		t.Fatal(err)
	}
	scopes[0], capability.Scopes[0] = ScopeWrite, ScopePublish
	validated, err := manager.ValidateCapability(token, "one.example")
	if err != nil || !reflect.DeepEqual(validated.Scopes, []string{ScopeRead}) {
		t.Fatalf("caller changed stored capability: %+v, %v", validated, err)
	}
	validated.Scopes[0] = ScopeAnalytics
	session, err := manager.Exchange(token, "one.example")
	if err != nil {
		t.Fatal(err)
	}
	session.Scopes[0] = ScopeWrite
	for range 3 {
		lookup, lookupErr := manager.Lookup(session.Token, "one.example")
		if lookupErr != nil || !reflect.DeepEqual(lookup.Scopes, []string{ScopeRead}) {
			t.Fatalf("caller changed stored session: %+v, %v", lookup, lookupErr)
		}
		lookup.Scopes[0] = ScopePublish
	}
}

func TestOwnershipConcurrentReplyMutationCannotEscalateAccess(t *testing.T) {
	manager := NewManager()
	defer manager.Close()
	token, _, err := manager.Issue("one.example", []string{ScopeRead})
	if err != nil {
		t.Fatal(err)
	}
	session, err := manager.Exchange(token, "one.example")
	if err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
				session.Scopes[0] = ScopePublish
			}
		}
	}()
	defer func() { close(stop); <-done }()
	for range 100 {
		lookup, lookupErr := manager.Lookup(session.Token, "one.example")
		if lookupErr != nil || !reflect.DeepEqual(lookup.Scopes, []string{ScopeRead}) {
			t.Fatalf("reply mutation escalated scopes: %+v, %v", lookup, lookupErr)
		}
	}
}

func TestOwnershipListRepliesCannotChangeAuthorizationOrEarlierReplies(t *testing.T) {
	manager := NewManager()
	defer manager.Close()
	token, _, err := manager.IssueFor("one.example", "owner@example.com", []string{ScopeRead})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Exchange(token, "one.example"); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		capabilities, listErr := manager.ListCapabilities("owner@example.com", "one.example")
		if listErr != nil || len(capabilities) != 1 || !reflect.DeepEqual(capabilities[0].Scopes, []string{ScopeRead}) {
			t.Fatalf("caller changed capability list: %+v, %v", capabilities, listErr)
		}
		sessions, listErr := manager.List("owner@example.com", "one.example")
		if listErr != nil || len(sessions) != 1 || !reflect.DeepEqual(sessions[0].Scopes, []string{ScopeRead}) {
			t.Fatalf("caller changed session list: %+v, %v", sessions, listErr)
		}
		capabilities[0].Scopes[0], sessions[0].Scopes[0] = ScopePublish, ScopeWrite
		if _, err := manager.ListCapabilities("owner@example.com", "one.example"); err != nil {
			t.Fatal(err)
		}
		if capabilities[0].Scopes[0] != ScopePublish || sessions[0].Scopes[0] != ScopeWrite {
			t.Fatal("later owner reply reused an earlier caller's snapshot")
		}
	}
}

func TestOwnershipOAuthRepliesCannotChangeStoredAuthorization(t *testing.T) {
	manager := NewManager()
	defer manager.Close()
	fixture := beginOAuthFixture(t, manager, "one.example", []string{ScopeRead})
	fixture.client.RedirectURIs[0] = "https://attacker.example/"
	client, err := manager.OAuthClient("one.example", fixture.client.ID)
	if err != nil || client.RedirectURIs[0] != fixture.authorization.RedirectURI {
		t.Fatalf("caller changed stored OAuth redirect: %+v, %v", client, err)
	}
	fixture.pairing.RequestedScopes[0] = ScopePublish
	approval, err := manager.PairingForApproval("one.example", fixture.pairing.VerificationCode)
	if err != nil || !reflect.DeepEqual(approval.RequestedScopes, []string{ScopeRead}) {
		t.Fatalf("caller changed stored OAuth scopes: %+v, %v", approval, err)
	}
	fixture = approveOAuthFixture(t, manager, "one.example", fixture, []string{ScopeRead})
	tokens := exchangeOAuthFixture(t, manager, "one.example", fixture)
	session, capability, err := manager.ValidateOAuth("one.example", fixture.authorization.Resource, tokens.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	session.Scopes[0], capability.Scopes[0] = ScopeWrite, ScopePublish
	session, capability, err = manager.ValidateOAuth("one.example", fixture.authorization.Resource, tokens.AccessToken)
	if err != nil || !reflect.DeepEqual(session.Scopes, []string{ScopeRead}) || !reflect.DeepEqual(capability.Scopes, []string{ScopeRead}) {
		t.Fatalf("OAuth reply changed stored authorization: %+v, %+v, %v", session, capability, err)
	}
}
