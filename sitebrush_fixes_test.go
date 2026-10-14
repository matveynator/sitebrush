package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/matveynator/sitebrush/v2/pkg/accountauth"
	"github.com/matveynator/sitebrush/v2/pkg/aicapability"
	browserstats "github.com/matveynator/sitebrush/v2/pkg/analytics"
	"github.com/matveynator/sitebrush/v2/pkg/channelacme"
	"github.com/matveynator/sitebrush/v2/pkg/demo"
	"github.com/matveynator/sitebrush/v2/pkg/hostingandsupport"
	"github.com/matveynator/sitebrush/v2/pkg/imageprepare"
	htmlparser "golang.org/x/net/html"
)

func TestAnalyticsServerRulesProduceBrowserConversion(t *testing.T) {
	application, _ := newTestApplication(t)
	goals := []browserstats.Goal{{Name: "Buy", Kind: "uri", Match: "/offer?plan=small#buy", Primary: true}, {Name: "Buy path", Kind: "path", Match: "/offer?plan=small#buy"}}
	encoded, _ := json.Marshal(goals)
	result := application.analyticsStorageExchange(browserstats.StorageRequest{Operation: browserstats.SaveConfiguration, Domain: "localhost", State: string(encoded)})
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	payload := application.analyticsURIRules("localhost")
	command := exec.Command("node", "scripts/analytics/uri_rules_test.cjs", payload)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("browser rules: %v: %s", err, output)
	}
	var envelopes []browserstats.Event
	if err = json.Unmarshal(output, &envelopes); err != nil {
		t.Fatal(err, string(output))
	}
	site := browserstats.New(time.Now())
	site.Goals = goals
	matches := 0
	for _, event := range envelopes {
		for _, signal := range event.Signals {
			if signal.Kind == "goal" {
				matches++
				if signal.Name != browserstats.URIEventName(goals[0].Match) {
					t.Fatal("wrong URI signal")
				}
			}
		}
		site.Record(event, time.Now(), 8<<20)
	}
	if matches != 1 {
		t.Fatalf("URI signals=%d, want 1", matches)
	}
	report := site.Report(time.Now(), 90).Conversion
	for _, funnel := range report.Funnels {
		if funnel.Completed != 1 {
			t.Fatalf("URI signal did not complete funnel: %+v", funnel)
		}
	}
	if len(report.Funnels) != 2 {
		t.Fatalf("missing conversion funnels: %+v", report)
	}
}

func TestFinalPageSaveCreatesOneRevisionPerChangedPage(t *testing.T) {
	for _, templateEdit := range []bool{false, true} {
		t.Run(fmt.Sprint(templateEdit), func(t *testing.T) {
			application, db := newTestApplication(t)
			before := `<section class="SiteBrush-Template shared"><img src="/p/shared.png"></section><img src="/p/local.png">`
			for _, pagePath := range []string{"/", "/about"} {
				if _, err := db.Exec(`INSERT INTO pages(domain,path,title,html,published) VALUES('localhost',?,?,?,1)`, pagePath, pagePath, before); err != nil {
					t.Fatal(err)
				}
			}
			after := strings.Replace(before, "/p/local.png", "/p/replaced.png", 1)
			if templateEdit {
				after = strings.Replace(before, "/p/shared.png", "/p/replaced.png", 1)
			}
			if err := application.persistFinalPageSave(context.Background(), "localhost", "/", "/", "/", after); err != nil {
				t.Fatal(err)
			}
			if err := application.persistFinalPageSave(context.Background(), "localhost", "/", "/", "/", after); err != nil {
				t.Fatal(err)
			}
			for _, pagePath := range []string{"/", "/about"} {
				var count int
				if err := db.QueryRow(`SELECT COUNT(1) FROM revisions WHERE domain='localhost' AND page_path=?`, pagePath).Scan(&count); err != nil {
					t.Fatal(err)
				}
				expected := 1
				if pagePath == "/about" && !templateEdit {
					expected = 0
				}
				if count != expected {
					t.Fatalf("%s has %d revisions, want %d", pagePath, count, expected)
				}
			}
		})
	}
}

func TestDemoRefreshReplacesPageSetAndFirstImportCreatesDatabase(t *testing.T) {
	previousClient := newGrabHTTPClient
	newGrabHTTPClient = func() *http.Client {
		return &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			html := `<!doctype html><html><body><a href="/about">About</a><p>Home</p></body></html>`
			if strings.HasPrefix(request.URL.Path, "/about") {
				html = `<!doctype html><html><body><p>About</p></body></html>`
			}
			return fakeGrabResponse{contentType: "text/html", body: html}.httpResponse(request), nil
		})}
	}
	defer func() { newGrabHTTPClient = previousClient }()
	application := newRouterTestApplication(t)
	control := setupBillingOwnerForTest(t, application, "owner.example", "owner@example.com", true)
	defer control.Close()
	domain := "demo-clean.example"
	settings := demo.Settings{Domain: domain, Enabled: true, SourceURL: "https://template.example/", CopyWholeSite: true, MaxSiteBytes: 8 << 20}
	if _, _, err := application.ensureDemoSiteReady(context.Background(), settings, true, ""); err != nil {
		t.Fatal(err)
	}
	domainContext := contextWithDomain(context.Background(), domain)
	if _, err := application.db.ExecContext(domainContext, `INSERT INTO pages(domain,path,title,html,published) VALUES(?,?,?, ?,1)`, domain, "/obsolete", "Old", "old page"); err != nil {
		t.Fatal(err)
	}
	application.writePublishedStaticHTML(domain, "/obsolete", "old page")
	if _, err := application.db.ExecContext(domainContext, `INSERT INTO revisions(domain,page_path,html,created_at) VALUES(?,?,?,?)`, domain, "/obsolete", "old page", time.Now().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if _, _, err := application.ensureDemoSiteReady(context.Background(), settings, true, ""); err != nil {
			t.Fatal(err)
		}
		var count int
		if err := application.db.QueryRowContext(domainContext, `SELECT COUNT(1) FROM pages WHERE domain=?`, domain).Scan(&count); err != nil || count != 2 {
			t.Fatalf("pages=%d err=%v", count, err)
		}
		for _, table := range []string{"pages", "published_pages", "revisions"} {
			column := "path"
			if table == "revisions" {
				column = "page_path"
			}
			if err := application.db.QueryRowContext(domainContext, `SELECT COUNT(1) FROM `+table+` WHERE domain=? AND `+column+`='/obsolete'`, domain).Scan(&count); err != nil || count != 0 {
				t.Fatalf("stale %s count=%d err=%v", table, count, err)
			}
		}
		if _, err := os.Stat(filepath.Join(application.domainStaticDir(domain), "obsolete.html")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("old publication exists: %v", err)
		}
	}
}

func TestUnknownAndDeletedHostnamesOfferRegistrationOrRequest(t *testing.T) {
	for _, automatic := range []bool{false, true} {
		for _, deleted := range []bool{false, true} {
			t.Run(fmt.Sprintf("automatic=%t/deleted=%t", automatic, deleted), func(t *testing.T) {
				application := newRouterTestApplication(t)
				control := setupBillingOwnerForTest(t, application, "owner.example", "owner@example.com", automatic)
				defer control.Close()
				domain := "new.owner.example"
				if deleted {
					if err := application.createManagedSite(context.Background(), domain, "previous@example.com", "old", 1<<20); err != nil {
						t.Fatal(err)
					}
					ctx := contextWithDomain(context.Background(), domain)
					if _, err := application.db.ExecContext(ctx, `SELECT 1`); err != nil {
						t.Fatal(err)
					}
					if err := application.deleteDemoManagedSiteWithoutBackup(ctx, domain); err != nil {
						t.Fatal(err)
					}
				}
				for _, path := range []string{"/", "/old", "/?login"} {
					request := httptest.NewRequest(http.MethodGet, "https://"+domain+path, nil)
					request.AddCookie(&http.Cookie{Name: "sitebrush_session", Value: "stale-session"})
					response := httptest.NewRecorder()
					application.route(response, request)
					if response.Code != http.StatusSeeOther || !strings.HasSuffix(response.Header().Get("Location"), "?register") {
						t.Fatalf("status=%d location=%q body=%s", response.Code, response.Header().Get("Location"), response.Body.String())
					}
				}
				request := httptest.NewRequest(http.MethodGet, "https://"+domain+"/?register", nil)
				response := httptest.NewRecorder()
				application.route(response, request)
				expected := `action="?register"`
				if !automatic {
					expected = `action="?site_request"`
				}
				if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), expected) {
					t.Fatalf("wrong onboarding: %d %s", response.Code, response.Body.String())
				}
				databasePath := filepath.Join(siteDatabaseRootPath(application.serverControlDBPath()), domainStorageName(domain)+".db")
				if _, err := os.Stat(databasePath); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("opening registration created DB: %v", err)
				}
				if !automatic {
					plan, _ := (hostingandsupport.Store{DB: control}).DefaultPlan(context.Background())
					form := url.Values{"name": {"New owner"}, "email": {"new@example.com"}, "phone": {"123"}, "plan_id": {fmt.Sprint(plan.ID)}}
					request = httptest.NewRequest(http.MethodPost, "https://"+domain+"/?site_request", strings.NewReader(form.Encode()))
					request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
					response = httptest.NewRecorder()
					application.route(response, request)
					var count int
					if err := control.QueryRow(`SELECT COUNT(1) FROM site_registration_requests WHERE domain=?`, domain).Scan(&count); err != nil || count != 1 {
						t.Fatalf("request count=%d err=%v status=%d body=%s", count, err, response.Code, response.Body.String())
					}
					if _, err := os.Stat(databasePath); !errors.Is(err, os.ErrNotExist) {
						t.Fatalf("request created DB: %v", err)
					}
				}
			})
		}
	}
}

func TestPreparedImageUploadStoresActualCropAndSinglePageRevision(t *testing.T) {
	application, db := newTestApplication(t)
	if _, err := db.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES('localhost','owner@example.com','old',1)`); err != nil {
		t.Fatal(err)
	}
	original := `<img id="icon" src="/p/old.png" width="50" height="50">`
	if _, err := db.Exec(`INSERT INTO pages(domain,path,title,html,published) VALUES('localhost','/','/',?,1)`, original); err != nil {
		t.Fatal(err)
	}
	cookie := newAdminSessionCookie(t, application, "owner@example.com")
	source := image.NewNRGBA(image.Rect(0, 0, 1000, 500))
	for y := 0; y < 500; y++ {
		for x := 0; x < 1000; x++ {
			source.SetNRGBA(x, y, color.NRGBA{G: 255, A: 255})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, source); err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	multipartWriter := multipart.NewWriter(&body)
	part, err := multipartWriter.CreateFormFile("upload_files", "replacement.png")
	if err != nil {
		t.Fatal(err)
	}
	part.Write(encoded.Bytes())
	for field, text := range map[string]string{"action": "prepare_image", "image_width": "50", "image_height": "50", "image_fit": "cover", "image_position_x": "0.5", "image_position_y": "0.5", "image_full": "0"} {
		multipartWriter.WriteField(field, text)
	}
	multipartWriter.Close()
	request := httptest.NewRequest(http.MethodPost, "http://localhost/?files&prepare_image", &body)
	request.Header.Set("Content-Type", multipartWriter.FormDataContentType())
	request.Header.Set("Accept", "application/json")
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	application.route(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("upload=%d %s", response.Code, response.Body.String())
	}
	var prepared preparedImageReplacementView
	if err = json.Unmarshal(response.Body.Bytes(), &prepared); err != nil {
		t.Fatal(err)
	}
	if prepared.Full != nil || prepared.Preview.Width != 100 || prepared.Preview.Height != 100 {
		t.Fatalf("unexpected stored image: %+v", prepared)
	}
	content, err := os.ReadFile(filepath.Join(application.domainFilesDirForDomain("localhost"), filepath.Base(prepared.Preview.URL)))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := png.Decode(bytes.NewReader(content))
	if err != nil || decoded.Bounds() != image.Rect(0, 0, 100, 100) {
		t.Fatalf("stored crop bounds err=%v", err)
	}
	form := url.Values{"path": {"/"}, "title": {"/"}, "html": {strings.Replace(original, "/p/old.png", prepared.Preview.URL, 1)}}
	request = httptest.NewRequest(http.MethodPost, "http://localhost/?save", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(cookie)
	response = httptest.NewRecorder()
	application.route(response, request)
	if response.Code != http.StatusFound {
		t.Fatalf("save=%d %s", response.Code, response.Body.String())
	}
	var count int
	if err = db.QueryRow(`SELECT COUNT(1) FROM revisions`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("revisions=%d err=%v", count, err)
	}
}

func TestCreationLimitsAreIndependentAndPreserveExistingSites(t *testing.T) {
	application := newRouterTestApplication(t)
	control := setupBillingOwnerForTest(t, application, "owner.example", "owner@example.com", true)
	defer control.Close()
	store := hostingandsupport.Store{DB: control}
	defaults := store.SiteSizeLimits(context.Background())
	if defaults.Registration != 1<<30 || defaults.TestDrive != 1<<30 {
		t.Fatalf("unexpected defaults %+v", defaults)
	}
	if err := application.createManagedSite(context.Background(), "existing.example", "owner@example.com", "old", 9<<20); err != nil {
		t.Fatal(err)
	}
	limits := hostingandsupport.SiteSizeLimits{Registration: 3 << 20, TestDrive: 2 << 20}
	if err := store.SaveRegistrationSettingsWithLimits(context.Background(), true, true, limits); err != nil {
		t.Fatal(err)
	}
	if err := (demo.Store{DB: control}).SaveSettingsWithLimit(context.Background(), "demo.example", "", false, true, 1<<20); err != nil {
		t.Fatal(err)
	}
	if got := application.creationSizeLimits(context.Background()); got != limits {
		t.Fatalf("limits changed %+v", got)
	}
	if got := (demo.Store{DB: control}).Settings(context.Background()).MaxSiteBytes; got != 1<<20 {
		t.Fatalf("demo limit=%d", got)
	}
	ctx := contextWithDomain(context.Background(), "existing.example")
	if got := application.storedDomainStorageUsage(ctx, "existing.example").LimitBytes; got != 9<<20 {
		t.Fatalf("old quota changed to %d", got)
	}
	if got := application.registrationCreationQuota(context.Background()); got != limits.Registration {
		t.Fatalf("registration limit=%d", got)
	}
	if got := hostingandsupport.CreationQuota(limits.TestDrive, 512<<10); got != 512<<10 {
		t.Fatal("smaller tariff ignored")
	}
	if _, err := parseCreationSize("-1gb", 1<<30); err == nil {
		t.Fatal("negative limit admitted")
	}
	invalid := limits
	invalid.TestDrive = -1
	if err := store.SaveRegistrationSettingsWithLimits(context.Background(), false, false, invalid); err == nil {
		t.Fatal("invalid policy accepted")
	}
	if !store.AutomaticRegistrationAllowed(context.Background()) || store.SiteSizeLimits(context.Background()) != limits {
		t.Fatal("invalid policy partially saved")
	}
	if err := application.createManagedSiteWithoutAdmin(context.Background(), "trial.example", limits.TestDrive); err != nil {
		t.Fatal(err)
	}
	ctx = contextWithDomain(context.Background(), "trial.example")
	filename := filepath.Join(application.domainFilesDirForDomain("trial.example"), "quota.bin")
	if err := application.applyDomainStorageDelta(ctx, "trial.example", 0, 0, 0, limits.TestDrive, 0); err != nil {
		t.Fatal(err)
	}
	if err := application.writeFileInsideStorage(filename, make([]byte, limits.TestDrive), 0600); err != nil {
		t.Fatal(err)
	}
	if err := application.applyDomainStorageDelta(ctx, "trial.example", 0, 0, 0, 1, 0); err == nil {
		t.Fatal("write exceeded configured limit")
	}
}

func TestRecreatedSiteKeepsCertificateAndRejectsExpiredCache(t *testing.T) {
	application := newRouterTestApplication(t)
	control := setupBillingOwnerForTest(t, application, "owner.example", "owner@example.com", true)
	defer control.Close()
	domain := "recreated.example.com"
	if err := application.createManagedSite(context.Background(), domain, "old@example.com", "old", 1<<20); err != nil {
		t.Fatal(err)
	}
	cacheDirectory := filepath.Join(application.storageRootDir(), "letsencrypt")
	if err := application.mkdirAllInsideStorage(cacheDirectory, 0700); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	certificatePEM := cachedAutoCertPEMForTest(t, domain, now.Add(-time.Hour), now.Add(90*24*time.Hour))
	filename := filepath.Join(cacheDirectory, domain)
	if err := application.writeFileInsideStorage(filename, certificatePEM, 0600); err != nil {
		t.Fatal(err)
	}
	if err := application.deleteDemoManagedSiteWithoutBackup(context.Background(), domain); err != nil {
		t.Fatal(err)
	}
	if err := application.createManagedSite(context.Background(), domain, "new@example.com", "new", 2<<20); err != nil {
		t.Fatal(err)
	}
	stored, err := os.ReadFile(filename)
	if err != nil || !bytes.Equal(stored, certificatePEM) {
		t.Fatalf("certificate changed: %v", err)
	}
	cacheContext, cancelCache := context.WithCancel(context.Background())
	defer cancelCache()
	application.autoCertCertificateCache = startAutoCertCertificateMemoryCache(cacheContext, cacheDirectory)
	result := application.prepareAutomaticSSLDomain(automaticSSLIssuerFunc(func(ctx context.Context, requestedDomain string) channelacme.IssueResult {
		t.Error("valid certificate triggered issuance")
		return channelacme.IssueResult{Err: errors.New("unexpected issuance")}
	}), domain, nil)
	if result.state != "ready" || result.certificate == nil {
		t.Fatalf("cached certificate not reused: %+v", result)
	}
	expired := cachedAutoCertPEMForTest(t, domain, now.Add(-90*24*time.Hour), now.Add(-time.Hour))
	if _, certificate, _, _ := automaticSSLCertificateCacheEntry(domain, expired, now); certificate != nil {
		t.Fatal("expired certificate reused")
	}
}

func TestProfilePaginatesAIAccessAndKeepsSessionIPOrder(t *testing.T) {
	application, db := newTestApplication(t)
	if _, err := db.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES('localhost','owner@example.com','old',1)`); err != nil {
		t.Fatal(err)
	}
	cookie := newAdminSessionCookie(t, application, "owner@example.com")
	request := httptest.NewRequest(http.MethodGet, "http://localhost/?profile", nil)
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	application.route(response, request)
	if strings.Contains(response.Body.String(), "profile-ai-card") {
		t.Fatal("empty AI card is visible")
	}
	application.aiCapabilities = aicapability.NewManager()
	defer application.aiCapabilities.Close()
	for index := 0; index < 7; index++ {
		token, _, err := application.aiCapabilities.IssueFor("localhost", "owner@example.com", []string{aicapability.ScopeRead})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = application.aiCapabilities.Exchange(token, "localhost"); err != nil {
			t.Fatal(err)
		}
	}
	for _, page := range []int{1, 2, 999} {
		request = httptest.NewRequest(http.MethodGet, fmt.Sprintf("http://localhost/?profile&ai_access_page=%d&ai_session_page=%d", page, page), nil)
		request.AddCookie(cookie)
		response = httptest.NewRecorder()
		application.route(response, request)
		body := response.Body.String()
		expected := 5
		if page > 1 {
			expected = 2
		}
		if count := strings.Count(body, `name="ai_capability_id"`); count != expected {
			t.Fatalf("AI links page %d count=%d want=%d body=%s", page, count, expected, body)
		}
		if count := strings.Count(body, `name="ai_session_id"`); count != expected*3 {
			t.Fatalf("AI sessions page %d count=%d", page, count)
		}
		sessionAt, ipAt, aiAt := strings.Index(body, "profile-sessions-card"), strings.Index(body, "profile-admin-ip-card"), strings.Index(body, "profile-ai-card")
		if !(sessionAt < ipAt && ipAt < aiAt) {
			t.Fatal("sessions, IP and AI order changed")
		}
		document, parseErr := htmlparser.Parse(strings.NewReader(body))
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		grid := profileTestElementWithClass(document, "profile-account-grid")
		aiCard := profileTestElementWithClass(document, "profile-ai-card")
		if grid == nil || aiCard == nil || aiCard.Parent != grid {
			t.Fatal("AI card must remain inside the account grid")
		}
	}
}

func profileTestElementWithClass(root *htmlparser.Node, className string) *htmlparser.Node {
	for _, attribute := range root.Attr {
		if attribute.Key == "class" {
			for _, candidate := range strings.Fields(attribute.Val) {
				if candidate == className {
					return root
				}
			}
		}
	}
	for child := root.FirstChild; child != nil; child = child.NextSibling {
		if found := profileTestElementWithClass(child, className); found != nil {
			return found
		}
	}
	return nil
}

func TestProfileSecurityCardsShareGridAndPaginateThreeRecords(t *testing.T) {
	application, database := newTestApplication(t)
	if _, err := database.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES('localhost','owner@example.com','old',1)`); err != nil {
		t.Fatal(err)
	}
	cookie := newAdminSessionCookie(t, application, "owner@example.com")
	for index := 0; index < 6; index++ {
		if _, err := database.Exec(`INSERT INTO sessions(token,user_email,created_at,last_used_at) VALUES(?,?,?,?)`, fmt.Sprintf("profile-session-%d", index), "localhost|owner@example.com", time.Now().Add(-time.Duration(index+1)*time.Minute).UTC().Format(time.RFC3339), time.Now().Unix()); err != nil {
			t.Fatal(err)
		}
	}
	for index := 0; index < 7; index++ {
		if _, err := database.Exec(`INSERT INTO admin_allowed_ips(domain,email,client_ip,added_at) VALUES(?,?,?,?)`, "localhost", "owner@example.com", fmt.Sprintf("198.51.100.%d", index+1), index); err != nil {
			t.Fatal(err)
		}
	}
	scenarios := []struct {
		sessionPage, ipPage int
		sessions, ips       int
	}{
		{1, 1, 3, 3},
		{2, 1, 3, 3},
		{1, 2, 3, 3},
		{3, 2, 1, 3},
		{2, 3, 3, 1},
		{999, 999, 1, 1},
	}
	for _, scenario := range scenarios {
		t.Run(fmt.Sprintf("sessions_%d_ips_%d", scenario.sessionPage, scenario.ipPage), func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, fmt.Sprintf("http://localhost/?profile&session_page=%d&ip_page=%d", scenario.sessionPage, scenario.ipPage), nil)
			request.AddCookie(cookie)
			response := httptest.NewRecorder()
			application.route(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("profile status=%d", response.Code)
			}
			body := response.Body.String()
			if count := strings.Count(body, `name="session_token"`); count != scenario.sessions {
				t.Fatalf("sessions=%d want=%d", count, scenario.sessions)
			}
			if count := strings.Count(body, `name="profile_action" value="admin_ip_remove"`); count != scenario.ips {
				t.Fatalf("IPs=%d want=%d", count, scenario.ips)
			}
			document, err := htmlparser.Parse(strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			grid := profileTestElementWithClass(document, "profile-account-grid")
			sessionsCard := profileTestElementWithClass(document, "profile-sessions-card")
			ipCard := profileTestElementWithClass(document, "profile-admin-ip-card")
			if grid == nil || sessionsCard == nil || ipCard == nil || sessionsCard.Parent != grid || ipCard.Parent != grid {
				t.Fatal("sessions and IP cards must be direct children of the same grid")
			}
			nextCard := sessionsCard.NextSibling
			for nextCard != nil && nextCard.Type != htmlparser.ElementNode {
				nextCard = nextCard.NextSibling
			}
			if nextCard != ipCard {
				t.Fatal("IP card must immediately follow the sessions card")
			}
			for _, card := range []*htmlparser.Node{sessionsCard, ipCard} {
				pager := profileTestElementWithClass(card, "profile-pagination")
				if pager == nil {
					t.Fatal("each long security list needs its own pager")
				}
				for link := pager.FirstChild; link != nil; link = link.NextSibling {
					for _, attribute := range link.Attr {
						if attribute.Key != "href" {
							continue
						}
						linkURL, parseErr := url.Parse(attribute.Val)
						if parseErr != nil {
							t.Fatal(parseErr)
						}
						preservedParameter, preservedPage := "ip_page", scenario.ipPage
						if card == ipCard {
							preservedParameter, preservedPage = "session_page", scenario.sessionPage
						}
						if linkURL.Query().Get(preservedParameter) != fmt.Sprint(preservedPage) {
							t.Fatal("pagination must preserve the other list's page")
						}
					}
				}
			}
		})
	}
	if _, err := database.Exec(`DELETE FROM sessions WHERE token IN ('profile-session-2','profile-session-3','profile-session-4','profile-session-5')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`DELETE FROM admin_allowed_ips WHERE added_at>=3`); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "http://localhost/?profile", nil)
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	application.route(response, request)
	if strings.Contains(response.Body.String(), "profile-pagination") {
		t.Fatal("three records must fit without pagination")
	}
}

func TestProfileAdminIPUpdatesKeepSettingsAndShowValidation(t *testing.T) {
	application, database := newTestApplication(t)
	if _, err := database.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES('localhost','owner@example.com','old',1)`); err != nil {
		t.Fatal(err)
	}
	cookie := newAdminSessionCookie(t, application, "owner@example.com")
	target := "http://localhost/folder/page?profile&ip_page=2&session_page=2"
	baseRequest := httptest.NewRequest(http.MethodGet, target, nil)
	baseRequest.AddCookie(cookie)
	scenarios := []struct {
		address, errorKey string
		statusCode        int
	}{
		{"999.1.2.3", "admin_ip_error_invalid", http.StatusBadRequest},
		{"1.2.3.4", "", http.StatusSeeOther},
		{"1.2.3.0/24", "", http.StatusSeeOther},
		{"1.2.3.4", "", http.StatusSeeOther},
		{"192.0.2.1", "", http.StatusSeeOther},
		{"192.0.2.1", "", http.StatusSeeOther},
	}
	for _, scenario := range scenarios {
		form := url.Values{"profile_action": {"admin_ip_add"}, "admin_ip": {scenario.address}, "account_csrf": {accountCSRF(baseRequest)}}
		request := httptest.NewRequest(http.MethodPost, target, strings.NewReader(form.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		request.Header.Set("Accept-Language", "ru")
		request.AddCookie(cookie)
		response := httptest.NewRecorder()
		application.route(response, request)
		if response.Code != scenario.statusCode {
			t.Fatalf("add %s status=%d body=%s", scenario.address, response.Code, response.Body.String())
		}
		if scenario.errorKey != "" {
			if !strings.Contains(response.Header().Get("Content-Type"), "text/html") || strings.Contains(response.Body.String(), "account update unavailable") {
				t.Fatal("validation must render the account form as HTML")
			}
			document, err := htmlparser.Parse(strings.NewReader(response.Body.String()))
			if err != nil {
				t.Fatal(err)
			}
			ipCard := profileTestElementWithClass(document, "profile-admin-ip-card")
			if ipCard == nil {
				t.Fatal("validation lost the IP settings card")
			}
			var cardHTML strings.Builder
			if err := htmlparser.Render(&cardHTML, ipCard); err != nil {
				t.Fatal(err)
			}
			message := translationsForRequest(request)[scenario.errorKey]
			if message == "" || !strings.Contains(cardHTML.String(), message) || !strings.Contains(cardHTML.String(), `value="`+scenario.address+`"`) {
				t.Fatal("IP validation must explain the failure inside the card and retain input")
			}
			continue
		}
		location, err := url.Parse(response.Header().Get("Location"))
		if err != nil || location.Path != "/folder/page" || !location.Query().Has("profile") || location.Fragment != "ProfileAdminIPs" || location.Query().Get("ip_page") != "2" || location.Query().Get("session_page") != "2" {
			t.Fatalf("IP update lost settings location: %s err=%v", response.Header().Get("Location"), err)
		}
		followRequest := httptest.NewRequest(http.MethodGet, "http://localhost"+response.Header().Get("Location"), nil)
		followRequest.AddCookie(cookie)
		followResponse := httptest.NewRecorder()
		application.route(followResponse, followRequest)
		if followResponse.Code != http.StatusOK || !strings.Contains(followResponse.Body.String(), `id="ProfileAdminIPs"`) {
			t.Fatal("IP update redirect must return to administrator IP settings")
		}
	}
	var count int
	if err := database.QueryRow(`SELECT COUNT(1) FROM admin_allowed_ips WHERE domain='localhost' AND email='owner@example.com' AND client_ip='192.0.2.1'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("repeated current IP addition must be idempotent: count=%d err=%v", count, err)
	}
	if err := database.QueryRow(`SELECT COUNT(1) FROM admin_allowed_ips WHERE domain='localhost' AND email='owner@example.com' AND client_ip IN ('1.2.3.4','1.2.3.0/24')`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("manual IP and subnet not stored: count=%d err=%v", count, err)
	}
	request := httptest.NewRequest(http.MethodGet, "http://localhost/?profile", nil)
	if target := profileFormAction(request); target != "/?profile" {
		t.Fatalf("unpaginated profile action must contain an absolute local path: %s", target)
	}
}

func TestLANPasswordLoginAcceptsFormWithStaleCookieAndOpaqueOrigin(t *testing.T) {
	t.Setenv("SITEBRUSH_SERVICE_MAIL_MODE", "local")
	for _, cookieState := range []string{"revoked", "expired"} {
		t.Run(cookieState, func(t *testing.T) {
			application, database := newTestApplication(t)
			captureImmediateProfileEmail(t, application)
			const domain, email, clientIP = "192.168.1.227", "owner@example.com", "192.168.1.20"
			if _, err := database.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, domain, email, "correct-password"); err != nil {
				t.Fatal(err)
			}
			staleCookie := &http.Cookie{Name: "sitebrush_session", Value: "stale-session"}
			if cookieState == "expired" {
				if _, err := database.Exec(`INSERT INTO sessions(token,user_email,created_at,client_ip,last_used_at) VALUES(?,?,?,?,?)`, staleCookie.Value, domain+"|"+email, time.Now().Add(-2*accountauth.SessionTTL).UTC().Format(time.RFC3339), clientIP, time.Now().Add(-2*accountauth.SessionTTL).Unix()); err != nil {
					t.Fatal(err)
				}
			}
			handler := application.securityResponseHeadersMiddleware(http.HandlerFunc(application.route))
			getRequest := httptest.NewRequest(http.MethodGet, "https://"+domain+"/?login&return_path=%2Fdocs%3Fprofile", nil)
			getRequest.RemoteAddr = clientIP + ":1234"
			getRequest.AddCookie(staleCookie)
			getResponse := httptest.NewRecorder()
			handler.ServeHTTP(getResponse, getRequest)
			if getResponse.Code != http.StatusOK || getResponse.Header().Get("Referrer-Policy") != "no-referrer" {
				t.Fatalf("login form status=%d body=%s", getResponse.Code, getResponse.Body.String())
			}
			browserCookie := staleCookie
			for _, responseCookie := range getResponse.Result().Cookies() {
				if responseCookie.Name == staleCookie.Name && responseCookie.MaxAge < 0 {
					browserCookie = nil
				}
			}
			document, err := htmlparser.Parse(strings.NewReader(getResponse.Body.String()))
			if err != nil {
				t.Fatal(err)
			}
			loginForm := profileTestElementWithClass(document, "authentication-card")
			if loginForm == nil || loginForm.Data != "form" {
				t.Fatal("login form is missing")
			}
			form := url.Values{}
			var collectInputs func(*htmlparser.Node)
			collectInputs = func(element *htmlparser.Node) {
				if element.Data == "input" {
					inputName, inputValue := "", ""
					for _, attribute := range element.Attr {
						switch attribute.Key {
						case "name":
							inputName = attribute.Val
						case "value":
							inputValue = attribute.Val
						}
					}
					if inputName != "" {
						form.Set(inputName, inputValue)
					}
				}
				for child := element.FirstChild; child != nil; child = child.NextSibling {
					collectInputs(child)
				}
			}
			collectInputs(loginForm)
			form.Set("email", email)
			form.Set("password", "correct-password")
			postRequest := httptest.NewRequest(http.MethodPost, "https://"+domain+"/?login", strings.NewReader(form.Encode()))
			postRequest.RemoteAddr = getRequest.RemoteAddr
			postRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			postRequest.Header.Set("Origin", "null")
			if browserCookie != nil {
				postRequest.AddCookie(browserCookie)
			}
			postResponse := httptest.NewRecorder()
			handler.ServeHTTP(postResponse, postRequest)
			if postResponse.Code != http.StatusOK || !strings.Contains(postResponse.Body.String(), `name="login_challenge"`) {
				t.Fatalf("LAN login status=%d location=%s body=%s", postResponse.Code, postResponse.Header().Get("Location"), postResponse.Body.String())
			}
			code := ""
			select {
			case delivery := <-application.emailDelivery:
				for _, word := range strings.Fields(delivery.Message.Body) {
					if isSixDigitCode(word) {
						code = word
						break
					}
				}
			default:
				t.Fatal("password login did not send the verification code")
			}
			if code == "" {
				t.Fatal("verification email has no code")
			}
			codeDocument, err := htmlparser.Parse(strings.NewReader(postResponse.Body.String()))
			if err != nil {
				t.Fatal(err)
			}
			codeForm := profileTestElementWithClass(codeDocument, "authentication-card")
			if codeForm == nil || codeForm.Data != "form" {
				t.Fatal("verification form is missing")
			}
			form = url.Values{}
			collectInputs(codeForm)
			form.Set("login_code", code)
			verifyRequest := httptest.NewRequest(http.MethodPost, "https://"+domain+"/?login", strings.NewReader(form.Encode()))
			verifyRequest.RemoteAddr = getRequest.RemoteAddr
			verifyRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			verifyRequest.Header.Set("Origin", "null")
			if browserCookie != nil {
				verifyRequest.AddCookie(browserCookie)
			}
			postResponse = httptest.NewRecorder()
			handler.ServeHTTP(postResponse, verifyRequest)
			location, err := url.Parse(postResponse.Header().Get("Location"))
			if postResponse.Code != http.StatusFound || err != nil || !location.Query().Has("profile") || location.Query().Get("return_path") != "/docs?profile" {
				t.Fatalf("LAN verification status=%d location=%s body=%s", postResponse.Code, postResponse.Header().Get("Location"), postResponse.Body.String())
			}
			cookies := postResponse.Result().Cookies()
			if len(cookies) != 1 || cookies[0].Name != "sitebrush_session" || cookies[0].Value == staleCookie.Value || !cookies[0].Secure || !cookies[0].HttpOnly {
				t.Fatal("LAN login must replace the stale cookie with a secure authenticated session")
			}
			profileRequest := httptest.NewRequest(http.MethodGet, "https://"+domain+"/?profile", nil)
			profileRequest.RemoteAddr = getRequest.RemoteAddr
			profileRequest.AddCookie(cookies[0])
			if !application.isAdminRequest(profileRequest) {
				t.Fatal("LAN login session did not authenticate the administrator")
			}
			totpResponse := httptest.NewRecorder()
			application.renderAccountTOTP(totpResponse, getRequest, "totp-challenge", "")
			if !strings.Contains(totpResponse.Body.String(), `name="account_csrf" value="`+accountCSRF(getRequest)+`"`) {
				t.Fatal("TOTP form must carry the cookie-bound CSRF token too")
			}
			for _, csrfToken := range []string{"", "wrong-token"} {
				if browserCookie == nil {
					break
				}
				form.Set("account_csrf", csrfToken)
				rejectedRequest := httptest.NewRequest(http.MethodPost, "https://"+domain+"/?login", strings.NewReader(form.Encode()))
				rejectedRequest.RemoteAddr = getRequest.RemoteAddr
				rejectedRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				rejectedRequest.Header.Set("Origin", "null")
				rejectedRequest.AddCookie(browserCookie)
				rejectedResponse := httptest.NewRecorder()
				handler.ServeHTTP(rejectedResponse, rejectedRequest)
				if rejectedResponse.Code != http.StatusForbidden {
					t.Fatal("opaque-origin login with a stale cookie must require the form's CSRF token")
				}
			}
		})
	}
}

func TestPageSaveRollsBackPublicationWhenAnotherPageCannotBeWritten(t *testing.T) {
	application, db := newTestApplication(t)
	before := `<section class="SiteBrush-Template shared"><p>Old</p></section>`
	after := strings.Replace(before, "Old", "New", 1)
	for _, pagePath := range []string{"/", "/about"} {
		if _, err := db.Exec(`INSERT INTO pages(domain,path,title,html,published) VALUES('localhost',?,?,?,1)`, pagePath, pagePath, before); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO published_pages(domain,path,title,html) VALUES('localhost',?,?,?)`, pagePath, pagePath, before); err != nil {
			t.Fatal(err)
		}
	}
	application.writePublishedStaticHTML("localhost", "/", before)
	if err := application.mkdirAllInsideStorage(filepath.Join(application.domainStaticDir("localhost"), "about.html"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := application.persistFinalPageSave(context.Background(), "localhost", "/", "/", "/", after); err == nil {
		t.Fatal("unwritable publication accepted")
	}
	root, err := application.readFileInsideStorage(filepath.Join(application.domainStaticDir("localhost"), "index.html"))
	if err != nil || string(root) != before {
		t.Fatalf("first publication was not restored: %q %v", root, err)
	}
	var html string
	for _, table := range []string{"pages", "published_pages"} {
		if err := db.QueryRow(`SELECT html FROM ` + table + ` WHERE domain='localhost' AND path='/'`).Scan(&html); err != nil || html != before {
			t.Fatalf("%s did not roll back", table)
		}
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(1) FROM revisions`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed save created revisions: %d %v", count, err)
	}
}

func TestDemoSnapshotRestoreHonorsNewQuotaBeforeReplacingContent(t *testing.T) {
	application := newRouterTestApplication(t)
	control := setupBillingOwnerForTest(t, application, "owner.example", "owner@example.com", true)
	defer control.Close()
	domain := "snapshot-limit.example"
	settings := demo.Settings{Domain: domain, Enabled: true, MaxSiteBytes: 4 << 20}
	if _, _, err := application.ensureDemoSiteReady(context.Background(), settings, true, ""); err != nil {
		t.Fatal(err)
	}
	ctx := contextWithDomain(context.Background(), domain)
	large := `<html><body>` + strings.Repeat("working page ", 30000) + `</body></html>`
	if err := application.persistFinalPageSave(ctx, domain, "/", "/", "Home", large); err != nil {
		t.Fatal(err)
	}
	if err := application.createDemoSiteSnapshot(ctx, domain); err != nil {
		t.Fatal(err)
	}
	if err := application.initializeRegistrationQuota(ctx, domain, 32<<10); err != nil {
		t.Fatal(err)
	}
	if err := application.restoreDemoSiteFromSnapshot(ctx, domain); err == nil {
		t.Fatal("oversized snapshot bypassed demo quota")
	}
	var retained string
	if err := application.db.QueryRowContext(ctx, `SELECT html FROM pages WHERE domain=? AND path='/'`, domain).Scan(&retained); err != nil || retained != large {
		t.Fatal("failed restore removed working content")
	}
}

func TestConcurrentIdenticalPageSavesCreateOneRevision(t *testing.T) {
	application := newRouterTestApplication(t)
	domain := "save-concurrency.example"
	if err := application.createManagedSite(context.Background(), domain, "owner@example.com", "old", 8<<20); err != nil {
		t.Fatal(err)
	}
	ctx := contextWithDomain(context.Background(), domain)
	if _, err := application.db.ExecContext(ctx, `INSERT INTO pages(domain,path,title,html,published) VALUES(?, '/', 'Home', '<p>Old</p>',1)`, domain); err != nil {
		t.Fatal(err)
	}
	replies := make(chan error, 12)
	for attempt := 0; attempt < 12; attempt++ {
		go func() { replies <- application.persistFinalPageSave(ctx, domain, "/", "/", "Home", "<p>New</p>") }()
	}
	for attempt := 0; attempt < 12; attempt++ {
		select {
		case err := <-replies:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("save worker blocked")
		}
	}
	var count int
	if err := application.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM revisions WHERE domain=?`, domain).Scan(&count); err != nil || count != 1 {
		t.Fatalf("concurrent saves created %d revisions err=%v", count, err)
	}
}

func TestPreparedImageDeduplicatesPreviewAndFullStorage(t *testing.T) {
	application, db := newTestApplication(t)
	file := imageprepare.File{Content: []byte("same immutable image"), MIME: "image/png", Extension: ".png", Width: 100, Height: 100}
	request := httptest.NewRequest(http.MethodPost, "http://localhost/?files", nil)
	result, err := application.storePreparedImageReplacement(request, "/", imageprepare.Result{Preview: file, Full: &file})
	if err != nil {
		t.Fatal(err)
	}
	if result.Full == nil || result.Preview.URL != result.Full.URL {
		t.Fatal("identical image was stored twice")
	}
	var storedBytes int64
	if err = db.QueryRow(`SELECT file_bytes FROM domain_storage_usage WHERE domain='localhost'`).Scan(&storedBytes); err != nil || storedBytes != int64(len(file.Content)) {
		t.Fatalf("quota charged %d bytes err=%v", storedBytes, err)
	}
}

func TestMissingRegisteredDatabaseDoesNotOfferNewOwnership(t *testing.T) {
	application := newRouterTestApplication(t)
	control := setupBillingOwnerForTest(t, application, "owner.example.com", "owner@example.com", true)
	defer control.Close()
	for _, domain := range []string{"owner.example.com", "paid.example.com"} {
		if domain == "paid.example.com" {
			store := hostingandsupport.Store{DB: control}
			plan, _ := store.DefaultPlan(context.Background())
			if err := store.AssignSite(context.Background(), domain, plan.ID, "paid"); err != nil {
				t.Fatal(err)
			}
		}
		for _, query := range []string{"", "?register"} {
			request := httptest.NewRequest(http.MethodGet, "https://"+domain+"/"+query, nil)
			response := httptest.NewRecorder()
			application.route(response, request)
			if response.Code != http.StatusServiceUnavailable || response.Header().Get("Location") != "" {
				t.Fatalf("registered missing DB offered ownership: %d %s", response.Code, response.Body.String())
			}
		}
	}
}

func TestClosedRegistrationRejectsPreviouslyIssuedConfirmation(t *testing.T) {
	application := newRouterTestApplication(t)
	confirmationContext, stopConfirmations := context.WithCancel(context.Background())
	defer stopConfirmations()
	application.registrationConfirmations = startEmailConfirmationMemoryWorker(confirmationContext)
	control := setupBillingOwnerForTest(t, application, "owner.example", "owner@example.com", false)
	defer control.Close()
	domain := "pending.example.com"
	confirmation := EmailConfirmation{Token: "pending-registration", Domain: domain, Email: "new@example.com", Action: "register", ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339)}
	if err := application.saveRegistrationConfirmation(context.Background(), confirmation); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "https://"+domain+"/?email_confirm="+confirmation.Token, strings.NewReader("password=secret&password_confirm=secret"))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	application.route(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `action="?site_request"`) {
		t.Fatalf("closed registration did not offer request: %d %s", response.Code, response.Body.String())
	}
	filename := filepath.Join(siteDatabaseRootPath(application.serverControlDBPath()), domainStorageName(domain)+".db")
	if _, err := os.Stat(filename); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("closed registration created site DB: %v", err)
	}
}
