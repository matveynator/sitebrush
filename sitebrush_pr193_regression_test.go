package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPR193PartitionedAutomaticSSLCandidatesQueryOwnersSeparately(t *testing.T) {
	application := newPartitionedTestApplication(t)

	contentDomain := "ssl-content.example"
	contentBoundary := contextWithSiteDatabaseCreation(contextWithDomain(context.Background(), contentDomain))
	if _, err := application.db.ExecContext(contentBoundary, `INSERT INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,0)`, contentDomain, "/", "Home", "content"); err != nil {
		t.Fatal(err)
	}
	seen := map[string]struct{}{}
	application.collectAutomaticSSLDomainCandidates(contentBoundary, seen)
	if _, ok := seen[contentDomain]; !ok {
		t.Fatal("automatic SSL candidate collection lost a content-owned domain")
	}
	if !application.domainIsAutomaticSSLCandidate(contentBoundary, contentDomain) {
		t.Fatal("automatic SSL admission rejected a content-owned domain")
	}

	controlDomain := "ssl-control.example"
	controlBoundary := contextWithSiteDatabaseCreation(contextWithDomain(context.Background(), controlDomain))
	if _, err := application.db.ExecContext(controlBoundary, `INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, controlDomain, "owner@example.org", "password"); err != nil {
		t.Fatal(err)
	}
	seen = map[string]struct{}{}
	application.collectAutomaticSSLDomainCandidates(controlBoundary, seen)
	if _, ok := seen[controlDomain]; !ok {
		t.Fatal("automatic SSL candidate collection lost a control-owned domain")
	}
	if !application.domainIsAutomaticSSLCandidate(controlBoundary, controlDomain) {
		t.Fatal("automatic SSL admission rejected a control-owned domain")
	}
}

func TestPR193AIIngressDecisionUsesRequestContext(t *testing.T) {
	application := &App{}
	request := httptest.NewRequest(http.MethodGet, "https://example.test/?mcp", nil)
	request = requestWithAIIngressDecision(request, true, false)
	machine, owner := application.aiIngressDecisionForRequest(request)
	if !machine || owner {
		t.Fatalf("cached ingress decision = machine %t owner %t, want true false", machine, owner)
	}
}
