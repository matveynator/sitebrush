package aicapability

import "testing"

func TestAccessPaginationKeepsOwnerIsolationAndStablePageContents(t *testing.T) {
	manager := NewManager()
	defer manager.Close()
	for index := 0; index < 7; index++ {
		token, _, err := manager.IssueFor("example.com", "owner@example.com", []string{ScopeRead})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := manager.Exchange(token, "example.com"); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := manager.IssueFor("example.com", "other@example.com", []string{ScopeRead}); err != nil {
		t.Fatal(err)
	}
	allLinks, total, err := manager.ListCapabilitiesPage("owner@example.com", "example.com", 0, 0)
	if err != nil || total != 7 || len(allLinks) != 7 {
		t.Fatalf("link total=%d err=%v", total, err)
	}
	allSessions, total, err := manager.ListSessionsPage("owner@example.com", "example.com", 0, 0)
	if err != nil || total != 7 || len(allSessions) != 7 {
		t.Fatalf("session total=%d err=%v", total, err)
	}
	for _, scenario := range []struct{ offset, first, length int }{{0, 0, 5}, {5, 5, 2}, {999, 5, 2}, {-1, 0, 5}} {
		links, count, listErr := manager.ListCapabilitiesPage("owner@example.com", "example.com", scenario.offset, 5)
		if listErr != nil || count != 7 || len(links) != scenario.length {
			t.Fatalf("links offset=%d count=%d err=%v", scenario.offset, count, listErr)
		}
		sessions, count, listErr := manager.ListSessionsPage("owner@example.com", "example.com", scenario.offset, 5)
		if listErr != nil || count != 7 || len(sessions) != scenario.length {
			t.Fatalf("sessions offset=%d count=%d err=%v", scenario.offset, count, listErr)
		}
		for index := range links {
			if links[index].ID != allLinks[scenario.first+index].ID || sessions[index].ID != allSessions[scenario.first+index].ID {
				t.Fatal("pagination changed ordering or returned another owner's record")
			}
		}
	}
}
