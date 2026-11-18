package publicsite

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/matveynator/sitebrush/v2/pkg/dirprotect"
)

func legacyRAMFixture(t *testing.T, mutate func(*Policy)) (*Catalog, string, <-chan struct{}) {
	t.Helper()
	root := t.TempDir()
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	domain := "vz.sitebrush.com"
	for relative, source := range map[string]string{
		"static/index.html": `<html><head><link rel="stylesheet" href="/style.css"></head><body>published legacy page</body></html>`,
		"static/common.css": "body { color: green }",
		"files/style.css":   "body { color: red }",
		"files/manual.txt":  "public manual",
		"files/token.txt":   "private token file",
	} {
		parts := strings.SplitN(relative, "/", 2)
		filename := filepath.Join(root, "storage", parts[0], domain, parts[1])
		if err := os.MkdirAll(filepath.Dir(filename), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	policy := Policy{Domain: domain, Layouts: map[string]string{"/index.html": "legacy"}, ResourcePaths: map[string]string{"/style.css": "style.css"}, Files: []FileRule{{Name: "manual.txt", Mode: "public"}, {Name: "token.txt", Mode: "token"}}}
	if mutate != nil {
		mutate(&policy)
	}
	if err := Write(root, policy); err != nil {
		t.Fatal(err)
	}
	owner := Policy{Domain: "owner.test", ServerOwner: true, ServerIPs: []TrustedIP{{Address: "192.0.2.1", Expires: time.Now().Add(time.Hour).Unix()}}}
	if err := Write(root, owner); err != nil {
		t.Fatal(err)
	}
	options := RuntimeOptions{Root: root, Mode: StaticSnapshot, RAMCacheBytes: 1 << 20, StorageName: func(domain string) string { return domain },
		StaticPath: func(pathname string) string {
			if pathname == "/" {
				return "index.html"
			}
			return strings.TrimPrefix(pathname, "/")
		},
		HTMLFile: func(_ string, prefix string) bool { return strings.Contains(prefix, "<html>") }, PrepareBootstrap: func(Policy, string) map[HTMLVariantKey]string {
			return map[HTMLVariantKey]string{{Language: "en"}: "<script>bootstrap</script>"}
		}}
	return NewCatalog(root, stop, options), root, stop
}

func TestRAMRecoversOrdinaryLegacySourcesBeforeRequests(t *testing.T) {
	catalog, root, stop := legacyRAMFixture(t, func(policy *Policy) { policy.AllowedIPs = []string{"198.51.100.10"} })
	if err := catalog.SetMemory(true); err != nil {
		t.Fatal(err)
	}
	policyBefore, err := os.ReadFile(PolicyPath(root, "vz.sitebrush.com"))
	if err != nil {
		t.Fatal(err)
	}
	for _, client := range []string{"192.0.2.1", "203.0.113.1"} {
		for _, pathname := range []string{"/", "/index.html", "/common.css", "/style.css"} {
			decision := catalog.Lookup(Lookup{Host: "vz.sitebrush.com", Path: pathname, ClientIP: client})
			if decision.AdminAllowed || decision.LoadTrusted != (client == "192.0.2.1") {
				t.Fatalf("trust changed: %+v", decision)
			}
			response, found, acquireErr := catalog.AcquirePrepared(decision, pathname, HTMLVariantKey{Language: "en"})
			if acquireErr != nil || !found {
				t.Fatalf("%s found=%t error=%v", pathname, found, acquireErr)
			}
			if response.generation.recoveredPages == 0 {
				t.Fatal("legacy fingerprint was not recovered")
			}
			// The serving path must remain independent of source descriptors.
			for _, sourceRoot := range response.generation.roots {
				sourceRoot.Close()
			}
			recorder := httptest.NewRecorder()
			if serveErr := response.ServeHTTP(recorder, httptest.NewRequest("GET", "http://vz.sitebrush.com"+pathname, nil)); serveErr != nil || recorder.Code != 200 {
				t.Fatal(recorder.Code, serveErr)
			}
			response.Close()
		}
	}
	decision := catalog.Lookup(Lookup{Host: "vz.sitebrush.com", Path: "/p/manual.txt", File: "manual.txt"})
	response, found, err := catalog.AcquirePreparedFile(decision, "manual.txt")
	if err != nil || !found {
		t.Fatal(found, err)
	}
	response.Close()
	denied := catalog.Lookup(Lookup{Host: "vz.sitebrush.com", Path: "/p/token.txt", File: "token.txt"})
	if response, found, err = catalog.AcquirePreparedFile(denied, "token.txt"); err != nil || found || response != nil {
		t.Fatal("token file entered RAM", found, err)
	}
	stats, err := catalog.RAMStats(stop)
	if err != nil {
		t.Fatal(err)
	}
	matched := false
	for _, site := range stats.Sites {
		if site.Domain != "vz.sitebrush.com" {
			continue
		}
		matched = true
		if site.Policy == nil || site.Policy.Frozen || site.Policy.Versioned || site.Policy.RootPrepared || site.Policy.IndexPrepared || site.Policy.AdministratorIPRules != 1 || !site.RootRecovered || site.RecoveredPages == 0 || site.RecoveredFiles == 0 {
			t.Fatalf("incorrect policy diagnostics: %+v %+v", site, site.Policy)
		}
	}
	if !matched {
		t.Fatal("domain missing from diagnostics")
	}
	policyAfter, err := os.ReadFile(PolicyPath(root, "vz.sitebrush.com"))
	if err != nil || string(policyBefore) != string(policyAfter) {
		t.Fatal("public preparation rewrote durable policy", err)
	}
}

func TestRAMRecoveryPreservesRestrictionsAndCheckpointAuthority(t *testing.T) {
	for _, scenario := range []struct {
		name, reason string
		mutate       func(*Policy)
	}{
		{"password", "restricted", func(policy *Policy) { policy.Passwords = []dirprotect.Rule{{Path: "/"}} }},
		{"hidden", "restricted", func(policy *Policy) { policy.Hidden = map[string]bool{"page\x00/": true} }},
		{"trial", "restricted", func(policy *Policy) { policy.PrivateTestDrive = true }},
		{"pending", "restricted", func(policy *Policy) { policy.PendingRegistration = true }},
		{"stealth", "restricted", func(policy *Policy) { policy.Stealth = true }},
		{"frozen", "missing_version", func(policy *Policy) { policy.Frozen = true }},
		{"checkpoint", "missing_version", func(policy *Policy) {
			policy.Frozen = true
			policy.Versioned = true
			policy.Layouts["/"] = "checkpoint"
		}},
		{"versioned", "missing_version", func(policy *Policy) { policy.Versioned = true; policy.Layouts["/"] = "checkpoint" }},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			catalog, _, _ := legacyRAMFixture(t, scenario.mutate)
			if err := catalog.SetMemory(true); err != nil {
				t.Fatal(err)
			}
			var reason string
			decision := catalog.Lookup(Lookup{Host: "vz.sitebrush.com", Path: "/"})
			response, found, err := catalog.AcquirePrepared(decision, "/", HTMLVariantKey{Language: "en"}, &reason)
			if response != nil || found || err != nil {
				t.Fatal(found, err)
			}
			if scenario.reason == "restricted" && reason != scenario.reason && reason != "no_prepared_object" {
				t.Fatal(reason)
			}
			generation := decision.generation
			if scenario.name != "password" && scenario.name != "hidden" && (generation.recoveredPages != 0 || generation.recoveredFiles != 0) {
				t.Fatal("restricted/versioned source was recovered")
			}
		})
	}
}

func TestRAMRecoveryNeverReplacesExistingFingerprint(t *testing.T) {
	catalog, _, _ := legacyRAMFixture(t, func(policy *Policy) { policy.PreparedPages = map[string]string{"/": strings.Repeat("0", 64)} })
	if err := catalog.SetMemory(true); err == nil {
		t.Fatal("mismatched fingerprint was silently replaced")
	}
}

func TestRAMRecoveredResponseValidatorsAndRanges(t *testing.T) {
	catalog, _, _ := legacyRAMFixture(t, nil)
	if err := catalog.SetMemory(true); err != nil {
		t.Fatal(err)
	}
	decision := catalog.Lookup(Lookup{Host: "vz.sitebrush.com", Path: "/"})
	response, found, err := catalog.AcquirePrepared(decision, "/", HTMLVariantKey{Language: "en"})
	if err != nil || !found {
		t.Fatal(found, err)
	}
	defer response.Close()
	publication, err := catalog.OpenPublished(decision, "index.html", "")
	if err != nil {
		t.Fatal(err)
	}
	defer publication.Close()
	baseline := httptest.NewRecorder()
	if err = NewStreamer().Prepared(baseline, httptest.NewRequest("GET", "http://vz.sitebrush.com/", nil), publication.File, publication.HTML, HTMLVariantKey{Language: "en"}, ""); err != nil {
		t.Fatal(err)
	}
	actual := httptest.NewRecorder()
	if err = response.ServeHTTP(actual, httptest.NewRequest("GET", "http://vz.sitebrush.com/", nil)); err != nil {
		t.Fatal(err)
	}
	if actual.Body.String() != baseline.Body.String() || actual.Header().Get("ETag") != baseline.Header().Get("ETag") {
		t.Fatal("recovered RAM representation differs from file path")
	}
	for _, scenario := range []struct {
		method, header, content string
		status                  int
	}{{"HEAD", "", "", 200}, {"GET", "Range", "bytes=0-7", 206}, {"GET", "If-None-Match", baseline.Header().Get("ETag"), 304}, {"GET", "If-Modified-Since", baseline.Header().Get("Last-Modified"), 304}} {
		request := httptest.NewRequest(scenario.method, "http://vz.sitebrush.com/", nil)
		if scenario.header != "" {
			request.Header.Set(scenario.header, scenario.content)
		}
		recorder := httptest.NewRecorder()
		response.ServeHTTP(recorder, request)
		if recorder.Code != scenario.status {
			t.Fatal(scenario, recorder.Code)
		}
		if scenario.method == http.MethodHead && recorder.Body.Len() != 0 {
			t.Fatal("HEAD returned a body")
		}
	}
}
