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
	payload := `{"version":2,"visitor":"1111111111111111","view":"2222222222222222","sequence":1,"path":"/","referrer":"https://github.com/project?token=private","signals":[{"kind":"page","at":0}],"address":"private","language":"private","timezone":"private"}`
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
	if len(event.Fingerprint) != 64 {
		t.Fatal("browser fingerprint not computed")
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

func TestConversionConfigurationRequiresAdminAndCSRF(t *testing.T) {
	application, _ := newTestApplication(t)
	request := httptest.NewRequest("GET", "http://localhost/_sitebrush/analytics", nil)
	reply, handled := application.analyticsConfigurationMessage(request, `{"operation":"configure","goals":[]}`)
	if !handled || !strings.Contains(reply, "forbidden") {
		t.Fatal("anonymous configuration accepted")
	}
}

func TestConversionBrowserRegressions(t *testing.T) {
	if os.Getenv("SITEBRUSH_ANALYTICS_BROWSER_TEST") != "1" {
		t.Skip("set SITEBRUSH_ANALYTICS_BROWSER_TEST=1 with scripts/editor Playwright installed")
	}
	application, rawDB := newTestApplication(t)
	assets, err := buildEmbeddedStaticAssetCache(testStaticFiles(t))
	if err != nil {
		t.Fatal(err)
	}
	application.embeddedStaticAssets = assets
	if _, err = rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "analytics@example.com", "old"); err != nil {
		t.Fatal(err)
	}
	staticHTML := `<!doctype html><html lang="en"><head><title>Analytics fixture</title><style>section{height:600px}body{margin:20px}</style></head><body><main><section data-analytics-section="hero"><h1>Fixture</h1><a href="/download" data-analytics-key="download">Download</a><div data-analytics-key="dead">Looks clickable</div></section><section><form data-analytics-key="contact"><input id="name" name="name" value="field-secret"><input id="password" type="password" name="password" data-analytics-value="true" value="password-secret"><select id="plan" name="plan" data-analytics-key="plan" data-analytics-value="true"><option value="starter">Starter</option><option value="pro">Pro</option></select><textarea>textarea-secret</textarea><button>Submit</button></form></section></main><script>window.fixtureExecuted=true</script></body></html>`
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
	if _, err = rawDB.Exec(`INSERT INTO published_pages(domain,path,title,html) VALUES('localhost','/','Home',''),('localhost','/docs/guide','Getting started','')`); err != nil {
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
	rawBeacons := make(chan string, 8)
	fixture := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
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
		if strings.Contains(payload, "visitor-secret") || strings.Contains(payload, "url-secret") || strings.Contains(payload, "field-secret") || strings.Contains(payload, "textarea-secret") || strings.Contains(payload, "password-secret") {
			t.Fatal("raw client batch contained field contents or a secret")
		}
		var observation browserstats.Event
		if json.Unmarshal([]byte(payload), &observation) != nil {
			t.Fatal("malformed beacon")
		}
		foundSuccess := false
		for _, signal := range observation.Signals {
			foundSuccess = foundSuccess || signal.Kind == "form-success"
		}
		if len(observation.Replay) == 0 {
			t.Fatal("sampled replay absent from production batch")
		}
		replayJSON, _ := json.Marshal(observation.Replay)
		if strings.Contains(string(replayJSON), "pro") || strings.Contains(string(replayJSON), "secret") {
			t.Fatal("replay copied field values")
		}
		foundSafeField := false
		for _, signal := range observation.Signals {
			foundSafeField = foundSafeField || signal.Kind == "field-value" && signal.Field == "key:plan" && signal.FieldText == "pro"
		}
		if !foundSafeField {
			t.Fatal("explicitly marked safe field was not recorded")
		}
		if !foundSuccess {
			t.Fatal("confirmed form outcome missing from beacon")
		}
	}
	for len(application.browserAnalytics) > 0 {
		observed := (<-application.browserAnalytics).event
		encoded, _ := json.Marshal(observed)
		if strings.Contains(string(encoded), "visitor-secret") || strings.Contains(string(encoded), "url-secret") {
			t.Fatal("collector stored a secret")
		}
		if observed.Version == 2 && (len(observed.Fingerprint) != 64 || observed.Client.UserAgent == "") {
			t.Fatal("fingerprint/browser characteristics missing")
		}
		if observed.Version == 2 && observed.Layout != layout {
			t.Fatalf("collector/preview layout mismatch: %s != %s", observed.Layout, layout)
		}
	}
}

func TestAnalyticsDiskQuotaAccountingAndGrowthLimit(t *testing.T) {
	application, database := newTestApplication(t)
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
