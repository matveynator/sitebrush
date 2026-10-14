package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	browserstats "github.com/matveynator/sitebrush/v2/pkg/analytics"
)

func TestConversionBeaconAdmissionPrivacyAndCancellation(t *testing.T) {
	application := &App{browserAnalytics: make(chan browserAnalyticsEnvelope, 1)}
	payload := `{"version":2,"visitor":"1111111111111111","view":"2222222222222222","sequence":1,"path":"/","referrer":"https://github.com/project?token=private","signals":[{"kind":"page","at":0}],"address":"private","language":"private","timezone":"private","client":{"user_agent":"private"},"fingerprint":"private"}`
	request := httptest.NewRequest("POST", "http://localhost/_sitebrush/analytics", strings.NewReader(payload))
	request.Header.Set("Origin", "http://localhost")
	request.Header.Set("User-Agent", "Mozilla/5.0")
	response := httptest.NewRecorder()
	application.browserAnalyticsBeacon(response, request)
	if response.Code != 204 {
		t.Fatal(response.Code, response.Body.String())
	}
	event := (<-application.browserAnalytics).event
	encoded, _ := json.Marshal(event)
	if strings.Contains(string(encoded), "private") {
		t.Fatalf("metadata persisted: %s", encoded)
	}
	if event.Source != "GitHub" && event.Source != "github.com" {
		t.Fatalf("source %q", event.Source)
	}
	request = httptest.NewRequest("POST", "http://localhost/_sitebrush/analytics", strings.NewReader(payload))
	request.Header.Set("Origin", "https://external.example")
	response = httptest.NewRecorder()
	application.browserAnalyticsBeacon(response, request)
	if response.Code != 403 {
		t.Fatal("foreign origin admitted")
	}
	application.browserAnalytics <- browserAnalyticsEnvelope{}
	request = httptest.NewRequest("POST", "http://localhost/_sitebrush/analytics", strings.NewReader(payload))
	request.Header.Set("Origin", "http://localhost")
	request.Header.Set("User-Agent", "Mozilla/5.0")
	response = httptest.NewRecorder()
	application.browserAnalyticsBeacon(response, request)
	if response.Code != 503 {
		t.Fatal("full queue did not terminate admission")
	}
}

func TestConversionStealthCollectsPublicVisitsAndProtectsConfiguration(t *testing.T) {
	application, database := newTestApplication(t)
	for _, statement := range []string{
		`INSERT INTO users(domain,email,password,is_admin) VALUES('example.org','owner@example.org','old',1)`,
		`INSERT INTO admin_ip_policies(domain,email,enabled_at) VALUES('example.org','owner@example.org',1)`,
		`INSERT INTO admin_allowed_ips(domain,email,client_ip,added_at) VALUES('example.org','owner@example.org','192.0.2.1',1)`,
		`INSERT INTO admin_stealth_modes(domain,email,enabled_at) VALUES('example.org','owner@example.org',1)`,
	} {
		if _, err := database.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(application.domainStaticDir("example.org"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(application.domainStaticDir("example.org"), "index.html"), []byte(`<html><body>Public page</body></html>`), 0644); err != nil {
		t.Fatal(err)
	}
	cacheContext, stopCache := context.WithCancel(context.Background())
	defer stopCache()
	application.guestStaticHTMLCache = startGuestStaticHTMLCacheWorker(cacheContext, 1<<20)
	staticPath := filepath.Join(application.domainStaticDir("example.org"), "index.html")
	fileInfo, err := os.Stat(staticPath)
	if err != nil {
		t.Fatal(err)
	}
	menuHTML, err := application.cachedVisitorHTMLFromContent(staticPath, []byte(`<html><body>Public page</body></html>`), "/", "example.org", "", fileInfo, false)
	if err != nil || !strings.Contains(string(menuHTML), "SiteBrushContextMenu") {
		t.Fatal("normal guest cache did not contain its menu", err)
	}
	visit := httptest.NewRequest(http.MethodGet, "https://example.org/", nil)
	visit.RemoteAddr = "192.0.2.2:1234"
	response := httptest.NewRecorder()
	application.route(response, visit)
	if response.Code != 200 || strings.Count(response.Body.String(), `src="/p/static/analytics.js?v=`) != 1 || strings.Contains(response.Body.String(), "SiteBrushContextMenu") {
		t.Fatal("stealth must retain only the visitor collector", response.Body.String())
	}
	application.browserAnalytics = make(chan browserAnalyticsEnvelope, 16)
	stop := make(chan struct{})
	finished := make(chan struct{})
	go func() { defer close(finished); application.runBrowserAnalytics(stop) }()
	defer func() {
		select {
		case <-finished:
		default:
			close(stop)
			<-finished
		}
	}()
	batch := `{"version":2,"visitor":"1111111111111111","view":"2222222222222222","sequence":1,"path":"/","signals":[{"kind":"page"}]}`
	for retry := 0; retry < 2; retry++ {
		request := httptest.NewRequest(http.MethodPost, "https://example.org/_sitebrush/analytics", strings.NewReader(batch))
		request.RemoteAddr = visit.RemoteAddr
		request.Header.Set("Origin", "https://example.org")
		request.Header.Set("User-Agent", "Mozilla/5.0")
		recorder := httptest.NewRecorder()
		application.route(recorder, request)
		if recorder.Code != http.StatusNoContent {
			t.Fatal("stealth rejected visitor batch", recorder.Code, recorder.Body.String())
		}
	}
	close(stop)
	<-finished
	saved := application.analyticsStorage.Exchange(browserstats.StorageRequest{Operation: browserstats.ReadBrowserReport, Domain: "example.org"})
	if saved.Err != nil {
		t.Fatal(saved.Err)
	}
	var reports map[string]browserstats.Report
	if err := json.Unmarshal([]byte(saved.Text), &reports); err != nil {
		t.Fatal(err)
	}
	if reports["7"].Conversion.Visitors != 1 {
		t.Fatal("public visit was lost or duplicated", saved.Text)
	}
	sessionRequest := httptest.NewRequest(http.MethodGet, "https://example.org/", nil)
	sessionRequest.RemoteAddr = "192.0.2.1:1234"
	sessionResponse := httptest.NewRecorder()
	application.createSessionForDomain(sessionResponse, sessionRequest, sessionRequest.Context(), "example.org", "owner@example.org")
	visit.AddCookie(sessionResponse.Result().Cookies()[0])
	for _, statement := range []string{
		`INSERT INTO users(domain,email,password,is_admin) VALUES('example.org','second@example.org','old',1)`,
		`INSERT INTO admin_ip_policies(domain,email,enabled_at) VALUES('example.org','second@example.org',1)`,
		`INSERT INTO admin_allowed_ips(domain,email,client_ip,added_at) VALUES('example.org','second@example.org','192.0.2.2',1)`,
	} {
		if _, err := database.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	configuration := fmt.Sprintf(`{"operation":"configure","csrf":%q,"goals":[]}`, accountCSRF(visit))
	reply, handled := application.analyticsConfigurationMessage(visit, configuration)
	if !handled || !strings.Contains(reply, "forbidden") {
		t.Fatal("public collector bypassed administrator IP protection", reply)
	}
}

func TestConversionReportReadErrorIsNotEmptyTraffic(t *testing.T) {
	application := &App{analyticsStorage: permanentBrowserAnalyticsRepository{}}
	report := application.browserAnalyticsView(httptest.NewRequest(http.MethodGet, "https://example.org/?analytics", nil), "example.org")
	if !report.Error || report.Available {
		t.Fatal("failed report read appeared as valid empty statistics")
	}
}

func TestConversionPreviewCannotReadOutsidePublishedSite(t *testing.T) {
	application := &App{storagePath: t.TempDir()}
	pageDirectory := application.domainStaticDir("one.example")
	otherDirectory := application.domainStaticDir("two.example")
	for _, directory := range []string{pageDirectory, otherDirectory} {
		if err := os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(pageDirectory, "index.html"), []byte("published"), 0600); err != nil {
		t.Fatal(err)
	}
	if body, err := application.analyticsPublishedPage("one.example", "/"); err != nil || body != "published" {
		t.Fatal("published page unavailable", body, err)
	}
	for _, directory := range []string{t.TempDir(), otherDirectory} {
		secret := filepath.Join(directory, "private.html")
		if err := os.WriteFile(secret, []byte("private-content"), 0600); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(pageDirectory, "linked.html")
		if err := os.Symlink(secret, link); err != nil {
			t.Fatal(err)
		}
		if body, err := application.analyticsPublishedPage("one.example", "/linked.html"); err == nil || body != "" {
			t.Fatal("preview escaped published site", body, err)
		}
		if err := os.Remove(link); err != nil {
			t.Fatal(err)
		}
	}
}

func TestConversionConfigurationRequiresAdminAndCSRF(t *testing.T) {
	application, _ := newTestApplication(t)
	request := httptest.NewRequest("GET", "http://localhost/_sitebrush/analytics", nil)
	reply, handled := application.analyticsConfigurationMessage(request, `{"operation":"configure","goals":[]}`)
	if !handled || !strings.Contains(reply, "forbidden") {
		t.Fatal("anonymous configuration accepted")
	}
}

func TestConversionInitialGoalsUsePublishedCatalogOnce(t *testing.T) {
	application, database := newTestApplication(t)
	if _, err := database.Exec(`INSERT INTO published_pages(domain,path,title,html) VALUES('localhost','/','Home',''),('localhost','/contact','Contact','')`); err != nil {
		t.Fatal(err)
	}
	root := application.domainStaticDir("localhost")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	for filename, content := range map[string]string{"index.html": `<a href="/contact">Contact</a>`, "contact.html": `<form data-analytics-key="contact"><input value="private-secret"></form>`} {
		if err := os.WriteFile(filepath.Join(root, filename), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	request := httptest.NewRequest("GET", "http://localhost/?analytics", nil)
	catalog := application.analyticsPublishedCatalog(request, "localhost")
	if catalog.Unavailable || len(catalog.Pages) != 2 || !catalog.Pages[0].Inspected {
		t.Fatal("published catalog unavailable", catalog)
	}
	if err := application.initializeAnalyticsGoals(request, "localhost", catalog); err != nil {
		t.Fatal(err)
	}
	goals, err := application.readAnalyticsGoals("localhost")
	if err != nil || len(goals) != 2 || goals[0].Kind != "form-submit" || !goals[0].Automatic {
		t.Fatal("published form and actual route not initialized", goals, err)
	}
	result := application.analyticsStorageExchange(browserstats.StorageRequest{Operation: browserstats.SaveConfiguration, Domain: "localhost", State: `[]`})
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	if err := application.initializeAnalyticsGoals(request, "localhost", catalog); err != nil {
		t.Fatal(err)
	}
	goals, err = application.readAnalyticsGoals("localhost")
	if err != nil || len(goals) != 0 {
		t.Fatal("explicitly cleared goals were re-created", goals, err)
	}
}

func TestConversionBrowserRegressions(t *testing.T) {
	if os.Getenv("SITEBRUSH_ANALYTICS_BROWSER_TEST") != "1" {
		t.Skip("set SITEBRUSH_ANALYTICS_BROWSER_TEST=1 with scripts/editor Playwright installed")
	}
	// Session renewal and analytics quota writes must use the production SQL
	// owner rather than compete through independent raw SQLite connections.
	application := newRouterTestApplication(t)
	fixtureContext := contextWithSiteDatabaseCreation(contextWithDomain(context.Background(), "localhost"))
	store := browserstats.OpenStore(filepath.Join(application.storagePath, "analytics"))
	application.analyticsStorage = store
	t.Cleanup(store.Close)
	assets, err := buildEmbeddedStaticAssetCache(testStaticFiles(t))
	if err != nil {
		t.Fatal(err)
	}
	application.embeddedStaticAssets = assets
	if _, err = application.db.ExecContext(fixtureContext, `INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "analytics@example.com", "old"); err != nil {
		t.Fatal(err)
	}
	staticHTML := `<!doctype html><html lang="en"><head><title>Analytics fixture</title><style>section{height:600px}body{margin:20px}</style></head><body><main><section data-analytics-section="hero"><h1>Fixture</h1><a href="/download" data-analytics-key="download">Download</a><div data-analytics-key="dead">Looks clickable</div></section><section><form data-analytics-key="contact"><input id="name" name="name" value="field-secret"><input id="password" type="password" name="password" value="password-secret"><select id="plan" name="plan" data-analytics-key="plan"><option value="starter">Starter</option><option value="pro">Pro</option></select><textarea>textarea-secret</textarea><button>Submit</button></form></section></main><script>window.fixtureExecuted=true</script></body></html>`
	if err = os.MkdirAll(application.domainStaticDir("localhost"), 0755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(application.domainStaticDir("localhost"), "index.html"), []byte(staticHTML), 0644); err != nil {
		t.Fatal(err)
	}
	preview, err := browserstats.BehaviourPreview(strings.NewReader(string(buildGuestStaticHTMLBody([]byte(staticHTML), "/", "localhost", "en"))))
	if err != nil {
		t.Fatal(err)
	}
	layoutStart := strings.Index(preview, `data-analytics-layout="`) + len(`data-analytics-layout="`)
	layout := strings.Split(preview[layoutStart:], `"`)[0]
	if _, err = application.db.ExecContext(fixtureContext, `INSERT INTO published_pages(domain,path,title,html) VALUES('localhost','/','Home',''),('localhost','/docs/guide','Getting started','')`); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	state := browserstats.New(now)
	state.Goals = []browserstats.Goal{{Name: "Download", Primary: true, Kind: "event", Match: "download"}}
	event := browserstats.Event{Version: 2, Visitor: "1111111111111111", View: "2222222222222222", Sequence: 1, Path: "/", Layout: layout, Device: "desktop", Persistent: true, Signals: []browserstats.Signal{{Kind: "page"}, {Kind: "click", Element: "key:download"}, {Kind: "scroll", Scroll: 60}, {Kind: "goal", Name: "download"}}, Attribution: browserstats.Attribution{Name: "GitHub", Kind: "referral"}}
	event.Referrer = "https://github.com/sitebrush/releases?tag=v1"
	event.Address = "192.0.2.9"
	event.Location = browserstats.VisitorLocation{Address: event.Address, Known: true, Country: "GB", City: "London", Latitude: 51.5, Longitude: -0.1}
	preferences := browserstats.ReplayPreferences{Enabled: true, Sample: 5}
	sampledSession := ""
	for index := 0; index < 1000; index++ {
		candidate := fmt.Sprintf("%032x", index)
		if browserstats.ReplaySampled(candidate, preferences) {
			sampledSession = candidate
			break
		}
	}
	if sampledSession == "" {
		t.Fatal("no sampled session found")
	}
	event.Session = sampledSession
	event.ReplayAllowed = true
	event.ReplayRecording = true
	event.Replay = []browserstats.ReplayEvent{{Kind: "navigation", Width: 1280, Height: 800}, {Kind: "click", Element: "key:download", At: 1000}}
	state.Record(event, now, 4<<20)
	state.RecordPageChange(browserstats.PublicationChange("/", `<a href="/download">Download</a>`, `<a href="/download">Download for Linux</a>`, now.Add(-24*time.Hour)))
	application.captureAnalyticsReplayPages("localhost", state, nil)
	encodedPreferences, _ := json.Marshal(preferences)
	if result := application.analyticsStorage.Exchange(browserstats.StorageRequest{Operation: browserstats.SavePreferences, Domain: "localhost", State: string(encodedPreferences)}); result.Err != nil {
		t.Fatal(result.Err)
	}

	report := state.Report(now, 7)
	reports, _ := json.Marshal(map[string]browserstats.Report{"7": report, "30": report, "90": report, "1": report})
	snapshot, _ := json.Marshal(state)
	if result := application.analyticsStorage.Exchange(browserstats.StorageRequest{Operation: browserstats.SaveConfiguration, Domain: "localhost", State: analyticsGoalsJSON(state.Goals)}); result.Err != nil {
		t.Fatal(result.Err)
	}
	if result := application.analyticsStorage.Exchange(browserstats.StorageRequest{Operation: browserstats.SaveBrowser, Domain: "localhost", State: string(snapshot), Report: string(reports)}); result.Err != nil {
		t.Fatal(result.Err)
	}
	application.browserAnalytics = make(chan browserAnalyticsEnvelope, 512)
	application.analyticsConnections = make(chan struct{}, 8)
	stop := make(chan struct{})
	finished := make(chan struct{})
	go func() { defer close(finished); application.runBrowserAnalytics(stop) }()
	defer func() {
		select {
		case <-finished:
		default:
			close(stop)
			<-finished
		}
	}()
	rawBeacons := make(chan string, 8)
	fixture := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Referrer-Policy", "no-referrer")
		if request.URL.Path == "/_sitebrush/analytics" && request.Method == http.MethodPost {
			payload, readErr := io.ReadAll(io.LimitReader(request.Body, 16385))
			if readErr != nil {
				http.Error(writer, "invalid fixture payload", 400)
				return
			}
			request.Body = io.NopCloser(strings.NewReader(string(payload)))
			rawBeacons <- string(payload)
		}
		if strings.HasPrefix(request.URL.Path, "/p/static/") {
			application.serveEmbeddedStaticAsset(writer, request)
			return
		}
		application.route(writer, request)
	}))
	defer fixture.Close()
	cookie := newAdminSessionCookie(t, application, "analytics@example.com")
	command := exec.Command("node", "scripts/analytics/browser_test.cjs")
	command.Env = append(os.Environ(), "SITEBRUSH_ANALYTICS_URL="+strings.Replace(fixture.URL, "127.0.0.1", "localhost", 1), "SITEBRUSH_ANALYTICS_COOKIE="+cookie.Name+"="+cookie.Value, "SITEBRUSH_ANALYTICS_SAMPLE_SESSION="+sampledSession)
	output, err := command.CombinedOutput()
	t.Log(string(output))
	if err != nil {
		t.Fatal(err)
	}
	if len(rawBeacons) == 0 {
		t.Fatal("unload beacon did not reach the server")
	}
	for len(rawBeacons) > 0 {
		payload := <-rawBeacons
		if strings.Contains(payload, "visitor-secret") || strings.Contains(payload, "url-secret") || strings.Contains(payload, "field-secret") || strings.Contains(payload, "textarea-secret") || strings.Contains(payload, "password-secret") || strings.Contains(payload, "starter") || strings.Contains(payload, "pro") {
			t.Fatal("raw client batch contained form field contents")
		}
		var observation browserstats.Event
		if json.Unmarshal([]byte(payload), &observation) != nil {
			t.Fatal("malformed beacon")
		}
		foundSuccess := false
		foundFieldState := false
		for _, signal := range observation.Signals {
			foundSuccess = foundSuccess || signal.Kind == "form-success"
			foundFieldState = foundFieldState || signal.Kind == "field" && signal.Field == "key:plan"
			if signal.Kind == "field-value" {
				t.Fatal("form contents were emitted by collector")
			}
		}
		if len(observation.Replay) == 0 {
			t.Fatal("sampled replay absent from production batch")
		}
		replayJSON, _ := json.Marshal(observation.Replay)
		if strings.Contains(string(replayJSON), "pro") || strings.Contains(string(replayJSON), "secret") {
			t.Fatal("replay copied field values")
		}
		if !foundFieldState {
			t.Fatal("structural form field state was not recorded")
		}
		if !foundSuccess {
			t.Fatal("confirmed form outcome missing from beacon")
		}
	}
	close(stop)
	<-finished
	persisted := application.analyticsStorage.Exchange(browserstats.StorageRequest{Operation: browserstats.ReadBrowserReport, Domain: "localhost"})
	if persisted.Err != nil {
		t.Fatal(persisted.Err)
	}
	var storedReports map[string]browserstats.Report
	if err := json.Unmarshal([]byte(persisted.Text), &storedReports); err != nil {
		t.Fatal(err)
	}
	if storedReports["7"].Conversion.Visitors != 2 {
		t.Fatalf("real visitor did not reach the saved report, or batch retry duplicated it: %+v", storedReports["7"].Conversion)
	}
}

func TestAnalyticsDiskQuotaAccountingAndGrowthLimit(t *testing.T) {
	application, database := newTestApplication(t)
	store := browserstats.OpenStore(filepath.Join(application.storageRootDir(), "analytics"))
	defer store.Close()
	application.analyticsStorage = store
	application.ensureDomainStorageUsageRow(context.Background(), "localhost")
	if _, err := database.Exec(`UPDATE domain_storage_usage SET page_bytes=100,limit_bytes=1000 WHERE domain='localhost'`); err != nil {
		t.Fatal(err)
	}
	requests := make(chan browserstats.DiskQuotaRequest, 2)
	stop := make(chan struct{})
	done := make(chan struct{})
	go application.runAnalyticsDiskQuota(requests, stop, done)
	defer func() { close(stop); <-done }()
	update := func(bytes int64, final bool) error {
		reply := make(chan error, 1)
		requests <- browserstats.DiskQuotaRequest{Domain: "localhost", Bytes: bytes, Final: final, Reply: reply}
		return <-reply
	}
	if err := update(400, false); err != nil {
		t.Fatal(err)
	}
	usage := application.storedDomainStorageUsage(context.Background(), "localhost")
	if usage.AnalyticsReservedBytes != 400 || usage.totalBytes() != 100 {
		t.Fatalf("reservation counted as actual bytes: %#v", usage)
	}
	if err := update(400, true); err != nil {
		t.Fatal(err)
	}
	usage = application.storedDomainStorageUsage(context.Background(), "localhost")
	if usage.AnalyticsBytes != 400 || usage.AnalyticsReservedBytes != 0 || usage.totalBytes() != 500 {
		t.Fatalf("analytics not included separately: %#v", usage)
	}
	if err := update(950, false); err == nil {
		t.Fatal("analytics exceeded site's disk quota")
	}
	if err := update(200, true); err != nil {
		t.Fatal("cleanup cannot release quota", err)
	}
	directory := filepath.Join(application.storageRootDir(), "analytics", "site-localhost")
	if err := os.MkdirAll(directory, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "usage-fixture"), make([]byte, 200), 0600); err != nil {
		t.Fatal(err)
	}
	if err := application.applyDomainStorageDelta(context.Background(), "localhost", 900, 0, 0, 0, 0); err == nil {
		t.Fatal("page write ignored analytics disk usage")
	}
	if err := update(950, false); err != nil {
		t.Fatal(err)
	}
	application.rebuildDomainStorageUsage(context.Background(), "localhost")
	usage = application.storedDomainStorageUsage(context.Background(), "localhost")
	if usage.AnalyticsReservedBytes != 750 {
		t.Fatal("reconciliation erased a live growth reservation")
	}
}

func TestAnalyticsDiskQuotaKeepsUsageWithoutStorageWorker(t *testing.T) {
	application, database := newTestApplication(t)
	application.analyticsStorage = nil
	application.ensureDomainStorageUsageRow(context.Background(), "localhost")
	if _, err := database.Exec(`UPDATE domain_storage_usage SET analytics_bytes=200,analytics_reserved_bytes=100 WHERE domain='localhost'`); err != nil {
		t.Fatal(err)
	}
	application.rebuildDomainStorageUsage(context.Background(), "localhost")
	usage := application.storedDomainStorageUsage(context.Background(), "localhost")
	if usage.AnalyticsBytes != 200 || usage.AnalyticsReservedBytes != 100 {
		t.Fatal("worker unavailability released occupied disk quota", usage)
	}
}

func TestConversionPublicationSignalsUseSiteChannel(t *testing.T) {
	application := &App{storagePath: t.TempDir(), browserAnalytics: make(chan browserAnalyticsEnvelope, 1)}
	application.writePublishedStaticHTML("localhost", "/pricing", `<a href="/download">Download</a>`)
	if len(application.browserAnalytics) != 0 {
		t.Fatal("initial publication treated as a conversion experiment")
	}
	application.writePublishedStaticHTML("localhost", "/pricing", `<a href="/download">Download for Linux</a>`)
	change := (<-application.browserAnalytics).event.Change
	if change == nil || change.Page != "/pricing" || change.BeforeLabel != "Download" || change.AfterLabel != "Download for Linux" {
		t.Fatal("publication was not linked to analytics", change)
	}
	application.writePublishedStaticHTML("localhost", "/pricing", `<a href="/download">Download for Linux</a>`)
	if len(application.browserAnalytics) != 0 {
		t.Fatal("unchanged publication enqueued a change")
	}
}
