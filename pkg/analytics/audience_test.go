package analytics

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

func TestAudienceSessionsAcrossTabsSameDayReturnsAndCheckpoint(t *testing.T) {
	now := time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC)
	site := New(now)
	first := conversionEvent("1111111111111111", "2222222222222222", "/bag", 1, Signal{Kind: "page"})
	if !site.Record(first, now, 8<<20) {
		t.Fatal("first view rejected")
	}
	second := first
	second.View, second.Tab = "3333333333333333", "other-tab"
	if !site.Record(second, now.Add(time.Minute), 8<<20) || site.Record(second, now.Add(time.Minute), 8<<20) {
		t.Fatal("tab deduplication changed")
	}
	initial := site.Report(now.Add(time.Minute), 1).Conversion
	if initial.Audience.Sessions != 1 || initial.Audience.ReturningUsers != 0 || *initial.PageAudience[0].ReturningUsers != 0 {
		t.Fatalf("tabs became return visits: %+v", initial.Audience)
	}
	// A stale browser identifier must still become a new session after inactivity.
	third := first
	third.View = "4444444444444444"
	if !site.Record(third, now.Add(time.Hour), 8<<20) {
		t.Fatal("return rejected")
	}
	encoded, err := json.Marshal(site)
	if err != nil {
		t.Fatal(err)
	}
	restored := New(now)
	if err := json.Unmarshal(encoded, restored); err != nil {
		t.Fatal(err)
	}
	restored.Prune(now.Add(time.Hour))
	report := restored.Report(now.Add(time.Hour), 1)
	audience := report.Conversion.PageAudience[0]
	if report.Conversion.Audience.Sessions != 2 || report.Conversion.Audience.ReturningUsers != 1 || audience.Users != 1 || audience.PageViews != 3 || *audience.Sessions != 2 || *audience.ReturningUsers != 1 || *audience.ReturningSameDay != 1 {
		t.Fatalf("return evidence lost: %+v %+v", report.Conversion.Audience, audience)
	}
	ai := AIReportView(report, "example.org", "get_site_report", "", true, 20, 0)
	if ai.Summary.Sessions == nil || *ai.Summary.Sessions != 2 || *ai.Summary.ReturningUsers != 1 || ai.AudienceSince == nil {
		t.Fatalf("AI lost aggregate: %+v", ai)
	}
	if restored.Record(third, now.Add(time.Hour), 8<<20) {
		t.Fatal("restore lost delivery gate")
	}
}

func TestAudiencePeriodBoundariesCoverageRetentionAndBudget(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	site := New(now.AddDate(0, 0, -2))
	first := conversionEvent("1111111111111111", "2222222222222222", "/bag", 1, Signal{Kind: "page"})
	site.Record(first, now.AddDate(0, 0, -1), 8<<20)
	second := first
	second.View = "3333333333333333"
	site.Record(second, now, 8<<20)
	report := site.Report(now, 1).Conversion
	if report.Audience.Sessions != 1 || report.Audience.ReturningUsers != 1 || *report.PageAudience[0].ReturningUsers != 0 || report.Audience.Incomplete {
		t.Fatalf("period leaked old sessions: %+v %+v", report.Audience, report.PageAudience)
	}
	// Historical state starts coverage at the first new observation, never at an invented date.
	site.Conversion.AudienceSince = time.Time{}
	for _, visitor := range site.Conversion.Visitors {
		visitor.Visits = nil
		visitor.FirstVisit = time.Time{}
	}
	second.View = "4444444444444444"
	site.Record(second, now.Add(time.Minute), 8<<20)
	migrated := site.Report(now.Add(time.Minute), 30).Conversion
	if !migrated.Audience.Incomplete || !migrated.Audience.Since.Equal(now.Add(time.Minute)) || migrated.Audience.Sessions != 1 {
		t.Fatalf("fabricated historical sessions: %+v", migrated.Audience)
	}
	site.Prune(now.AddDate(0, 0, 91))
	if len(site.Conversion.Visitors) != 0 {
		t.Fatal("expired audience retained")
	}
	limited := New(now)
	for index := 0; index < 257; index++ {
		event := conversionEvent(first.Visitor, first.View, "/bag", index+1, Signal{Kind: "page"})
		event.Session = fmt.Sprintf("%016x", index+1)
		limited.Record(event, now.Add(time.Duration(index)*31*time.Minute), 8<<20)
	}
	visitor := limited.Conversion.Visitors[first.Visitor]
	if len(visitor.Visits) != 256 || !limited.Conversion.Incomplete {
		t.Fatal("session admission did not enforce its bound")
	}
	if limited.Record(second, now, 1) {
		t.Fatal("memory quota bypassed")
	}
}

func TestFormsCompareObservedDevicesWithoutInventingHistoricalDevice(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	site := New(now)
	first := conversionEvent("1111111111111111", "2222222222222222", "/contact", 1, Signal{Kind: "page"}, Signal{Kind: "form-view", Form: "contact"}, Signal{Kind: "form-start", Form: "contact"})
	first.Device = "mobile"
	site.Record(first, now, 8<<20)
	second := conversionEvent("3333333333333333", "4444444444444444", "/contact", 1, Signal{Kind: "page"}, Signal{Kind: "form-view", Form: "contact"}, Signal{Kind: "form-start", Form: "contact"}, Signal{Kind: "form-submit", Form: "contact"}, Signal{Kind: "form-success", Form: "contact"})
	second.Device = "desktop"
	site.Record(second, now, 8<<20)
	legacy := conversionEvent("5555555555555555", "6666666666666666", "/contact", 1, Signal{Kind: "form-start", Form: "contact"})
	legacy.Device = ""
	site.Record(legacy, now, 8<<20)
	checkpoint, err := json.Marshal(site)
	if err != nil {
		t.Fatal(err)
	}
	restored := New(now)
	if err := json.Unmarshal(checkpoint, restored); err != nil {
		t.Fatal(err)
	}
	site = restored
	report := site.Report(now.Add(time.Hour), 1)
	form := report.Conversion.Forms[0]
	if form.Started != 3 || form.Completed != 1 || form.Abandoned != 2 || len(form.Devices) != 3 {
		t.Fatalf("form totals changed: %+v", form)
	}
	for _, device := range form.Devices {
		if device.Device == "mobile" && (device.Started != 1 || device.Submitted != 0 || device.Completed != 0 || device.Abandoned != 1) {
			t.Fatalf("mobile loss missing: %+v", device)
		}
		if device.Device == "desktop" && (device.Completed != 1 || device.Abandoned != 0) {
			t.Fatalf("desktop success missing: %+v", device)
		}
	}
	ai := AIReportView(report, "example.org", "get_forms", "", true, 20, 0)
	forms := ai.Sections["forms"].([]map[string]any)
	devices := forms[0]["devices"].([]map[string]any)
	if len(devices) != 3 || devices[0]["device"] != "unknown" {
		t.Fatalf("historical device inferred: %+v", devices)
	}
}
