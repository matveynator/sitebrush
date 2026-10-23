package publicsite

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
)

func TestFrontKeepsPublicHTTPAndHTTPSWhileBackendIsReplaced(t *testing.T) {
	stop := make(chan struct{})
	defer close(stop)
	backend := httptest.NewServer(RestoreRequest(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Forwarded-For") != "" {
			t.Error("untrusted forwarding claim reached backend")
		}
		io.WriteString(w, "editor")
	}), "private-contact"))
	backendURL, _ := url.Parse(backend.URL)
	front := NewFront("private-contact", func(w http.ResponseWriter, r *http.Request, paused bool) bool {
		if r.Method == http.MethodGet && r.URL.RawQuery == "" && r.URL.Path == "/" {
			io.WriteString(w, "published")
			return true
		}
		return false
	}, stop)
	if err := front.Set(backendURL.Host, false); err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(front)
	defer httpServer.Close()
	httpsServer := httptest.NewTLSServer(front)
	defer httpsServer.Close()
	for _, phase := range []string{"running", "replacing", "recovered"} {
		if phase == "replacing" {
			front.Set("", true)
			draining, drainingErr := http.Get(httpServer.URL + "/pending-policy")
			if drainingErr != nil {
				t.Fatal(drainingErr)
			}
			drainingBody, _ := io.ReadAll(draining.Body)
			draining.Body.Close()
			if string(drainingBody) != "editor" {
				t.Fatal("public read was interrupted before policy drain completed")
			}
			front.Set(backendURL.Host, true)
			backend.Close()
		}
		if phase == "recovered" {
			replacement := httptest.NewServer(RestoreRequest(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "editor") }), "private-contact"))
			defer replacement.Close()
			replacementURL, _ := url.Parse(replacement.URL)
			front.Set(replacementURL.Host, false)
		}
		for _, server := range []*httptest.Server{httpServer, httpsServer} {
			response, err := server.Client().Get(server.URL + "/")
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(response.Body)
			response.Body.Close()
			if response.StatusCode != http.StatusOK || string(body) != "published" {
				t.Fatalf("%s: %d %s", phase, response.StatusCode, body)
			}
		}
		if phase == "running" || phase == "recovered" {
			response, err := http.Get(httpServer.URL + "/?edit")
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(response.Body)
			response.Body.Close()
			if string(body) != "editor" {
				t.Fatalf("backend = %s", body)
			}
		}
		if phase == "replacing" {
			response, err := http.Post(httpServer.URL+"/?save", "text/plain", nil)
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != http.StatusServiceUnavailable {
				t.Fatal(response.StatusCode)
			}
		}
	}
}
func TestPublicationPolicyInvalidationAndRevokedAlias(t *testing.T) {
	root := t.TempDir()
	stop := make(chan struct{})
	defer close(stop)
	catalog := NewCatalog(root, stop)
	policy := Policy{Domain: "example.org", Aliases: []string{"alias.example.org"}, Files: []FileRule{{Name: "secret.pdf", Mode: "token"}}}
	if err := Write(root, policy); err != nil {
		t.Fatal(err)
	}
	decision := catalog.Lookup(Lookup{Host: "alias.example.org", File: "secret.pdf"})
	if !decision.Found || decision.Domain != policy.Domain || decision.File.Mode != "token" {
		t.Fatal(decision)
	}
	if err := os.WriteFile(BlockPath(root, policy.Domain), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if catalog.Lookup(Lookup{Host: "alias.example.org"}).Found {
		t.Fatal("mutation barrier did not close the alias fast path")
	}
	os.Remove(BlockPath(root, policy.Domain))
	policy.Aliases = nil
	if err := Write(root, policy); err != nil {
		t.Fatal(err)
	}
	if catalog.Lookup(Lookup{Host: "alias.example.org"}).Found {
		t.Fatal("removed alias retained public access")
	}
	stale, _ := json.Marshal(Policy{Domain: policy.Domain})
	if err := os.WriteFile(PolicyPath(root, "alias.example.org"), stale, 0600); err != nil {
		t.Fatal(err)
	}
	if catalog.Lookup(Lookup{Host: "alias.example.org"}).Found {
		t.Fatal("stale alias pointer bypassed the current primary policy")
	}

}
