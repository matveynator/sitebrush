package analytics

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func conversionEvent(visitor, view, path string, sequence int, signals ...Signal) Event {
	return Event{Version: 2, Visitor: visitor, View: view, Sequence: sequence, Path: path, BrowserContext: BrowserContext{URI: path, Tab: "tab", Session: "session"}, Layout: "abcd", Device: "desktop", Persistent: true, Attribution: Attribution{Name: "GitHub", Kind: "referral"}, Signals: signals}
}
func TestConversionFunnelAcrossSessionsDeduplicationAndVersions(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	site := New(now)
	site.Goals = []Goal{{Name: "Purchase", Primary: true, Steps: []Goal{{Name: "Product", Kind: "page", Page: "/product"}, {Name: "Checkout", Kind: "page", Page: "/checkout"}, {Name: "Purchase", Kind: "event", Match: "purchase"}}}}
	first := conversionEvent("1111111111111111", "2222222222222222", "/product", 1, Signal{Kind: "page"})
	if !site.Record(first, now, 4<<20) || site.Record(first, now, 4<<20) {
		t.Fatal("duplicate batch admitted")
	}
	second := conversionEvent(first.Visitor, "3333333333333333", "/checkout", 1, Signal{Kind: "page"}, Signal{Kind: "goal", Name: "purchase", At: 1000})
	second.Session = "returned"
	second.Attribution = Attribution{Name: "direct", Kind: "direct"}
	if !site.Record(second, now.AddDate(0, 0, 30), 4<<20) {
		t.Fatal("return rejected")
	}
	if len(site.Conversion.HourlyConversions) != 1 || site.Conversion.HourlyConversions[now.AddDate(0, 0, 30).Format("2006-01-02T15")] != 1 {
		t.Fatal("hourly aggregates counted actions instead of goal completions")
	}
	report := site.Report(now.AddDate(0, 0, 30), 90).Conversion
	if len(report.Funnels) != 1 || report.Funnels[0].Completed != 1 || len(report.Funnels[0].Steps) != 4 {
		t.Fatalf("funnel %#v", report.Funnels)
	}
	if report.Sources[0].Name != "GitHub" || report.Sources[0].Rate != "100.0%" {
		t.Fatalf("source %#v", report.Sources)
	}
	encoded, _ := json.Marshal(site)
	restored := New(now)
	if json.Unmarshal(encoded, restored) != nil {
		t.Fatal("restore failed")
	}
	restored.Prune(now.AddDate(0, 0, 30))
	if restored.Record(second, now.AddDate(0, 0, 30), 4<<20) {
		t.Fatal("checkpoint lost dedup gate")
	}
	oldKey := GoalKey(site.Goals[0])
	site.Goals[0].Steps[1].Page = "/new-checkout"
	report = site.Report(now.AddDate(0, 0, 30), 90).Conversion
	if len(report.Funnels) != 2 {
		t.Fatal("old version disappeared")
	}
	for _, funnel := range report.Funnels {
		if funnel.Key == oldKey && funnel.Completed != 1 {
			t.Fatal("old outcomes rewritten")
		}
	}
}
func TestConversionOpenWindowAndExpiredLoss(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	site := New(now)
	site.Goals = []Goal{{Name: "Purchase", Kind: "event", Match: "purchase"}}
	event := conversionEvent("1111111111111111", "2222222222222222", "/", 1, Signal{Kind: "page"})
	site.Record(event, now, 4<<20)
	pending := site.Report(now.AddDate(0, 0, 20), 90).Conversion.Funnels[0]
	if pending.Users != 1 || pending.Steps[0].Pending != 1 || pending.Steps[0].Lost != 0 {
		t.Fatalf("pending %#v", pending)
	}
	final := site.Report(now.Add(90*24*time.Hour), 90).Conversion
	// The report period selects entry dates; use a timestamp just inside its first date.
	final = site.conversionReport(now.Add(90*24*time.Hour), 91)
	if final.Funnels[0].Steps[0].Lost != 1 || final.Funnels[0].Steps[0].Pending != 0 {
		t.Fatalf("final %#v", final.Funnels)
	}
	late := conversionEvent(event.Visitor, "3333333333333333", "/", 1, Signal{Kind: "goal", Name: "purchase"})
	site.Record(late, now.Add(90*24*time.Hour+time.Minute), 4<<20)
	if site.conversionReport(now.Add(90*24*time.Hour+time.Minute), 91).Funnels[0].Completed != 0 {
		t.Fatal("late conversion attributed")
	}
}
func TestConversionSourcesWithoutGoalsAndOrderedEvents(t *testing.T) {
	now := time.Now().UTC()
	site := New(now)
	event := conversionEvent("1111111111111111", "2222222222222222", "/", 1, Signal{Kind: "page"})
	site.Record(event, now, 4<<20)
	report := site.Report(now, 7).Conversion
	if len(report.Sources) != 1 || report.Sources[0].Visitors != 1 || report.Sources[0].Rate != "—" {
		t.Fatalf("unconfigured sources %#v", report.Sources)
	}
	site.Goals = []Goal{{Name: "Order", Steps: []Goal{{Name: "Checkout", Kind: "page", Page: "/checkout"}, {Name: "Purchase", Kind: "event", Match: "purchase"}}}}
	ordered := conversionEvent(event.Visitor, "3333333333333333", "/checkout", 1, Signal{Kind: "goal", Name: "purchase"}, Signal{Kind: "page", At: 1000})
	site.Record(ordered, now.Add(time.Hour), 4<<20)
	if site.Report(now.Add(time.Hour), 7).Conversion.Funnels[0].Completed != 0 {
		t.Fatal("out-of-order purchase attributed")
	}
}
func TestConversionBehaviourFormsPathsAndPrivacy(t *testing.T) {
	now := time.Now().UTC()
	site := New(now)
	site.Goals = []Goal{{Name: "Download", Kind: "event", Match: "download"}}
	event := conversionEvent("1111111111111111", "2222222222222222", "/", 1, Signal{Kind: "page"}, Signal{Kind: "scroll", Scroll: 60}, Signal{Kind: "click", Element: "key:cta"}, Signal{Kind: "form-view", Form: "key:contact"}, Signal{Kind: "form-start", Form: "key:contact"}, Signal{Kind: "field", Form: "key:contact", Field: "input:1", Filled: true}, Signal{Kind: "form-submit", Form: "key:contact"})
	if !Valid(event) || !site.Record(event, now, 4<<20) {
		t.Fatal("valid structural batch rejected")
	}
	report := site.Report(now.Add(time.Hour), 7).Conversion
	if report.Forms[0].Submitted != 1 || report.Forms[0].Completed != 0 || report.Forms[0].Abandoned != 1 || report.Forms[0].Fields[0].Label != "input:1" {
		t.Fatalf("form %#v", report.Forms)
	}
	if report.Behaviour[0].Scroll[5].Rate != "100.0%" || report.Behaviour[0].Scroll[6].Rate != "0.0%" {
		t.Fatalf("scroll %#v", report.Behaviour)
	}
	if report.Paths[0].ExitRate != "100.0%" {
		t.Fatalf("exit %#v", report.Paths)
	}
	success := conversionEvent(event.Visitor, event.View, "/", 2, Signal{Kind: "form-success", Form: "key:contact"})
	site.Record(success, now.Add(time.Minute), 4<<20)
	if site.Report(now.Add(time.Hour), 7).Conversion.Forms[0].Completed != 1 {
		t.Fatal("confirmed success lost")
	}
	event.Signals = []Signal{{Kind: "click", Element: "email:private@example.com"}}
	if Valid(event) {
		t.Fatal("arbitrary selector accepted")
	}
	preview, err := BehaviourPreview(strings.NewReader(`<html><body><script>secret()</script><form><input value="password-secret" onfocus="secret()"><textarea>message-secret</textarea></form><a href="/purchase">Buy</a></body></html>`))
	if err != nil || strings.Contains(preview, "secret") || strings.Contains(preview, "href=") || !strings.Contains(preview, "data-analytics-layout") {
		t.Fatalf("unsafe preview: %s %v", preview, err)
	}
}
func TestConversionCohortsSurviveIdentityExpiryAndOverloadPreservesAccepted(t *testing.T) {
	now := time.Date(2026, 1, 5, 12, 0, 0, 0, time.UTC)
	site := New(now)
	event := conversionEvent("1111111111111111", "2222222222222222", "/", 1, Signal{Kind: "page"})
	site.Record(event, now, 4<<20)
	returned := conversionEvent(event.Visitor, "3333333333333333", "/", 1, Signal{Kind: "page"})
	site.Record(returned, now.AddDate(0, 0, 7), 4<<20)
	report := site.Report(now.AddDate(0, 0, 14), 30).Conversion
	if report.Cohorts[0].Weeks[0].Rate != "100.0%" {
		t.Fatalf("cohort %#v", report.Cohorts)
	}
	accepted := site.Conversion.Visitors[event.Visitor]
	rejected := conversionEvent("4444444444444444", "5555555555555555", "/", 1, Signal{Kind: "page"})
	if site.Record(rejected, now.AddDate(0, 0, 14), site.Conversion.Used) || site.Conversion.Visitors[event.Visitor] != accepted || !site.Conversion.Incomplete {
		t.Fatal("overload lost accepted observations")
	}
	site.Prune(now.AddDate(0, 0, 120))
	if len(site.Conversion.Visitors) != 0 || site.Report(now.AddDate(0, 0, 120), 90).Conversion.Cohorts[0].Visitors != 1 {
		t.Fatal("expiry biased historical cohort")
	}
}

func TestConversionVisitorMapAndDailyCalendar(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	site := New(now)
	event := conversionEvent("1111111111111111", "2222222222222222", "/", 1, Signal{Kind: "page"})
	event.Address = "192.0.2.9"
	event.Location = VisitorLocation{Address: event.Address, Country: "GB", City: "London", Latitude: 51.5, Longitude: -0.1, Known: true}
	site.Record(event, now, 4<<20)
	event.Sequence++
	event.Signals = []Signal{{Kind: "click", Element: "key:download"}}
	site.Record(event, now.Add(time.Minute), 4<<20)
	report := site.Report(now.Add(time.Hour), 7).Conversion
	if len(report.Locations) != 1 || report.Locations[0].Address != event.Address || report.Locations[0].Landing != "/" || report.Daily[0].Visitors != 1 {
		t.Fatalf("map/calendar inflated by actions: %#v", report)
	}
	site.Prune(now.AddDate(0, 0, 91))
	expired := site.Report(now.AddDate(0, 0, 91), 90).Conversion
	if len(expired.Locations) != 0 || len(expired.Daily) != 1 || expired.Daily[0].Visitors != 1 {
		t.Fatal("IP did not expire separately from daily aggregate")
	}
}

func TestSourceQualityAggregatesExactReferrersAndLandingPages(t *testing.T) {
	now := time.Now().UTC()
	site := New(now)
	site.Goals = []Goal{{Name: "Purchase", Kind: "event", Match: "purchase"}}
	first := conversionEvent("1111111111111111", "2222222222222222", "/product", 1, Signal{Kind: "page"}, Signal{Kind: "goal", Name: "purchase"})
	first.Referrer = "https://github.com/project/releases?tag=v1"
	site.Record(first, now, 4<<20)
	second := conversionEvent("3333333333333333", "4444444444444444", "/pricing", 1, Signal{Kind: "page"})
	second.Referrer = "https://github.com/project/issues/42"
	site.Record(second, now, 4<<20)
	report := site.Report(now, 7).Conversion
	if len(report.Sources) != 1 || report.Sources[0].Visitors != 2 || report.Sources[0].Rate != "50.0%" || len(report.Referrers) != 2 {
		t.Fatal("exact referrer links fragmented source quality", report.Sources, report.Referrers)
	}
}
