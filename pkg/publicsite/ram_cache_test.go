package publicsite

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/matveynator/sitebrush/v2/pkg/dirprotect"
	"github.com/matveynator/sitebrush/v2/pkg/sitefreeze"
)

func ramFixture(t *testing.T, limit int64, mutate func(*Policy)) (*Catalog, Policy, string) {
	t.Helper()
	root := t.TempDir()
	stop := make(chan struct{})
	t.Cleanup(func() { close(stop) })
	source := "<html><head><title>Published</title></head><body>immutable</body></html>"
	digest := sha256.Sum256([]byte(source))
	version := hex.EncodeToString(digest[:])
	object := filepath.Join(root, sitefreeze.ObjectPath("ram.test", version))
	if err := os.MkdirAll(filepath.Dir(object), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(object, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	policy := Policy{Domain: "ram.test", Versioned: true, PublishedVersions: map[string]string{"/": version, "/index.html": version}, Layouts: map[string]string{"/": "first", "/index.html": "second"}}
	if mutate != nil {
		mutate(&policy)
	}
	if err := Write(root, policy); err != nil {
		t.Fatal(err)
	}
	catalog := NewCatalog(root, stop, RuntimeOptions{Mode: StaticSnapshot, Root: root, RAMCacheBytes: limit,
		StorageName: func(domain string) string { return domain }, StaticPath: func(pathname string) string {
			if pathname == "/" {
				return "index.html"
			}
			return strings.TrimPrefix(pathname, "/")
		}, HTMLFile: func(string, string) bool { return true },
		PrepareBootstrap: func(policy Policy, pathname string) map[HTMLVariantKey]string {
			bootstrap := "<script>" + policy.Layouts[pathname] + "</script>"
			return map[HTMLVariantKey]string{{Language: "en"}: bootstrap, {Language: "ru"}: bootstrap}
		}})
	if err := catalog.SetMemory(true); err != nil {
		t.Fatal(err)
	}
	return catalog, policy, source
}

func acquireRAM(t *testing.T, catalog *Catalog, pathname string) *PreparedResponse {
	t.Helper()
	decision := catalog.Lookup(Lookup{Host: "ram.test", Path: pathname})
	response, found, err := catalog.AcquirePrepared(decision, pathname, HTMLVariantKey{Language: "en"})
	if err != nil || !found {
		t.Fatalf("RAM response: found=%t error=%v", found, err)
	}
	return response
}

func TestRAMResponsesRetainHTTPSemanticsWithoutFileOperations(t *testing.T) {
	catalog, _, _ := ramFixture(t, 1<<20, nil)
	response := acquireRAM(t, catalog, "/")
	defer response.Close()
	file, err := catalog.OpenPublished(catalog.Lookup(Lookup{Host: "ram.test", Path: "/"}), "index.html", catalog.Lookup(Lookup{Host: "ram.test", Path: "/"}).PageVersion)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	baseline := httptest.NewRecorder()
	if err := NewStreamer().Prepared(baseline, httptest.NewRequest("GET", "http://ram.test/", nil), file.File, response.prepared, HTMLVariantKey{Language: "en"}, ""); err != nil {
		t.Fatal(err)
	}
	// Closing the root makes any additional open/stat fail on every platform.
	response.generation.roots["versions"].Close()
	for _, scenario := range []struct {
		method, header, content string
		status                  int
	}{
		{"GET", "", "", 200}, {"HEAD", "", "", 200},
		{"GET", "Range", "bytes=3-22", 206}, {"HEAD", "Range", "bytes=3-22", 206},
		{"GET", "Range", "bytes=99999-", 416},
		{"GET", "Range", "bytes=0-2,20-24", 206},
		{"GET", "If-None-Match", baseline.Header().Get("ETag"), 304},
		{"GET", "If-Modified-Since", baseline.Header().Get("Last-Modified"), 304},
		{"GET", "If-Match", "\"other\"", 412},
		{"GET", "If-Unmodified-Since", time.Unix(1, 0).UTC().Format(http.TimeFormat), 412},
	} {
		request := httptest.NewRequest(scenario.method, "http://ram.test/", nil)
		if scenario.header != "" {
			request.Header.Set(scenario.header, scenario.content)
		}
		actual := httptest.NewRecorder()
		if err := response.ServeHTTP(actual, request); err != nil {
			t.Fatal(err)
		}
		if actual.Code != scenario.status {
			t.Fatalf("%+v: status=%d", scenario, actual.Code)
		}
		if scenario.header == "" {
			for _, header := range []string{"Content-Type", "Content-Length", "ETag", "Last-Modified", "Accept-Ranges", "Cache-Control"} {
				if actual.Header().Get(header) != baseline.Header().Get(header) {
					t.Fatalf("%s changed: %q != %q", header, actual.Header().Get(header), baseline.Header().Get(header))
				}
			}
			if scenario.method == "GET" && actual.Body.String() != baseline.Body.String() {
				t.Fatal("RAM body differs")
			}
		}
		if scenario.method == "HEAD" && actual.Body.Len() != 0 {
			t.Fatal("HEAD returned a body")
		}
	}
}

func TestRAMBudgetDeduplicatesAndRetainsBorrowedGenerations(t *testing.T) {
	catalog, policy, _ := ramFixture(t, 1<<20, nil)
	response := acquireRAM(t, catalog, "/")
	first, err := catalog.RAMStats(nil)
	if err != nil || first.Bodies != 2 || first.Bytes > first.Limit {
		t.Fatal(first, err)
	}
	if err := catalog.Reload(policy.Domain); err != nil {
		t.Fatal(err)
	}
	replacement := acquireRAM(t, catalog, "/")
	if replacement.body != response.body {
		t.Fatal("unchanged body copied across generations")
	}
	if _, _, err := catalog.AcquirePrepared(Decision{Found: true, Domain: policy.Domain, generation: response.generation, PageVersion: policy.PublishedVersions["/"]}, "/", HTMLVariantKey{Language: "en"}); err == nil {
		t.Fatal("retired decision reacquired")
	}
	if err := catalog.Block(policy.Domain); err != nil {
		t.Fatal(err)
	}
	if _, _, err := catalog.AcquirePrepared(catalog.Lookup(Lookup{Host: policy.Domain, Path: "/"}), "/", HTMLVariantKey{Language: "en"}); err == nil {
		t.Fatal("blocked site acquired RAM body")
	}
	response.Close()
	replacement.Close()
}

func TestRAMCacheMissesNeverChangePublicationOrPrivateAccess(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		limit  int64
		mutate func(*Policy)
	}{
		{"disabled", 0, nil}, {"budget", 1, nil},
		{"password", 1 << 20, func(policy *Policy) { policy.Passwords = []dirprotect.Rule{{Path: "/"}} }},
		{"trial", 1 << 20, func(policy *Policy) { policy.PrivateTestDrive = true }},
		{"unversioned", 1 << 20, func(policy *Policy) { policy.PublishedVersions = nil }},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			catalog, _, _ := ramFixture(t, scenario.limit, scenario.mutate)
			response, found, err := catalog.AcquirePrepared(catalog.Lookup(Lookup{Host: "ram.test", Path: "/"}), "/", HTMLVariantKey{Language: "en"})
			if response != nil || found || err != nil {
				t.Fatal(response, found, err)
			}
			stats, err := catalog.RAMStats(nil)
			if err != nil || stats.Bytes != 0 {
				t.Fatal(stats, err)
			}
		})
	}
}

func TestRAMConfigurationBounds(t *testing.T) {
	for _, input := range []string{"-1", "invalid", "9223372036854775807"} {
		if _, err := ParseRAMCacheMiB(input); err == nil {
			t.Fatal(input)
		}
	}
	if count, err := ParseRAMCacheMiB("512"); err != nil || count != 512<<20 {
		t.Fatal(count, err)
	}
}

func TestRAMOwnerBoundsStagingAndCancellation(t *testing.T) {
	stop := make(chan struct{})
	defer close(stop)
	cache := newRAMCache(8, stop)
	first, err := cache.exchange(ramJob{action: "reserve", key: "first", size: 8}, nil)
	if err != nil || first.ticket == 0 {
		t.Fatal(first, err)
	}
	second, err := cache.exchange(ramJob{action: "reserve", key: "second", size: 1}, nil)
	if err != nil || second.ticket != 0 {
		t.Fatal("staged bytes escaped budget", second, err)
	}
	abandoned := make(chan struct{})
	close(abandoned)
	if _, err = cache.exchange(ramJob{action: "reserve", key: "cancelled", size: 1}, abandoned); err == nil {
		t.Fatal("cancelled reservation accepted")
	}
	cache.release([]uint64{first.ticket})
	stats, err := cache.exchange(ramJob{action: "stats"}, nil)
	if err != nil || stats.stats.Bytes != 0 {
		t.Fatal(stats, err)
	}
}

func TestRAMReadersDoNotShareMutableResponses(t *testing.T) {
	catalog, _, _ := ramFixture(t, 1<<20, nil)
	results := make(chan error, 64)
	for index := 0; index < cap(results); index++ {
		go func() {
			response, found, err := catalog.AcquirePrepared(catalog.Lookup(Lookup{Host: "ram.test", Path: "/"}), "/", HTMLVariantKey{Language: "en"})
			if err == nil && !found {
				err = os.ErrNotExist
			}
			if err == nil {
				output := httptest.NewRecorder()
				err = response.ServeHTTP(output, httptest.NewRequest("GET", "http://ram.test/", nil))
				response.Close()
				if !strings.Contains(output.Body.String(), "first") {
					err = os.ErrInvalid
				}
			}
			results <- err
		}()
	}
	for index := 0; index < cap(results); index++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
}
