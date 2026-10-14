package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestHTTPServerUsesAnotherPortWhenExistingProcessOwnsRequestedPort(t *testing.T) {
	primaryListener, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	defer primaryListener.Close()
	requestedPort := primaryListener.Addr().(*net.TCPAddr).Port
	secondaryListener, actualPort, err := listenOnAvailablePort(requestedPort)
	if err != nil {
		t.Fatal(err)
	}
	defer secondaryListener.Close()
	if actualPort == requestedPort {
		t.Fatal("independent application processes must not share an HTTP listener")
	}
}

// The browser uses production routes and storage. Only fixture resources and a
// read-only database view are test adapters; upload and save are not mocked.
func TestVisualEditorBrowserRegressions(t *testing.T) {
	if os.Getenv("SITEBRUSH_EDITOR_BROWSER_TEST") != "1" {
		t.Skip("set SITEBRUSH_EDITOR_BROWSER_TEST=1 after npm ci in scripts/editor and playwright install chromium")
	}
	application := newRouterTestApplication(t)
	fixtureContext := contextWithSiteDatabaseCreation(contextWithDomain(context.Background(), "localhost"))
	staticAssets, err := buildEmbeddedStaticAssetCache(testStaticFiles(t))
	if err != nil {
		t.Fatal(err)
	}
	application.embeddedStaticAssets = staticAssets
	if _, err := application.db.ExecContext(fixtureContext, `INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, "localhost", "editor@example.com", "old"); err != nil {
		t.Fatal(err)
	}
	fixtureHTML := `<!doctype html>
<html lang="en" data-example="preserved"><head>
<title>Editor fixture</title>
<meta name="description" content="preserve > metadata">
<link rel="stylesheet" href="/_editor-fixture.css">
<base href="/">
<style>.class-frame { width: 180px; height: 120px; } .responsive-frame { width: 100%; height: auto; } body { margin: 8px; } </style>
<script>window.fixtureScriptMustNotRun = true; const boundaryExample = "<body>example</body>";</script>
</head><body data-example="body-preserved">
<img id="attribute-image" src="/_editor-wide.svg" width="180" height="120" alt="attributes" onload="window.fixtureHandlerMustNotRun = true">
<img id="class-image" class="class-frame" src="/_editor-wide.svg" alt="class">
<img id="responsive-image" class="responsive-frame" src="/_editor-wide.svg" alt="responsive">
<img id="inline-image" src="/_editor-wide.svg" style="width: 180px; height: 120px; object-fit: contain;" alt="inline">
<a id="gallery-link" href="/_editor-wide.svg" data-fancybox="gallery"><img id="gallery-image" src="/_editor-wide.svg" width="50" height="50" alt="gallery"></a>
<p>Editable content</p>
<script id="body-script">window.fixtureBodyScriptMustNotRun = true;</script>
<iframe id="script-frame" title="Embedded script fixture" srcdoc="&lt;script&gt;window.fixtureNestedScriptMustNotRun = true;&lt;/script&gt;"></iframe>
` + strings.Repeat("<p>Long document for caret and scroll regression</p>\n", 100) + `</body></html>`
	for fixtureIndex := 0; fixtureIndex < 40; fixtureIndex++ {
		pagePath := fmt.Sprintf("/editor-%d", fixtureIndex)
		if _, err := application.db.ExecContext(fixtureContext, `INSERT INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,1)`, "localhost", pagePath, "Editor fixture", fixtureHTML); err != nil {
			t.Fatal(err)
		}
	}
	for _, viewportWidth := range []int{1280, 320} {
		copiedMetadata := `<p><title>Editor fixture</title><meta name="description" content="preserve &gt; metadata"><link rel="stylesheet" href="/_editor-fixture.css"></p>`
		corruptedHTML := strings.Replace(fixtureHTML, "<p>Editable content</p>", copiedMetadata+copiedMetadata+"<p>Editable content</p>", 1)
		for fixtureName, documentHTML := range map[string]string{"repeat": fixtureHTML, "corrupted": corruptedHTML} {
			pagePath := fmt.Sprintf("/editor-%s-%d", fixtureName, viewportWidth)
			if _, err := application.db.ExecContext(fixtureContext, `INSERT INTO pages(domain,path,title,html,published) VALUES(?,?,?,?,1)`, "localhost", pagePath, "Editor fixture", documentHTML); err != nil {
				t.Fatal(err)
			}
		}
	}
	adminCookie := newAdminSessionCookie(t, application, "editor@example.com")
	fixtureServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasPrefix(request.URL.Path, "/p/static/") {
			application.serveEmbeddedStaticAsset(writer, request)
			return
		}
		if strings.HasPrefix(request.URL.Path, "/p/") {
			application.servePublicAsset(writer, request)
			return
		}
		switch request.URL.Path {
		case "/_editor-fixture.html":
			if !application.isAdminRequest(request) {
				http.Error(writer, "forbidden", http.StatusForbidden)
				return
			}
			var storedHTML string
			if err := application.db.QueryRowContext(request.Context(), `SELECT html FROM pages WHERE domain=? AND path=?`, "localhost", request.URL.Query().Get("path")).Scan(&storedHTML); err != nil {
				http.Error(writer, err.Error(), http.StatusNotFound)
				return
			}
			writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = writer.Write([]byte(storedHTML))
		case "/_editor-fixture.css":
			writer.Header().Set("Content-Type", "text/css")
			_, _ = writer.Write([]byte("img { vertical-align: top; }"))
		case "/_editor-wide.svg", "/_editor-tall.svg":
			writer.Header().Set("Content-Type", "image/svg+xml")
			imageWidth, imageHeight := 400, 100
			if request.URL.Path == "/_editor-tall.svg" {
				imageWidth, imageHeight = 100, 400
			}
			_, _ = fmt.Fprintf(writer, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d"><rect width="100%%" height="100%%" fill="orange"/><rect width="50%%" height="50%%" fill="blue"/><rect width="100%%" height="10%%" fill="red"/><rect y="90%%" width="100%%" height="10%%" fill="green"/></svg>`, imageWidth, imageHeight)
		default:
			application.route(writer, request)
		}
	}))
	defer fixtureServer.Close()
	fixtureURL := strings.Replace(fixtureServer.URL, "127.0.0.1", "localhost", 1)
	browserCommand := exec.Command("node", "scripts/editor/browser_test.cjs")
	browserCommand.Env = append(os.Environ(), "SITEBRUSH_EDITOR_URL="+fixtureURL, "SITEBRUSH_EDITOR_COOKIE="+adminCookie.Name+"="+adminCookie.Value)
	browserOutput, err := browserCommand.CombinedOutput()
	t.Log(string(browserOutput))
	if err != nil {
		t.Fatalf("editor browser regressions: %v", err)
	}
}
