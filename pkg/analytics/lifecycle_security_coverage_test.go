package analytics

import (
	"strings"
	"testing"
	"time"
)

func validAnalyticsEvent() Event {
	return Event{
		Visitor:    "0123456789abcdef",
		View:       "fedcba9876543210",
		Sequence:   1,
		Path:       "/page",
		Source:     "direct",
		ActiveMS:   10,
		Scroll:     10,
		Persistent: true,
	}
}

func TestAnalyticsValidationSecurityBoundaries(t *testing.T) {
	base := validAnalyticsEvent()
	if !Valid(base) {
		t.Fatal("valid analytics event rejected")
	}

	mutations := []func(*Event){
		func(e *Event) { e.Visitor = "short" },
		func(e *Event) { e.View = strings.Repeat("a", 65) },
		func(e *Event) { e.Sequence = 0 },
		func(e *Event) { e.Sequence = 100001 },
		func(e *Event) { e.ActiveMS = -1 },
		func(e *Event) { e.ActiveMS = 24*60*60*1000 + 1 },
		func(e *Event) { e.Scroll = -1 },
		func(e *Event) { e.Scroll = 101 },
		func(e *Event) { e.Visitor = "0123456789abcdeg" },
		func(e *Event) { e.Tab = strings.Repeat("x", 65) },
		func(e *Event) { e.Session = strings.Repeat("x", 65) },
		func(e *Event) { e.Actions = make([]Action, 17) },
		func(e *Event) { e.Language = strings.Repeat("x", 33) },
		func(e *Event) { e.PageLanguage = strings.Repeat("x", 33) },
		func(e *Event) { e.Timezone = strings.Repeat("x", 65) },
		func(e *Event) { e.URI = strings.Repeat("x", 513) },
		func(e *Event) { e.Actions = []Action{{Name: "", Count: 1}} },
		func(e *Event) { e.Actions = []Action{{Name: strings.Repeat("x", 65), Count: 1}} },
		func(e *Event) { e.Actions = []Action{{Name: "click", Target: strings.Repeat("x", 257), Count: 1}} },
		func(e *Event) { e.Actions = []Action{{Name: "click", Count: -1}} },
		func(e *Event) { e.Actions = []Action{{Name: "click", Count: 10001}} },
		func(e *Event) { e.Campaign.Source = strings.Repeat("x", 65) },
		func(e *Event) { e.Path = "relative" },
		func(e *Event) { e.Path = "/bad?query" },
		func(e *Event) { e.Source = strings.Repeat("x", 129) },
		func(e *Event) { e.Referrer = strings.Repeat("x", 257) },
	}
	for index, mutate := range mutations {
		event := base
		mutate(&event)
		if Valid(event) {
			t.Fatalf("SECURITY: invalid analytics event mutation %d was accepted: %#v", index, event)
		}
	}
}

func TestAnalyticsPruneRemovesExpiredDetailAndInitializesMaps(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	old := now.AddDate(0, 0, -RetentionDays-1)
	currentDate := dayKey(now)
	oldDate := dayKey(old)

	site := &Site{
		Started: now.AddDate(0, 0, -RetentionDays),
		Days: map[string]*Day{
			oldDate: newDay(),
			currentDate: {
				Pages: map[string]int{"ok": 1},
				Sources: map[string]int{}, Entries: map[string]int{}, Exits: map[string]int{},
				Transitions: map[string]int{}, Insights: map[string]int{},
			},
		},
		Visitors: map[string]*Visitor{
			"expired": {Last: old},
			"active": {
				Last: now,
				Days: map[string]*Observation{oldDate: {Views: 1}, currentDate: {Views: 1}},
				DirectDays: map[string]bool{oldDate: true, currentDate: true},
				Pages: map[string]*Page{
					"/old": {LastDay: oldDate},
					"/now": {LastDay: currentDate},
				},
				Views: map[string]*View{
					"old-view": {Seen: now.Add(-25 * time.Hour)},
					"new-view": {Seen: now},
				},
			},
		},
	}
	site.Prune(now)

	if _, ok := site.Days[oldDate]; ok {
		t.Fatal("expired analytics day survived prune")
	}
	if _, ok := site.Visitors["expired"]; ok {
		t.Fatal("expired visitor survived prune")
	}
	active := site.Visitors["active"]
	if _, ok := active.Days[oldDate]; ok {
		t.Fatal("expired visitor day survived prune")
	}
	if _, ok := active.DirectDays[oldDate]; ok {
		t.Fatal("expired direct day survived prune")
	}
	if _, ok := active.Pages["/old"]; ok {
		t.Fatal("expired page survived prune")
	}
	if _, ok := active.Views["old-view"]; ok {
		t.Fatal("expired view survived prune")
	}
	if site.Days[currentDate].ReturnSources == nil || site.Days[currentDate].ReturnIntervals == nil {
		t.Fatal("prune did not initialize return analytics maps")
	}

	empty := &Site{}
	empty.Prune(now)
	if empty.Visitors == nil || empty.Days == nil {
		t.Fatal("prune did not initialize empty site maps")
	}
}

func TestAnalyticsReportCoversLifecycleClassifications(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	start := dayStart(now).AddDate(0, 0, -6)
	previous := start.AddDate(0, 0, -2)
	today := dayKey(now)
	previousDay := dayKey(previous)

	site := &Site{
		Started: start.AddDate(0, 0, -14),
		Days: map[string]*Day{
			today: {
				Views: 3, Sessions: 2, SingleSessions: 1, ActiveMS: 100,
				Pages: map[string]int{"/": 3}, Sources: map[string]int{"direct": 2},
				Entries: map[string]int{"/": 2}, Exits: map[string]int{"/": 1},
				Transitions: map[string]int{}, Insights: map[string]int{},
				ReturnSources: map[string]int{"direct": 1}, ReturnIntervals: map[string]int{"1–24 hours": 1},
			},
		},
		Visitors: map[string]*Visitor{
			"new": {
				First: now.Add(-time.Hour), FirstSource: "direct", Persistent: false,
				Days: map[string]*Observation{today: {Views: 1}},
			},
			"continuing": {
				First: start.AddDate(0, 0, -10), FirstSource: "search", Persistent: true,
				Days: map[string]*Observation{previousDay: {Views: 1}, today: {Views: 1, Returning: true}},
			},
			"resurrected": {
				First: start.AddDate(0, 0, -10), FirstSource: "referral", Persistent: true,
				Days: map[string]*Observation{today: {Views: 1}},
			},
			"dormant": {
				First: start.AddDate(0, 0, -10), FirstSource: "direct", Persistent: true,
				Days: map[string]*Observation{previousDay: {Views: 1}},
			},
		},
	}
	report := site.Report(now, 7)
	if report.Visitors != 3 || report.New != 1 || report.Continuing != 1 || report.Resurrected != 1 || report.Dormant != 1 {
		t.Fatalf("lifecycle report = %#v", report)
	}
	if report.Temporary != 1 || report.Views != 3 || len(report.Retention) != 3 {
		t.Fatalf("unexpected report totals = %#v", report)
	}
}

func TestAnalyticsTargetAndAttributionSecurityBranches(t *testing.T) {
	for raw, want := range map[string]string{
		"javascript:alert(1)": "javascript:",
		"ftp://example.org/file": "ftp:",
		"https://EXAMPLE.ORG/a?x=1": "example.org/a",
		"/local/path?secret=x": "/local/path",
	} {
		if got := SafeTarget(raw); got != want {
			t.Fatalf("SafeTarget(%q)=%q want %q", raw, got, want)
		}
	}
	if SafeTarget("http://[::1") != "" {
		t.Fatal("malformed target was not rejected")
	}
	if parsedHost(nil) != "" {
		t.Fatal("nil URL returned a host")
	}

	cases := []struct {
		campaign Campaign
		referrer string
		host string
		want string
	}{
		{Campaign{Google: true}, "", "example.org", "Google Ads"},
		{Campaign{Yandex: true}, "", "example.org", "Yandex Ads"},
		{Campaign{Facebook: true}, "", "example.org", "Meta"},
		{Campaign{}, "https://chatgpt.com/share/x", "example.org", "ChatGPT"},
		{Campaign{}, "https://sub.reddit.com/r/test", "example.org", "Reddit"},
		{Campaign{}, "https://example.org/page", "example.org", "direct-hidden"},
	}
	for _, tc := range cases {
		if got := SourceAttribution(tc.campaign, tc.referrer, tc.host); got.Name != tc.want {
			t.Fatalf("attribution %#v => %#v want %q", tc, got, tc.want)
		}
	}
}
