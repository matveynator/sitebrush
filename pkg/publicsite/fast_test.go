package publicsite

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestMemoryCatalogTrustRevocationAndBarrier(t *testing.T) {
	root := t.TempDir()
	stop := make(chan struct{})
	defer close(stop)
	owner := Policy{Domain: "owner.test", ServerIPs: []TrustedIP{{Address: "192.0.2.1"}}}
	site := Policy{Domain: "site.test", Aliases: []string{"alias.test"}, AllowedIPs: []string{"198.51.100.0/24"}, TrustedIPs: []TrustedIP{{Address: "203.0.113.1", Expires: time.Now().Add(time.Hour).Unix()}, {Address: "203.0.113.2", Expires: 1}}}
	for _, policy := range []Policy{owner, site} {
		if err := Write(root, policy); err != nil {
			t.Fatal(err)
		}
	}
	catalog := NewCatalog(root, stop)
	if err := catalog.SetMemory(true); err != nil {
		t.Fatal(err)
	}
	// Removing the metadata directory proves the read path does not touch disk.
	directory := filepath.Dir(PolicyPath(root, site.Domain))
	if err := os.Rename(directory, directory+".held"); err != nil {
		t.Fatal(err)
	}
	for _, address := range []string{"192.0.2.1", "198.51.100.4", "203.0.113.1"} {
		if decision := catalog.Lookup(Lookup{Host: "alias.test", ClientIP: address}); !decision.Found || !decision.LoadTrusted {
			t.Fatalf("%s: %+v", address, decision)
		}
	}
	if catalog.Lookup(Lookup{Host: "site.test", ClientIP: "203.0.113.2"}).LoadTrusted {
		t.Fatal("expired login trusted")
	}
	if catalog.Lookup(Lookup{Host: "owner.test", ClientIP: "198.51.100.4"}).LoadTrusted {
		t.Fatal("site administrator trusted on another site")
	}
	if err := os.Rename(directory+".held", directory); err != nil {
		t.Fatal(err)
	}
	if err := SetBlocked(root, site.Domain, true); err != nil {
		t.Fatal(err)
	}
	if err := catalog.Block(site.Domain); err != nil {
		t.Fatal(err)
	}
	if decision := catalog.Lookup(Lookup{Host: "alias.test"}); decision.Found || !decision.Known {
		t.Fatal("barrier did not close alias", decision)
	}
	owner.ServerIPs = nil
	if err := Write(root, owner); err != nil {
		t.Fatal(err)
	}
	if err := catalog.Reload(owner.Domain); err != nil {
		t.Fatal(err)
	}
	site.Aliases = nil
	site.AllowedIPs = nil
	site.TrustedIPs = nil
	if err := Write(root, site); err != nil {
		t.Fatal(err)
	}
	if err := catalog.Reload(site.Domain); err != nil {
		t.Fatal(err)
	}
	if catalog.Lookup(Lookup{Host: "alias.test"}).Found {
		t.Fatal("removed alias retained access")
	}
	if catalog.Lookup(Lookup{Host: "site.test"}).Found {
		t.Fatal("snapshot opened before durable barrier was released")
	}
	if err := SetBlocked(root, site.Domain, false); err != nil {
		t.Fatal(err)
	}
	if catalog.Lookup(Lookup{Host: "site.test"}).Found {
		t.Fatal("disk barrier removal opened unacknowledged metadata")
	}
	if err := catalog.Open(site.Domain); err != nil {
		t.Fatal(err)
	}
	if decision := catalog.Lookup(Lookup{Host: "site.test", ClientIP: "192.0.2.1"}); !decision.Found || decision.LoadTrusted {
		t.Fatal("owner trust not revoked", decision)
	}
	if err := catalog.SetMemory(false); err != nil {
		t.Fatal(err)
	}
	if !catalog.Lookup(Lookup{Host: "site.test"}).Found {
		t.Fatal("legacy policy fallback failed")
	}
}

func TestHTMLStreamsConditionalAndRangeResponses(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "page.html")
	original := "<html><body>" + strings.Repeat("published ", 3000) + "</body></html>"
	if err := os.WriteFile(filename, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	script := "<script>bootstrap()</script>"
	expected := strings.Replace(original, "<html>", "<html>"+script, 1)
	response := httptest.NewRecorder()
	if err := HTML(response, httptest.NewRequest("GET", "http://site.test/", nil), file, script, "test"); err != nil {
		t.Fatal(err)
	}
	if response.Body.String() != expected {
		t.Fatal("stream changed published bytes")
	}
	for _, method := range []string{"GET", "HEAD"} {
		request := httptest.NewRequest(method, "http://site.test/", nil)
		request.Header.Set("If-None-Match", response.Header().Get("ETag"))
		cached := httptest.NewRecorder()
		if err := HTML(cached, request, file, script, "test"); err != nil {
			t.Fatal(err)
		}
		if cached.Code != http.StatusNotModified || cached.Body.Len() != 0 {
			t.Fatal("conditional response", cached.Code)
		}
	}
	request := httptest.NewRequest("GET", "http://site.test/", nil)
	insertion := strings.Index(expected, "<script>")
	request.Header.Set("Range", "bytes="+strings.Join([]string{strconv.Itoa(insertion - 3), strconv.Itoa(insertion + len(script) + 3)}, "-"))
	ranged := httptest.NewRecorder()
	if err := HTML(ranged, request, file, script, "test"); err != nil {
		t.Fatal(err)
	}
	if ranged.Code != http.StatusPartialContent || ranged.Body.String() != expected[insertion-3:insertion+len(script)+4] {
		t.Fatal("range across insertion failed")
	}
}

func TestObservationsRetainAcknowledgedBoundaryAndDropLater(t *testing.T) {
	stop := make(chan struct{})
	defer close(stop)
	front := NewFront("secret", func(http.ResponseWriter, *http.Request, bool) bool { return false }, stop)
	if err := front.Set("127.0.0.1:12345", false); err != nil {
		t.Fatal(err)
	}
	if err := front.PolicyEnabled(); err != nil {
		t.Fatal(err)
	}
	pull := func(ack uint64) Observations {
		encoded, _ := json.Marshal(ObservationRequest{Address: "127.0.0.1:12345", Ack: ack})
		response, err := front.PullObservations(encoded)
		if err != nil {
			t.Fatal(err)
		}
		var envelope Observations
		if err := json.Unmarshal(response, &envelope); err != nil {
			t.Fatal(err)
		}
		return envelope
	}
	front.Observe("site.test", struct{ Sequence int }{1})
	first := pull(0)
	deadline := time.Now().Add(2 * time.Second)
	for len(first.Events) == 0 && time.Now().Before(deadline) {
		first = pull(first.Batch)
	}
	if len(first.Events) != 1 {
		t.Fatal("first observation missing")
	}
	for index := 2; index < 10000; index++ {
		if front.ObservationAvailable("site.test") {
			front.Observe("site.test", struct{ Sequence int }{index})
		}
	}
	replay := pull(0)
	if string(replay.Events[0]) != string(first.Events[0]) || replay.Batch != first.Batch || len(replay.Events) != 1 {
		t.Fatal("in-flight batch changed")
	}
	next := pull(first.Batch)
	if next.Batch == first.Batch || len(next.Events) > 64 {
		t.Fatal("invalid acknowledged batch")
	}
	total := len(next.Events)
	lost := len(next.LostDomains) > 0
	for index := 0; index < 40 && !lost; index++ {
		next = pull(next.Batch)
		total += len(next.Events)
		lost = len(next.LostDomains) > 0
	}
	if !lost || total == 0 {
		t.Fatal("overload failed to retain accepted observations and mark losses")
	}
	if _, err := front.PullObservations([]byte("invalid")); err == nil {
		t.Fatal("invalid observation request accepted")
	}
}
