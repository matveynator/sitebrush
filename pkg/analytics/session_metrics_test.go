package analytics

import (
	"encoding/json"
	"testing"
	"time"
)

func TestSessionGoalAttributionSurvivesCheckpointAndRepeatedUpdates(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	site := New(now)
	site.Goals = []Goal{{Name: "Download page", Kind: "path", Match: "/download/"}, {Name: "Download", Kind: "action", Match: "download"}, {Name: "Registration", Kind: "path", Match: "/register/"}}
	event := experienceEvent("2222222222222222", "tab")
	event.Referrer = "https://example-game.com/article?id=42&token=private"
	event.Attribution = SourceWithAgent(Campaign{}, event.Referrer, "site.example", "Mozilla/5.0")
	event.Source = event.Attribution.Name
	if !site.Record(event, now, 16<<20) {
		t.Fatal("landing rejected")
	}
	event.View = "3333333333333333"
	event.Path = "/download/"
	event.URI = event.Path
	event.Referrer = "https://site.example/"
	event.Attribution = SourceAttribution(Campaign{}, event.Referrer, "site.example")
	event.Source = event.Attribution.Name
	event.Actions = []Action{{Name: "download", Target: "/file.zip", Count: 2}}
	if !site.Record(event, now.Add(time.Second), 16<<20) {
		t.Fatal("download rejected")
	}
	checkpoint, err := json.Marshal(site)
	if err != nil {
		t.Fatal(err)
	}
	var restored Site
	if err := json.Unmarshal(checkpoint, &restored); err != nil {
		t.Fatal(err)
	}
	event.Sequence++
	event.ActiveMS = 1000
	restored.Record(event, now.Add(2*time.Second), 16<<20)
	view := restored.ExperienceReport(now.Add(3*time.Second), 1, true).View(ExperienceFilter{})
	if view.Sessions != 1 || view.Views != 2 || view.GoalSessions != 1 || len(view.Sources) != 1 {
		t.Fatalf("session metrics: %+v", view)
	}
	session := view.Recent[0]
	if session.Source.Name != "example-game.com" || session.Referrer != "https://example-game.com/article?id=42" || session.GoalCounts["Download"] != 2 || session.GoalCounts["Download page"] != 1 || session.GoalCounts["Registration"] != 0 {
		t.Fatalf("session attribution/goals: %+v", session)
	}
	if view.Sources[0].GoalCounts["Download"] != 2 || len(view.GoalRows) != 2 {
		t.Fatalf("goal aggregates: %+v", view)
	}
}

func TestPresentReferrerCannotBecomeDirect(t *testing.T) {
	for _, referrer := range []string{"https://site.example/page", "android-app://com.example.app/", "invalid-referrer"} {
		if source := SourceWithAgent(Campaign{}, referrer, "site.example", "Instagram"); source.Kind == "direct" {
			t.Fatalf("referrer lost: %q", referrer)
		}
	}
	if source := SourceWithAgent(Campaign{}, "", "site.example", "Instagram"); source.Name != "Instagram" || source.Evidence != "user-agent" {
		t.Fatalf("app evidence: %+v", source)
	}
}
