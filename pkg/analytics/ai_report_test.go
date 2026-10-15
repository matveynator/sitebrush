package analytics

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestAIReportContainsOnlyBoundedAggregatesAndNoVisitorIdentity(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	report := Report{Days: 30, PeriodStart: now.AddDate(0, 0, -29), PeriodEnd: now, Generated: now, Conversion: ConversionReport{
		Version: 2, Visitors: 43, Incomplete: true,
		PageAudience: []PageAudience{{Page: "/bag", Users: 43, PageViews: 66, ReturningOnOtherDays: 11}, {Page: "/old", Users: 7}},
		Locations:    []VisitorLocation{{Address: "203.0.113.77", City: "private-city"}},
		Referrers:    []ConversionSource{{Name: "raw-campaign-person@example.org", Kind: "campaign", Referrer: "https://user:secret@search.example/find?q=private-query#secret", Landing: "/bag?token=private-token", Campaign: "private-campaign", Visitors: 43, Completed: 3}},
		Behaviour:    []BehaviourPage{{Page: "/bag", Visitors: 43, Elements: []BehaviourRow{{Element: "email=private-person@example.org", Kind: "dead-click", Users: 8, Clicks: 11}}}},
		Forms:        []ConversionForm{{Page: "/bag", Form: "private-form-label", Started: 5, Submitted: 3, Completed: 2, Abandoned: 3, Fields: []Row{{Label: "private-field-label", Count: 2}}}},
		Insights:     []ConversionInsight{{Kind: "dead", Subject: "/bag", Users: 8}},
	}, Experience: ExperienceReport{Recent: []SessionSummary{{ID: "raw-session", Visitor: "raw-visitor", Address: "203.0.113.77"}}}}
	result := AIReportView(report, "example.org", "get_site_report", "", true, 1, 0)
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"203.0.113.77", "private-city", "raw-session", "raw-visitor", "raw-campaign", "user:secret", "private-query", "private-token", "private-campaign", "private-person", "private-form-label", "private-field-label"} {
		if strings.Contains(string(encoded), private) {
			t.Fatalf("privacy boundary exposed %q: %s", private, encoded)
		}
	}
	if !strings.Contains(string(encoded), "https://search.example") || !strings.Contains(string(encoded), `"returning_on_other_days":11`) || !strings.Contains(string(encoded), `"clicked_users":8`) {
		t.Fatalf("lost actionable aggregate evidence: %s", encoded)
	}
	if result.Summary.Visitors == nil || *result.Summary.Visitors != 43 || result.Summary.Sessions != nil || result.Summary.PageViews == nil || *result.Summary.PageViews != 66 || !result.Incomplete || !result.Truncated || result.Totals["pages"] != 2 {
		t.Fatalf("invalid report metadata: %+v", result)
	}
	if report.Conversion.Forms[0].Form != "private-form-label" || report.Conversion.Behaviour[0].Elements[0].Element != "email=private-person@example.org" {
		t.Fatal("report mapping mutated its source snapshot")
	}
	second := AIReportView(report, "example.org", "get_page_behaviour", "", true, 1, 1)
	if second.Sections["pages"].([]PageAudience)[0].Page != "/old" {
		t.Fatal("aggregate pagination repeated first row")
	}
	if result.Sections["forms"].([]map[string]any)[0]["devices_available"] != false {
		t.Fatal("legacy snapshot fabricated a form device breakdown")
	}
}

func TestAIReportSeparatesUnavailableSignalsFromMeasuredZero(t *testing.T) {
	missing := AIReportView(Report{}, "example.org", "get_site_report", "", false, 20, 0)
	if missing.Summary.Visitors != nil || missing.Summary.PageViews != nil || missing.Available || len(missing.Sections) != 0 {
		t.Fatalf("invented unavailable observations: %+v", missing)
	}
	zero := AIReportView(Report{Conversion: ConversionReport{Version: 2}}, "example.org", "get_site_report", "", true, 20, 0)
	if zero.Summary.Visitors == nil || *zero.Summary.Visitors != 0 || zero.Summary.PageViews != nil || zero.Summary.Sessions != nil {
		t.Fatalf("old snapshot fabricated measurements: %+v", zero)
	}
	for _, signal := range []string{"search_engine_queries", "internal_search_terms", "total_sessions", "causal_attribution"} {
		if !strings.Contains(strings.Join(zero.Unavailable, ","), signal) {
			t.Fatalf("missing unavailable signal %q", signal)
		}
	}
}

func TestPageAudienceUsesObservedDatesViewsAndUniqueVisitors(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	site := New(now.AddDate(0, 0, -1))
	first := conversionEvent("1111111111111111", "2222222222222222", "/bag", 1, Signal{Kind: "page"})
	if !site.Record(first, now.AddDate(0, 0, -1), 4<<20) {
		t.Fatal("first observation rejected")
	}
	second := conversionEvent(first.Visitor, "3333333333333333", "/bag", 1, Signal{Kind: "page"})
	if !site.Record(second, now, 4<<20) || site.Record(second, now, 4<<20) {
		t.Fatal("deduplication changed")
	}
	third := conversionEvent("4444444444444444", "5555555555555555", "/bag", 1, Signal{Kind: "page"})
	if !site.Record(third, now, 4<<20) {
		t.Fatal("third observation rejected")
	}
	period := site.Report(now, 30)
	if len(period.Conversion.PageAudience) != 1 {
		t.Fatalf("audience: %+v", period.Conversion.PageAudience)
	}
	audience := period.Conversion.PageAudience[0]
	if audience.Users != 2 || audience.ReturningOnOtherDays != 1 || audience.PageViews != 3 {
		t.Fatalf("wrong audience: %+v", audience)
	}
	singleDay := site.Report(now, 1).Conversion.PageAudience[0]
	if singleDay.Users != 2 || singleDay.ReturningOnOtherDays != 0 || singleDay.PageViews != 2 {
		t.Fatalf("period leaked older visits: %+v", singleDay)
	}
}

func TestAIPageImpactsExposeComparisonWindowsAndSampleCaveats(t *testing.T) {
	now := time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)
	changed := now.AddDate(0, 0, -2)
	report := Report{PeriodStart: now.AddDate(0, 0, -29), PeriodEnd: now, Generated: now, Conversion: ConversionReport{Version: 2, Impacts: []PageImpact{{Page: "/bag", At: changed, BeforeVisitors: 20, BeforeCompleted: 4, AfterVisitors: 10, AfterCompleted: 3, BeforeRate: "20%", AfterRate: "30%", Partial: true, SmallSample: true, OtherChanges: true}}}}
	result := AIReportView(report, "example.org", "get_page_impacts", "/bag", true, 20, 0)
	impact := result.Sections["page_impacts"].([]AIImpact)[0]
	if impact.BeforeStart != changed.Add(-ImpactWindow) || impact.AfterEnd != now || impact.BeforeUsers != 20 || impact.AfterCompleted != 3 || !impact.Partial || !impact.SmallSample || !impact.OtherChanges || !impact.ConversionAvailable {
		t.Fatalf("impact evidence lost: %+v", impact)
	}
}

func TestAIReportRedactsIdentityLikeURLSegments(t *testing.T) {
	for _, raw := range []string{"/account/alice%40example.org", "/visitor/203.0.113.77", "/link/0123456789abcdef0123456789abcdef"} {
		cleaned := aiSafePath(raw)
		if !strings.Contains(cleaned, "redacted-") || strings.Contains(cleaned, "alice") || strings.Contains(cleaned, "203.0.113") || strings.Contains(cleaned, "0123456789abcdef") {
			t.Fatalf("identity path exposed: %q -> %q", raw, cleaned)
		}
	}
	if aiSafePath("/catalog/cork-bag?secret=private") != "/catalog/cork-bag" || aiSafePath("") != "" {
		t.Fatal("ordinary content path or missing landing changed")
	}
}
