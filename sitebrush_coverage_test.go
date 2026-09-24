package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type sqliteCodedError int

func (e sqliteCodedError) Error() string { return "sqlite failure" }
func (e sqliteCodedError) Code() int     { return int(e) }

func TestCoverageSiteBrushPagePathAndDefaults(t *testing.T) {
	for _, test := range []struct{ path, want string }{
		{"", ""}, {"/", ""}, {"/a/", "/a"}, {"/a/b", "/a"}, {"page", "/"},
	} {
		if got := parentPagePath(test.path); got != test.want {
			t.Errorf("parentPagePath(%q) = %q, want %q", test.path, got, test.want)
		}
	}
	application, database := newTestApplication(t)
	if got := application.defaultHTMLForNewPage(context.Background(), "example.com", "/parent/child"); got != "<h1>New page</h1>" {
		t.Fatalf("new page fallback = %q", got)
	}
	if _, err := database.Exec(`INSERT INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,?)`, "example.com", "/parent", "parent", "<main>parent</main>", 0); err != nil {
		t.Fatal(err)
	}
	if got := application.defaultHTMLForNewPage(context.Background(), "example.com", "/parent/child"); got != "<main>parent</main>" {
		t.Fatalf("inherited page HTML = %q", got)
	}
}

func TestCoverageSiteBrushStorageAndGrabHelpers(t *testing.T) {
	for _, code := range []int{5, 6, 261, 262} {
		if !isTransientSiteDatabaseLock(sqliteCodedError(code)) {
			t.Errorf("sqlite code %d was not classified as a transient lock", code)
		}
	}
	if isTransientSiteDatabaseLock(errors.New("locked")) || isTransientSiteDatabaseLock(nil) {
		t.Fatal("non-SQLite lock error was classified as transient")
	}
	for _, test := range []struct {
		kind siteDBWorkloadKind
		want string
	}{
		{siteDBWorkloadRead, "read"}, {siteDBWorkloadWrite, "write"}, {siteDBWorkloadGeneral, "general"},
	} {
		if got := siteDBWorkloadName(test.kind); got != test.want {
			t.Errorf("siteDBWorkloadName(%d) = %q, want %q", test.kind, got, test.want)
		}
	}
	for _, test := range []struct{ source, ip, want string }{
		{"localhost/page", "", "http"}, {"127.0.0.1", "", "http"}, {"example.com", "", "https"},
		{"example.com", "8.8.8.8:80", "http"}, {"example.com", "8.8.8.8:443", "https"},
	} {
		if got := defaultGrabSchemeForServerIP(test.source, test.ip); got != test.want {
			t.Errorf("defaultGrabSchemeForServerIP(%q, %q) = %q, want %q", test.source, test.ip, got, test.want)
		}
	}
	if got := defaultGrabScheme("example.com/path"); got != "https" {
		t.Errorf("defaultGrabScheme = %q", got)
	}
	if got := supportedGrabSourceLanguageCodes(); len(got) != 16 || got[0] != "en" {
		t.Fatalf("supported source languages = %#v", got)
	}
	var output strings.Builder
	if written, err := copyWithLimit(&output, strings.NewReader("abc"), 3); err != nil || written != 3 || output.String() != "abc" {
		t.Fatalf("copy under limit = %d, %q, %v", written, output.String(), err)
	}
	output.Reset()
	if written, err := copyWithLimit(&output, strings.NewReader("abcd"), 3); !errors.Is(err, errReadLimitExceeded) || written != 4 {
		t.Fatalf("copy over limit = %d, %q, %v", written, output.String(), err)
	}
	output.Reset()
	if written, err := copyWithLimit(&output, strings.NewReader("unlimited"), 0); err != nil || written != 9 {
		t.Fatalf("unlimited copy = %d, %v", written, err)
	}
	if !isSuccessfulGrabResponse(&http.Response{StatusCode: http.StatusOK}) || isSuccessfulGrabResponse(&http.Response{StatusCode: http.StatusNotFound}) || isSuccessfulGrabResponse(nil) {
		t.Fatal("grab response status classification failed")
	}
	request := httptest.NewRequest("GET", "/", nil)
	request.Header.Set("Accept", "application/json; charset=utf-8")
	if !wantsJSONResponse(request) {
		t.Fatal("JSON Accept header was not recognized")
	}
}

func TestCoverageEmailDNSSetupTranslations(t *testing.T) {
	languages := []string{"ru", "fr", "ja", "it", "sv", "fi", "mn", "zh", "he", "fa", "de", "tr", "kk", "es", "pt", "en"}
	for _, language := range languages {
		view := emailDNSSetupViewForLanguage(language, "example.org", "mail@example.org", "2001:db8::1")
		if view.Title == "" || view.Intro == "" || view.Explanation == "" || len(view.Records) != 2 {
			t.Errorf("language %q has incomplete DNS setup view: %+v", language, view)
		}
		if !strings.Contains(view.Records[0].Value, "AAAA") || !strings.Contains(view.Records[1].Value, "ip6:") {
			t.Errorf("language %q did not use IPv6 DNS records: %+v", language, view.Records)
		}
	}
	defaultView := emailDNSSetupViewForLanguage("unknown", "", "", "")
	if defaultView.Title == "" || !strings.Contains(defaultView.Records[0].Value, "example.com.") {
		t.Fatalf("default DNS setup view = %+v", defaultView)
	}
}

func TestCoverageAnalyticsTechnicalReportMerge(t *testing.T) {
	addition := analyticsPreparedReport{GeneratedAt: "2026-09-24T12:00:00Z", PeriodEnd: "2026-09-25", TotalRequests: 2, PageViews: 2, AverageDurationMS: 300}
	if got := mergeTechnicalReports(analyticsPreparedReport{}, addition); got.GeneratedAt != addition.GeneratedAt {
		t.Fatalf("initial report merge = %+v", got)
	}
	current := analyticsPreparedReport{
		GeneratedAt: "2026-09-24T11:00:00Z", PeriodStart: "2026-09-23", PeriodEnd: "2026-09-24",
		TotalRequests: 2, PageViews: 1, AverageDurationMS: 100,
		TopPages:     []analyticsCountRow{{Label: "/", Count: 1}},
		SystemEvents: make([]analyticsCountRow, 32), UniqueVisitors: 9, Sessions: 7,
		EntryPages: []analyticsCountRow{{Label: "/stale", Count: 1}},
	}
	addition.TopPages = []analyticsCountRow{{Label: "/", Count: 2}}
	addition.SystemEvents = []analyticsCountRow{{Label: "latest", Count: 1}}
	got := mergeTechnicalReports(current, addition)
	if got.TotalRequests != 4 || got.PageViews != 3 || got.AverageDurationMS != 200 || got.TopPages[0].Count != 3 {
		t.Fatalf("merged totals = %+v", got)
	}
	if got.PeriodStart != "2026-09-23" || got.PeriodEnd != addition.PeriodEnd || got.GeneratedAt != addition.GeneratedAt {
		t.Fatalf("merged time range = %+v", got)
	}
	if len(got.SystemEvents) != 32 || got.SystemEvents[31].Label != "latest" || got.UniqueVisitors != 0 || got.Sessions != 0 || got.EntryPages != nil {
		t.Fatalf("merged bounded report state = %+v", got)
	}
}

func TestCoverageBackupReferenceRewriting(t *testing.T) {
	for _, reference := range []string{"", "mailto:test@example.org", "TEL:123", "javascript:void(0)", "data:image/png,x", "blob:https://example.org/id", "//cdn.example.org/a.js", "https://example.org/a"} {
		if got := rewriteBackupReference(reference, "/backup", "prefix-"); got != reference {
			t.Errorf("blocked/external reference %q became %q", reference, got)
		}
	}
	if got := rewriteBackupReference(" /images/a.png?size=1#top ", "/backup", ""); got != "/backup/images/a.png?size=1#top" {
		t.Errorf("root relative reference = %q", got)
	}
	if got := rewriteBackupReference("/backup/images/a.png", "/backup", ""); got != "/backup/images/a.png" {
		t.Errorf("already based reference = %q", got)
	}
	if got := rewriteBackupReference("/images/a.png", "/", ""); got != "/images/a.png" {
		t.Errorf("root backup reference = %q", got)
	}
	if got := rewriteBackupReference("/p/images/a.png?x=1", "/backup", ""); got != "/p/images/a.png?x=1" {
		t.Errorf("asset reference without prefix = %q", got)
	}
	if got := rewriteBackupReference("relative/page", "/backup", "prefix-"); got != "relative/page" {
		t.Errorf("relative reference = %q", got)
	}
}

func TestCoverageProfileCodeConfirmationTransaction(t *testing.T) {
	application, database := newTestApplication(t)
	ctx := context.Background()
	if err := application.applyProfileCodeConfirmation(ctx, EmailConfirmation{}); err == nil {
		t.Fatal("empty profile change confirmation was accepted")
	}
	confirmation := EmailConfirmation{Token: "missing", Domain: "example.org", CurrentEmail: "old@example.org", Email: "new@example.org"}
	if err := application.applyProfileCodeConfirmation(ctx, confirmation); err == nil || !strings.Contains(err.Error(), "already consumed") {
		t.Fatalf("missing confirmation error = %v", err)
	}
	if _, err := database.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,?)`, "example.org", "old@example.org", "old-hash", 1); err != nil {
		t.Fatal(err)
	}
	confirmation.Token = "valid"
	confirmation.Password = "new-hash"
	confirmation.ExpiresAt = time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	if _, err := database.Exec(`INSERT INTO email_confirmations(token,domain,action,email,password,current_email,expires_at) VALUES(?,?,?,?,?,?,?)`, confirmation.Token, confirmation.Domain, "profile_password", confirmation.Email, confirmation.Password, confirmation.CurrentEmail, confirmation.ExpiresAt); err != nil {
		t.Fatal(err)
	}
	if err := application.applyProfileCodeConfirmation(ctx, confirmation); err != nil {
		t.Fatalf("apply profile confirmation: %v", err)
	}
	var email, password string
	if err := database.QueryRow(`SELECT email,password FROM users WHERE domain=?`, confirmation.Domain).Scan(&email, &password); err != nil {
		t.Fatal(err)
	}
	if email != confirmation.Email || password != confirmation.Password {
		t.Fatalf("updated account = %q %q", email, password)
	}
}
