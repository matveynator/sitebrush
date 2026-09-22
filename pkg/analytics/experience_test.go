package analytics

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func experienceEvent(view, tab string) Event {
	offset := -120
	return Event{Visitor: "1111111111111111", View: view, Sequence: 1, Path: "/", Source: "Habr", Persistent: true, Attribution: Attribution{Name: "Habr", Kind: "referral", Evidence: "referrer"}, BrowserContext: BrowserContext{Tab: tab, Language: "ru-RU", PageLanguage: "en", ClientClass: "human-likely", Timezone: "Europe/Berlin", Offset: &offset}}
}
func TestExperienceJourneyCountsSessionsAndRequiresKnownFirstSource(t *testing.T) {
	report := ExperienceReport{Recent: []SessionSummary{{
		Source: Attribution{Name: "direct-hidden"}, FirstSource: "direct", Class: "human-likely",
		Tabs: map[string][]string{"first": {"/", "/docs/"}, "second": {"/", "/docs/"}},
	}}}
	filtered := report.View(ExperienceFilter{})
	if len(filtered.Journeys) != 1 || filtered.Journeys[0].Sessions != 1 {
		t.Fatalf("same session counted twice: %+v", filtered.Journeys)
	}
	for _, insight := range filtered.Insights {
		if insight.Kind == "hidden-return" {
			t.Fatal("legacy direct source treated as known acquisition source")
		}
	}
}
func TestExperienceActionsTabsGoalsAndCompletion(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	site := New(now)
	site.Goals = []Goal{{Name: "Download", Kind: "action", Match: "download"}}
	event := experienceEvent("aaaaaaaaaaaaaaaa", "tab-a")
	if !site.Record(event, now, 8<<20) {
		t.Fatal("first view rejected")
	}
	event.Sequence = 2
	event.Actions = []Action{{Name: "download", Target: "/download/", Count: 1}}
	if !site.Record(event, now.Add(time.Second), 8<<20) {
		t.Fatal("action-only update rejected")
	}
	site.Record(event, now.Add(2*time.Second), 8<<20)
	second := experienceEvent("bbbbbbbbbbbbbbbb", "tab-b")
	second.Path = "/docs/"
	site.Record(second, now.Add(3*time.Second), 8<<20)
	third := experienceEvent("cccccccccccccccc", "tab-a")
	third.Path = "/features/"
	site.Record(third, now.Add(4*time.Second), 8<<20)
	site.Prune(now.Add(time.Hour))
	report := site.ExperienceReport(now.Add(time.Hour), 1, true).View(ExperienceFilter{})
	if report.Sessions != 1 || report.Views != 3 || report.Actions != 1 || report.GoalSessions != 1 || report.NextViews != 1 || report.CompletedViews != 3 || report.EndViews != 2 {
		t.Fatalf("wrong measures: %+v", report.Measures)
	}
	if report.LocalHours[14] != 1 {
		t.Fatalf("local hours: %v", report.LocalHours)
	}
	for _, session := range report.Recent {
		if len(session.Tabs["tab-b"]) != 1 || session.Tabs["tab-b"][0] != "/docs/" {
			t.Fatalf("tabs joined: %+v", session.Tabs)
		}
	}
	// Completion is idempotent across checkpoint restoration.
	encoded, _ := json.Marshal(site)
	restored := New(now)
	if err := json.Unmarshal(encoded, restored); err != nil {
		t.Fatal(err)
	}
	restored.Prune(now.Add(2 * time.Hour))
	if got := restored.ExperienceReport(now.Add(2*time.Hour), 1, true).View(ExperienceFilter{}); got.CompletedViews != 3 {
		t.Fatalf("completion counted twice: %+v", got.Measures)
	}
}
func TestExperienceSourceEvidenceAndCleaning(t *testing.T) {
	cases := []struct {
		ref            string
		campaign       Campaign
		name, evidence string
	}{
		{"https://chatgpt.com/c/private?token=secret", Campaign{}, "ChatGPT", "referrer"},
		{"https://google.com.attacker.example/", Campaign{}, "google.com.attacker.example", "referrer"},
		{"", Campaign{Source: "telegram", Name: "launch"}, "telegram", "utm"},
		{"", Campaign{Google: true}, "Google Ads", "click-parameter"},
		{"https://site.example/page", Campaign{}, "direct-hidden", "absent"},
	}
	for _, test := range cases {
		actual := SourceAttribution(test.campaign, test.ref, "site.example")
		if actual.Name != test.name || actual.Evidence != test.evidence || strings.Contains(actual.Detail, "secret") {
			t.Fatalf("attribution %+v", actual)
		}
	}
	if target := SafeTarget("https://example.org/download?token=secret#credentials"); target != "example.org/download" {
		t.Fatal(target)
	}
	if err := ValidateGoals([]Goal{{Name: "x", Kind: "path", Match: "/?token=x"}}); err == nil {
		t.Fatal("query goal accepted")
	}
}
func TestExperienceRetentionFiltersAndArchives(t *testing.T) {
	now := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	site := New(now)
	event := experienceEvent("aaaaaaaaaaaaaaaa", "a")
	site.Record(event, now, 8<<20)
	filtered := site.ExperienceReport(now, 1, true).View(ExperienceFilter{Source: "Google"})
	if filtered.Sessions != 0 || len(filtered.Recent) != 0 || filtered.LocalHours[14] != 0 {
		t.Fatal("filter mixed populations")
	}
	site.Prune(now.AddDate(0, 0, 8))
	report := site.ExperienceReport(now.AddDate(0, 0, 8), 30, true)
	if len(report.Recent) != 0 || report.View(ExperienceFilter{}).Views != 1 {
		t.Fatal("detail retention removed aggregates")
	}
	archive := site.ExperienceReport(now.AddDate(0, 0, 8), 30, false)
	if len(archive.Recent) != 0 {
		t.Fatal("archive retains identities")
	}
}
func TestSecuritySignalsRedactionAndBounds(t *testing.T) {
	now := time.Now().UTC()
	state := &SecurityState{}
	for index, pathname := range []string{"/.git/config", "/.env", "/wp-login.php", "/normal.js", "/public.map"} {
		state.Record(RequestObservation{Time: now.Add(time.Duration(index) * time.Second), IP: "192.0.2.1", Path: pathname, Query: "token=secret", Status: 200, Agent: "curl/8"})
	}
	if len(state.Incidents) != 1 || state.Incidents[0].Count != 3 || !state.Incidents[0].PossibleExposure {
		t.Fatalf("incidents: %+v", state.Incidents)
	}
	if ProbeCategory("/article", "q=union+select+password") != "injection" {
		t.Fatal("query signal missed")
	}
	if ProbeCategory("/a/%252e%252e/etc/passwd", "") != "traversal" {
		t.Fatal("encoded traversal missed")
	}
	encoded, _ := json.Marshal(state)
	if strings.Contains(string(encoded), "token=secret") {
		t.Fatal("raw query retained")
	}
	if ClientClass("GPTBot/1") != "ai-crawler" || ClientClass("Mozilla Chrome") != "unknown" {
		t.Fatal("client classification")
	}
	state.Limit(2048)
	state.Prune(now.AddDate(0, 0, 31))
	if len(state.Incidents) != 0 {
		t.Fatal("incident IP survived retention")
	}
	if len(state.Groups) == 0 {
		t.Fatal("daily aggregates removed early")
	}
}

func TestExperienceDetailEvictionKeepsTotals(t *testing.T) {
	now := time.Now().UTC()
	site := New(now)
	for index := 0; index < 510; index++ {
		event := experienceEvent(fmt.Sprintf("%016x", index+1), "a")
		event.Visitor = fmt.Sprintf("%016x", index+1000)
		if !site.Record(event, now.Add(time.Duration(index)*time.Millisecond), 64<<20) {
			t.Fatal("observation rejected")
		}
	}
	if len(site.Experience.Recent) != 500 {
		t.Fatalf("detail count %d", len(site.Experience.Recent))
	}
	report := site.ExperienceReport(now.Add(time.Second), 1, true).View(ExperienceFilter{})
	if report.Views != 510 || report.Sessions != 510 {
		t.Fatalf("detail eviction changed totals: %+v", report.Measures)
	}
}

func TestArchiveExcludesSessionAndVisitorIdentities(t *testing.T) {
	now := time.Now().UTC()
	site := New(now)
	event := experienceEvent("aaaaaaaaaaaaaaaa", "a")
	site.Record(event, now, 8<<20)
	report := site.Report(now, 1)
	if len(report.Experience.Recent) == 0 {
		t.Fatal("fixture has no detail")
	}
	encoded, _ := json.Marshal(map[string]Report{dayKey(now): report})
	directory := t.TempDir()
	if err := writeDailyArchive(directory, string(encoded), now); err != nil {
		t.Fatal(err)
	}
	archived, err := os.Open(filepath.Join(directory, "archives", dayKey(now)+".json.gz"))
	if err != nil {
		t.Fatal(err)
	}
	defer archived.Close()
	summary, err := DecodeArchive(archived)
	if err != nil {
		t.Fatal(err)
	}
	if len(summary.Experience.Recent) != 0 || summary.Views != 1 {
		t.Fatal("archive retained details or lost totals")
	}
	sanitized, _ := json.Marshal(summary)
	if strings.Contains(string(sanitized), event.Visitor) {
		t.Fatal("visitor identity in archive")
	}
}
