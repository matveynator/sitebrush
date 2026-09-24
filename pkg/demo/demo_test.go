package demo

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func TestDemoHelpers(t *testing.T) {
	if !reflect.DeepEqual(TableNames(), []string{"demo_site_sessions"}) {
		t.Fatalf("table names=%v", TableNames())
	}
	if AdminEmail(" Example.COM ") != "demo@example.com" || AdminEmail(" ") != "demo@localhost" {
		t.Fatal("admin email normalization failed")
	}
	if got := SnapshotPath("/backup", " demo "); got != "/backup/demo-demo-snapshot.zip" {
		t.Fatalf("snapshot path = %q", got)
	}
	if got := UniqueDomains(" A.com ", "a.COM", "", "B.com"); !reflect.DeepEqual(got, []string{"a.com", "b.com"}) {
		t.Fatalf("unique domains = %#v", got)
	}
	if got, err := NormalizeSourceURL("//Example.com/path#fragment"); err != nil || got != "https://Example.com/path" {
		t.Fatalf("normalized URL = %q, %v", got, err)
	}
	if got, err := NormalizeSourceURL(" "); err != nil || got != "" {
		t.Fatalf("empty URL = %q, %v", got, err)
	}
	for _, rawURL := range []string{"ftp://example.com", "https:///missing-host"} {
		if _, err := NormalizeSourceURL(rawURL); err == nil {
			t.Fatalf("invalid source URL accepted: %q", rawURL)
		}
	}
}

func TestDemoRequestAndSessionHelpers(t *testing.T) {
	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "https://example.com/page", nil),
		httptest.NewRequest(http.MethodHead, "https://example.com/", nil),
	} {
		if !CanStartFromRequest(request) {
			t.Fatalf("ordinary page request was rejected: %s", request.URL)
		}
	}
	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodPost, "https://example.com/", nil),
		httptest.NewRequest(http.MethodGet, "https://example.com/image.png", nil),
		httptest.NewRequest(http.MethodGet, "https://example.com/?logout", nil),
	} {
		if CanStartFromRequest(request) {
			t.Fatalf("excluded request was accepted: %s", request.URL)
		}
	}
	if CanStartFromRequest(nil) || HasQueryFlag(nil, "logout") {
		t.Fatal("nil request was accepted")
	}
	for raw, want := range map[string]bool{"?flag": true, "?flag=0": false, "?flag=false": false, "?flag=yes": true} {
		request := httptest.NewRequest(http.MethodGet, "https://example.com/"+raw, nil)
		if got := HasQueryFlag(request, "flag"); got != want {
			t.Errorf("HasQueryFlag(%q) = %t, want %t", raw, got, want)
		}
	}
	now := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	resetAt := now.Add(ResetDelay)
	if reset, ok := SessionResetAfter(Session{CreatedAt: now.Format(time.RFC3339)}); !ok || !reset.Equal(resetAt) {
		t.Fatalf("creation-based reset = %v, %t", reset, ok)
	}
	if _, ok := SessionResetAfter(Session{}); ok {
		t.Fatal("invalid session dates produced a reset time")
	}
	if !SessionsReadyForReset([]Session{{DeleteAfter: resetAt.Format(time.RFC3339)}}, resetAt) || SessionsReadyForReset([]Session{{DeleteAfter: resetAt.Format(time.RFC3339)}}, now) {
		t.Fatal("session reset readiness boundary is incorrect")
	}
}

func TestDemoStoreLifecycle(t *testing.T) {
	ctx := context.Background()
	database, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "demo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	for _, statement := range SchemaQueries() {
		if _, err := database.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := database.Exec(`CREATE TABLE server_settings(name TEXT PRIMARY KEY,value TEXT,updated_at TEXT)`); err != nil {
		t.Fatal(err)
	}
	store := Store{DB: database}
	if err := store.MarkContentRestored(ctx, " ", time.Time{}); err == nil {
		t.Fatal("empty restored domain was accepted")
	}
	if err := store.SaveSettings(ctx, "demo.example", "https://source.example", true, true); err != nil {
		t.Fatal(err)
	}
	if settings := store.Settings(ctx); settings.Domain != "demo.example" || settings.SourceURL != "https://source.example" || !settings.CopyWholeSite || !settings.Enabled {
		t.Fatalf("stored settings = %#v", settings)
	}
	if err := store.MarkContentRestored(ctx, " demo.example ", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	if settings := store.Settings(ctx); settings.LastRestoredAt != "2026-01-01T00:00:00Z" {
		t.Fatalf("restoration timestamp = %q", settings.LastRestoredAt)
	}
	if err := store.CreateSession(ctx, " ", "token", "user@example.com", time.Time{}); err == nil {
		t.Fatal("session with empty domain was created")
	}
	if err := store.CreateSession(ctx, "demo.example", " ", "user@example.com", time.Time{}); err == nil {
		t.Fatal("session with empty token was created")
	}
	if err := store.CreateSession(ctx, "demo.example", "token", "user@example.com", time.Time{}); err != nil {
		t.Fatal(err)
	}
	if sessions := store.Sessions(ctx); len(sessions) != 1 || sessions[0].Status != "active" {
		t.Fatalf("sessions = %#v", sessions)
	}
	resetAt := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	if err := store.ScheduleSessionReset(ctx, " ", resetAt); err != nil {
		t.Fatal(err)
	}
	if err := store.ScheduleSessionReset(ctx, "token", resetAt); err != nil {
		t.Fatal(err)
	}
	if sessions := store.Sessions(ctx); sessions[0].Status != "deleting" || sessions[0].DeleteAfter != resetAt.Format(time.RFC3339) {
		t.Fatalf("scheduled session = %#v", sessions[0])
	}
	if err := store.RemoveSessionsForDomain(ctx, "demo.example"); err != nil {
		t.Fatal(err)
	}
	if sessions := store.Sessions(ctx); len(sessions) != 0 {
		t.Fatalf("sessions after removal = %#v", sessions)
	}
}
