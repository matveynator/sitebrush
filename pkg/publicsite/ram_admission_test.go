package publicsite

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRAMAdmissionFavorsEveryHomepageAndSharedDependencies(t *testing.T) {
	root := t.TempDir()
	stop := make(chan struct{})
	defer close(stop)
	store := func(domain, directory, name, source string) string {
		t.Helper()
		target := filepath.Join(root, "storage", directory, domain, name)
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256([]byte(source))
		return hex.EncodeToString(digest[:])
	}
	for _, domain := range []string{"a.test", "vz.sitebrush.com"} {
		first := `<html><head><link rel="stylesheet" href="/style.css"></head><body>home</body></html>`
		second := strings.Replace(first, "home", "second", 1)
		policy := Policy{Domain: domain, PreparedPages: map[string]string{
			"/":            store(domain, "static", "index.html", first),
			"/index.html":  store(domain, "static", "index.html", first),
			"/second.html": store(domain, "static", "second.html", second),
		}, ResourcePaths: map[string]string{"/style.css": "style.css", "/font.woff2": "font.woff2", "/rare.bin": "rare.bin"},
			PreparedFiles: map[string]string{
				"style.css":  store(domain, "files", "style.css", `@font-face{src:url('/font.woff2')} body {color: red}`),
				"font.woff2": store(domain, "files", "font.woff2", strings.Repeat("f", 100)),
				"rare.bin":   store(domain, "files", "rare.bin", strings.Repeat("r", 500)),
			}}
		if err := Write(root, policy); err != nil {
			t.Fatal(err)
		}
	}
	catalog := NewCatalog(root, stop, RuntimeOptions{Root: root, Mode: StaticSnapshot, RAMCacheBytes: 650, DefaultLanguage: "ru",
		StorageName: func(domain string) string { return domain }, StaticPath: func(pathname string) string {
			if pathname == "/" {
				return "index.html"
			}
			return strings.TrimPrefix(pathname, "/")
		},
		HTMLFile: func(pathname, prefix string) bool { return strings.Contains(prefix, "<html>") },
		PrepareBootstrap: func(Policy, string) map[HTMLVariantKey]string {
			return map[HTMLVariantKey]string{
				{Language: "ru", Menu: true}:                 "<script>ru</script>",
				{Language: "en", Menu: true}:                 "<script>en</script>",
				{Language: "ru", Menu: true, Updating: true}: "<script>updating</script>",
			}
		}})
	if err := catalog.SetMemory(true); err != nil {
		t.Fatal(err)
	}
	for _, domain := range []string{"a.test", "vz.sitebrush.com"} {
		for _, pathname := range []string{"/", "/style.css", "/font.woff2"} {
			decision := catalog.Lookup(Lookup{Host: domain, Path: pathname})
			var reason string
			response, found, err := catalog.AcquirePrepared(decision, pathname, HTMLVariantKey{Language: "ru", Menu: true}, &reason)
			if err != nil || !found {
				t.Fatalf("%s%s found=%t reason=%s error=%v", domain, pathname, found, reason, err)
			}
			response.Close()
		}
		var reason string
		response, found, err := catalog.AcquirePrepared(catalog.Lookup(Lookup{Host: domain, Path: "/rare.bin"}), "/rare.bin", HTMLVariantKey{}, &reason)
		if response != nil {
			response.Close()
		}
		if err != nil || found || reason != "budget" {
			t.Fatalf("rare file found=%t reason=%s error=%v", found, reason, err)
		}
	}
	stats, err := catalog.RAMStats(stop)
	if err != nil || stats.Bytes > 650 || len(stats.Sites) != 2 {
		t.Fatalf("stats=%+v error=%v", stats, err)
	}
}

func TestRAMDependencyAliasesAndCyclesCountOncePerPage(t *testing.T) {
	generation := &publicGeneration{pageSources: map[string]string{"/": "root", "/index.html": "root", "/other": "other"}, references: map[string][]string{
		"/": {"/a.css", "/alias.css"}, "/other": {"/a.css"}, "/a.css": {"/b.css"}, "/alias.css": {"/b.css"}, "/b.css": {"/a.css", "/font.woff2", "https://external.test/ignored.css"},
	}}
	counts := dependencyUses(generation, Policy{Domain: "test", ResourcePaths: map[string]string{"/a.css": "a", "/alias.css": "a", "/b.css": "b", "/font.woff2": "font"}})
	for _, name := range []string{"a", "b", "font"} {
		if counts[name] != 2 {
			t.Fatalf("%s=%d", name, counts[name])
		}
	}
}

func TestRAMMissReasonsRemainDistinct(t *testing.T) {
	catalog, _, _ := ramFixture(t, 1, nil)
	decision := catalog.Lookup(Lookup{Host: "ram.test", Path: "/"})
	for _, scenario := range []struct {
		key    HTMLVariantKey
		reason string
	}{
		{HTMLVariantKey{Language: "en"}, "budget"},
		{HTMLVariantKey{Language: "unknown"}, "variant"},
	} {
		var reason string
		response, found, err := catalog.AcquirePrepared(decision, "/", scenario.key, &reason)
		if response != nil {
			response.Close()
		}
		if err != nil || found || reason != scenario.reason {
			t.Fatalf("found=%t reason=%s error=%v", found, reason, err)
		}
	}
	disabled, _, _ := ramFixture(t, 0, nil)
	var reason string
	_, found, err := disabled.AcquirePrepared(disabled.Lookup(Lookup{Host: "ram.test", Path: "/"}), "/", HTMLVariantKey{}, &reason)
	if err != nil || found || reason != "disabled" {
		t.Fatalf("disabled reason=%s error=%v", reason, err)
	}
	if err = catalog.Block("ram.test"); err != nil {
		t.Fatal(err)
	}
	_, found, err = catalog.AcquirePrepared(decision, "/", HTMLVariantKey{}, &reason)
	if err == nil || found || reason != "stale_generation" {
		t.Fatalf("revoked reason=%s error=%v", reason, err)
	}
}
