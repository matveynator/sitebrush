package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	browserstats "github.com/matveynator/sitebrush/v2/pkg/analytics"
)

func TestAnalyticsSeparatesSessionPageAndFiftyFilesAcrossFlush(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	state := newAnalyticsAggregateState(16 << 20)
	event := siteAnalyticsEvent{Domain: "site.example", Path: "/", Method: http.MethodGet, StatusCode: http.StatusOK, ContentSource: "static", UserAgent: "Mozilla/5.0", VisitorID: "visitor", OccurredAt: now, Referer: "https://example-game.com/article"}
	state.record(event)
	for index := 0; index < 50; index++ {
		asset := event
		asset.Path = "/image.png"
		asset.IsAsset = true
		state.record(asset)
	}
	first := state.reports(now)[event.Domain]
	if first.TotalRequests != 51 || first.HumanSessions != 1 || first.PageViews != 1 || first.StaticRequests != 50 {
		t.Fatalf("mixed units: %+v", first)
	}
	state = state.nextAfterFlush(now)
	event.OccurredAt = now.Add(time.Minute)
	event.Path = "/download/"
	event.Referer = "https://site.example/"
	state.record(event)
	second := state.reports(event.OccurredAt)[event.Domain]
	if second.HumanSessions != 0 || second.PageViews != 1 {
		t.Fatalf("flush created a visit: %+v", second)
	}
	event.OccurredAt = now.Add(32 * time.Minute)
	event.UserAgent = "Go-http-client/1.1"
	event.VisitorID = "bot"
	state.record(event)
	third := state.reports(event.OccurredAt)[event.Domain]
	if third.BotSessions != 1 || third.PageViews != 1 {
		t.Fatalf("bot became page view: %+v", third)
	}
}

type analyticsFlushObservedRepository struct {
	browserstats.Repository
	saved chan string
}

func (repository analyticsFlushObservedRepository) Exchange(request browserstats.StorageRequest) browserstats.StorageResult {
	result := repository.Repository.Exchange(request)
	if request.Operation == browserstats.SaveTechnical && result.Err == nil {
		repository.saved <- request.Report
	}
	return result
}

func TestAnalyticsLiveFlushPreservesHumanAndBotSessions(t *testing.T) {
	store := browserstats.OpenStore(t.TempDir())
	saved := make(chan string, 16)
	app := &App{
		analyticsStorage: analyticsFlushObservedRepository{Repository: store, saved: saved},
		analyticsEvents:  make(chan siteAnalyticsEvent, 16), analyticsFlushInterval: 20 * time.Millisecond,
		analyticsMemoryLimit: 16 << 20,
	}
	boundary, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); app.runAnalyticsEventWriter(boundary) }()
	t.Cleanup(func() {
		defer store.Close()
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("collector did not stop")
		}
	})
	now := time.Now().UTC()
	for round, elapsed := range []time.Duration{0, time.Second, 32 * time.Minute} {
		for _, agent := range []string{"Mozilla/5.0 Chrome/124.0.0.0", "Go-http-client/1.1"} {
			app.analyticsEvents <- siteAnalyticsEvent{
				Domain: "site.example", Path: "/docs/", Method: http.MethodGet, StatusCode: http.StatusOK,
				ClientIP: "203.0.113.1", UserAgent: agent, OccurredAt: now.Add(elapsed), Referer: "https://example-game.com/article",
			}
		}
		deadline := time.After(5 * time.Second)
		for {
			select {
			case snapshot := <-saved:
				var history analyticsTechnicalHistory
				if err := json.Unmarshal([]byte(snapshot), &history); err != nil {
					t.Fatal(err)
				}
				merged := analyticsPreparedReport{}
				for _, daily := range history.Days {
					merged = mergeTechnicalReports(merged, daily)
				}
				if merged.TotalRequests < (round+1)*2 {
					continue
				}
				sessions := 1
				if round == 2 {
					sessions = 2
				}
				if merged.TotalRequests != (round+1)*2 || merged.PageViews != round+1 || merged.HumanSessions != sessions || merged.BotSessions != sessions {
					t.Fatalf("live flush round %d: %+v", round, merged)
				}
				assertAnalyticsRow(t, merged.VisitorTypes, "human", sessions)
				assertAnalyticsRow(t, merged.VisitorTypes, "bot", sessions)
			case <-deadline:
				t.Fatalf("live flush round %d timed out", round)
			}
			break
		}
	}
}

func TestAnalyticsRotationDetachesAndExpiresSessionHistory(t *testing.T) {
	now := time.Now().UTC()
	state := newAnalyticsAggregateState(16 << 20)
	event := siteAnalyticsEvent{Domain: "site.example", Path: "/", Method: http.MethodGet, StatusCode: http.StatusOK, UserAgent: "Mozilla/5.0", VisitorID: "visitor", OccurredAt: now}
	state.record(event)
	next := state.nextAfterFlush(now.Add(time.Minute))
	event.OccurredAt = now.Add(2 * time.Minute)
	next.record(event)
	original := state.domains[event.Domain].visitorSessions[event.VisitorID]
	if original.pageCount != 1 || !original.lastEvent.OccurredAt.Equal(now) {
		t.Fatal("collector changed the detached persistence batch")
	}
	if next.usedBytes == 0 || next.domains[event.Domain].humanSessions != 0 {
		t.Fatal("retained history was not budgeted or created a new session")
	}
	if expired := next.nextAfterFlush(now.Add(33 * time.Minute)); len(expired.domains) != 0 {
		t.Fatal("inactive history survived expiry")
	}
}

func TestMergedAudiencePercentagesUseSessions(t *testing.T) {
	for _, botsOnly := range []bool{false, true} {
		name := "mixed"
		if botsOnly {
			name = "bots-only"
		}
		t.Run(name, func(t *testing.T) {
			now := time.Now().UTC()
			aggregate := newSiteAnalyticsAggregate()
			for _, agent := range []string{"Mozilla/5.0 Chrome/124.0.0.0", "Go-http-client/1.1"} {
				if botsOnly && agent != "Go-http-client/1.1" {
					continue
				}
				aggregate.record(siteAnalyticsEvent{Path: "/", Method: http.MethodGet, StatusCode: http.StatusOK, VisitorID: agent, UserAgent: agent, Referer: "https://example-game.com/article", AcceptLanguage: "en-US", GeoCountryCode: "US", GeoCity: "New York", OccurredAt: now})
			}
			report := aggregate.report(now)
			merged := mergeTechnicalReports(mergeTechnicalReports(report, report), report)
			audience := merged.HumanSessions + merged.BotSessions
			for _, dimension := range []struct {
				name  string
				rows  []analyticsCountRow
				total int
			}{
				{"sources", merged.TrafficSources, audience}, {"referrers", merged.Referrers, audience},
				{"countries", merged.Countries, audience}, {"cities", merged.Cities, audience},
				{"devices", merged.Devices, audience}, {"types", merged.VisitorTypes, audience},
				{"browsers", merged.Browsers, audience}, {"OS", merged.OperatingSystems, audience},
				{"languages", merged.Languages, audience}, {"crawlers", merged.BotCrawlers, merged.BotSessions},
				{"bot referrers", merged.BotReferrers, merged.BotSessions},
			} {
				if len(dimension.rows) == 0 {
					t.Fatalf("%s has no observations", dimension.name)
				}
				for _, row := range dimension.rows {
					if want := analyticsPercent(row.Count, dimension.total); row.Value != want {
						t.Errorf("%s %s: %s, want %s", dimension.name, row.Label, row.Value, want)
					}
				}
			}
		})
	}
}
