package analytics

import (
	"testing"
	"time"
)

func TestExperienceMeasureFormatting(t *testing.T) {
	metrics := Measures{GoalSessions: 1, Sessions: 4, ActionViews: 2, Views: 4, ProgressViews: 3, EndViews: 1, CompletedViews: 2, Scroll50: 2, ActiveMS: 5000}
	checks := map[string]string{
		metrics.GoalRate(): "25.0% (1/4)", metrics.ActionRate(): "50.0% (2/4)", metrics.NextRate(): "75.0% (3/4)",
		metrics.EndRate(): "50.0% (1/2)", metrics.ScrollRate(): "50.0% (2/4)", metrics.AverageActive(): "1.2 s",
		(SessionSummary{ActiveMS: 1250}).Active():            "1.2 s",
		(SessionSummary{}).ReturnAfter():                     "—",
		(SessionSummary{ReturnAfterMS: 30000}).ReturnAfter(): "1m",
		(SessionSummary{ReturnAfterMS: int64(2*time.Hour+15*time.Minute) / int64(time.Millisecond)}).ReturnAfter(): "2h 15m",
		(SessionSummary{ReturnAfterMS: int64(26*time.Hour+5*time.Hour) / int64(time.Millisecond)}).ReturnAfter():   "1d 7h",
		(SessionSummary{Started: time.Date(2026, 9, 24, 1, 2, 0, 0, time.FixedZone("test", 3600))}).When():         "2026-09-24 00:02 UTC",
	}
	for got, want := range checks {
		if got != want {
			t.Errorf("formatted %q, want %q", got, want)
		}
	}
	if (Measures{}).GoalRate() != "—" || (Measures{}).AverageActive() != "—" {
		t.Fatal("zero denominator should be unavailable")
	}
}

func TestSecurityReportAndReasons(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	state := SecurityState{
		Incomplete: true,
		EventBuckets: map[string]*SecurityEventBucket{
			"today":    {Date: "2026-09-24", Country: "RU", Category: "probe", Hour: 8, Count: 3},
			"bad-hour": {Date: "2026-09-24", Country: "US", Category: "scan", Hour: 24, Count: 2},
			"future":   {Date: "2026-09-25", Country: "FR", Category: "probe", Hour: 9, Count: 5},
		},
		Groups: map[string]*RequestGroup{
			"today": {Date: "2026-09-24", Count: 4, Errors: 1}, "old": {Date: "2026-01-01", Count: 9},
		},
		Incidents: []Incident{{Last: now, Categories: map[string]int{"scan": 1, "probe": 2}}},
	}
	report := state.Report(now, 1)
	if report.Requests != 4 || report.Errors != 1 || report.Suspicious != 1 || report.EventHours[8] != 3 || !report.Incomplete {
		t.Fatalf("unexpected report: %+v", report)
	}
	if got := state.Incidents[0].Reasons(); got != "probe × 2, scan × 1" {
		t.Fatalf("reasons=%q", got)
	}
}
