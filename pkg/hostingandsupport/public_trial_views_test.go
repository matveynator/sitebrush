package hostingandsupport

import (
	"testing"
	"time"
)

func TestGroupPublicTrialSitesClassifiesOnlyUnfinishedTrialsAsActive(t *testing.T) {
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	future := now.Add(time.Hour).Format(time.RFC3339)
	past := now.Add(-time.Hour).Format(time.RFC3339)
	sites := []PublicTrialSite{
		{ID: 1, Status: "private", DeleteAfter: future},
		{ID: 2, Status: "email_verified", DeleteAfter: past},
		{ID: 3, Status: "active", DeleteAfter: ""},
		{ID: 4, Status: "activated", DeleteAfter: past},
		{ID: 5, Status: "registered", DeleteAfter: past},
		{ID: 6, Status: "importing", DeleteAfter: past},
		{ID: 7, Status: "deleted", DeleteAfter: past},
		{ID: 8, Status: "expired", DeleteAfter: past},
		{ID: 9, Status: "expiring", DeleteAfter: past},
	}
	got := GroupPublicTrialSites(sites, now)
	if len(got.Active) != 1 || got.Active[0].ID != 1 {
		t.Fatalf("active trial rows: %+v", got.Active)
	}
	if len(got.CleanupPending) != 3 || got.CleanupPending[0].ID != 2 || got.CleanupPending[1].ID != 3 || got.CleanupPending[2].ID != 9 {
		t.Fatalf("cleanup pending rows: %+v", got.CleanupPending)
	}
	if len(got.History) != 5 || got.History[0].ID != 4 || got.History[1].ID != 5 || got.History[2].ID != 6 {
		t.Fatalf("completed trials were treated as temporary: %+v", got.History)
	}
	if len(got.Active)+len(got.CleanupPending)+len(got.History) != len(sites) {
		t.Fatalf("trial history lost: %+v", got)
	}
}
