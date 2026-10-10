package main

import (
	"net/http"
	"testing"
	"time"
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
	state.resetAfterFlush()
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
