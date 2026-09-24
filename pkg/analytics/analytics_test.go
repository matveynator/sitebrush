package analytics

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

func observation(view int, path string) Event {
	return Event{Visitor: "11111111111111111111111111111111", View: fmt.Sprintf("%032x", view), Sequence: 1, Path: path, Source: "search.example", Persistent: true}
}

func TestCumulativeDeliveryAndRestoredSession(t *testing.T) {
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	site := New(now)
	event := observation(1, "/docs")
	site.Record(event, now, 8<<20)
	event.Sequence = 2
	event.ActiveMS = 10000
	event.Scroll = 50
	site.Record(event, now.Add(10*time.Second), 8<<20)
	site.Record(event, now.Add(11*time.Second), 8<<20)
	encoded, err := json.Marshal(site)
	if err != nil {
		t.Fatal(err)
	}
	restored := New(now)
	if err := json.Unmarshal(encoded, restored); err != nil {
		t.Fatal(err)
	}
	restored.Prune(now)
	restored.Record(observation(2, "/pricing"), now.Add(2*time.Minute), 8<<20)
	report := restored.Report(now.Add(3*time.Minute), 1)
	if report.Views != 2 || report.Sessions != 1 || report.ActiveMS != 10000 || report.Scroll[1] != 1 || report.SingleSessions != 0 {
		t.Fatalf("report: %+v", report)
	}
}

func TestReturnCohortsAndExpiration(t *testing.T) {
	first := time.Date(2026, 6, 1, 10, 0, 0, 0, time.UTC)
	site := New(first)
	for index, offset := range []int{0, 1, 7, 30} {
		site.Record(observation(index+1, "/"), first.AddDate(0, 0, offset), 8<<20)
	}
	report := site.Report(first.AddDate(0, 0, 31), 90)
	if report.Visitors != 1 || report.Returning != 1 || report.Sessions != 4 {
		t.Fatalf("report: %+v", report)
	}
	for _, retention := range report.Retention {
		if retention.Eligible != 1 || retention.Returned != 1 {
			t.Fatalf("retention: %+v", retention)
		}
	}
	immature := site.Report(first.AddDate(0, 0, 30), 90)
	if immature.Retention[2].Eligible != 0 {
		t.Fatal("incomplete target day must not count")
	}
	site.Prune(first.AddDate(0, 0, 121))
	if len(site.Visitors) != 0 || len(site.Days) != 0 {
		t.Fatal("expired history retained")
	}
}

func TestLimitPreservesAcceptedObservations(t *testing.T) {
	now := time.Now().UTC()
	site := New(now)
	if !site.Record(observation(1, "/"), now, 8<<20) {
		t.Fatal("first event rejected")
	}
	if site.Record(observation(2, "/next"), now, 1) {
		t.Fatal("limit ignored")
	}
	report := site.Report(now, 1)
	if !report.Incomplete || report.Views != 1 {
		t.Fatalf("report: %+v", report)
	}
}

func TestPageHistoryAndInputAreBounded(t *testing.T) {
	now := time.Now().UTC()
	site := New(now)
	for index := 1; index <= 100; index++ {
		site.Record(observation(index, fmt.Sprintf("/page/%d", index)), now.Add(time.Duration(index)*time.Second), 8<<20)
	}
	visitor := site.Visitors[observation(1, "/").Visitor]
	if len(visitor.Pages) > MaximumPages || len(visitor.Views) > 32 || !site.HistoryLimited {
		t.Fatalf("unbounded history: pages=%d views=%d", len(visitor.Pages), len(visitor.Views))
	}
	event := observation(1, "/secret?token=password")
	if Valid(event) {
		t.Fatal("query accepted")
	}
	event = observation(1, "/")
	event.ActiveMS = -1
	if Valid(event) {
		t.Fatal("negative duration accepted")
	}
}

func TestContinuedReadingAcrossSessions(t *testing.T) {
	// Keep the whole scenario inside one UTC reporting day. Using time.Now()
	// makes this test fail when CI runs late in the UTC day because Report(..., 1)
	// then covers the following day and excludes the recorded insight.
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	site := New(now)
	first := observation(1, "/docs")
	first.Scroll = 25
	site.Record(first, now, 8<<20)
	next := observation(2, "/docs")
	site.Record(next, now.Add(time.Hour), 8<<20)
	next.Sequence = 2
	next.Scroll = 50
	next.ActiveMS = 10000
	site.Record(next, now.Add(time.Hour+10*time.Second), 8<<20)
	report := site.Report(now.Add(2*time.Hour), 1)
	found := false
	for _, insight := range report.Insights {
		if insight.Label == "Possible continued reading" && insight.Count == 1 {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing continued reading: %+v", report.Insights)
	}
}

func TestIdleMessagesCannotExtendSession(t *testing.T) {
	// Keep the whole scenario on one UTC calendar day. Using time.Now here made
	// the one-day report flaky when CI crossed midnight during the 42-minute
	// simulated session window.
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	site := New(now)
	event := observation(1, "/")
	site.Record(event, now, 8<<20)
	for index := 1; index <= 40; index++ {
		event.Sequence++
		site.Record(event, now.Add(time.Duration(index)*time.Minute), 8<<20)
	}
	site.Record(observation(2, "/next"), now.Add(41*time.Minute), 8<<20)
	if report := site.Report(now.Add(42*time.Minute), 1); report.Sessions != 2 {
		t.Fatalf("idle messages prolonged session: %+v", report)
	}
}
