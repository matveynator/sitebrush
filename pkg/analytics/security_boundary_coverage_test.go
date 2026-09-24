package analytics

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSecurityBoundaryAnalyticsSanitizesSecretsAndCrossOriginTargets(t *testing.T) {
	raw := "/account?token=secret&api_key=key&utm_source=ad&safe=ok#profile"
	got := SafeURI(raw)
	if strings.Contains(got, "secret") || strings.Contains(got, "api_key") || strings.Contains(got, "utm_") {
		t.Fatalf("SECURITY: analytics URI retained sensitive query data: %q", got)
	}
	if got != "/account?safe=ok#profile" {
		t.Fatalf("safe URI = %q", got)
	}

	for _, unsafe := range []string{
		"https://user:pass@example.com/private",
		"//example.com/private",
		"/safe#bad?fragment",
		"/safe#bad fragment",
	} {
		if got := SafeURI(unsafe); got != "" {
			t.Fatalf("SECURITY: unsafe analytics URI %q survived as %q", unsafe, got)
		}
	}

	if got := SafeTarget("javascript:alert(1)"); got != "javascript:" {
		t.Fatalf("unsafe target classification = %q", got)
	}
	if got := SafeTarget("https://EXAMPLE.COM/" + strings.Repeat("x", 80)); !strings.HasPrefix(got, "example.com/") {
		t.Fatalf("external target host was not normalized: %q", got)
	}
}

func TestSecurityBoundaryAnalyticsPathRedactsTokenLikeSegmentsAndControlText(t *testing.T) {
	secret := strings.Repeat("a", 80)
	path := SafePath("/download/" + secret + "/file")
	if strings.Contains(path, secret) || !strings.Contains(path, "[redacted]") {
		t.Fatalf("SECURITY: token-like path segment was retained: %q", path)
	}
	if got := CleanText("hello\x00\r\nworld", 64); got != "helloworld" {
		t.Fatalf("control characters survived CleanText: %q", got)
	}
	if got := CleanText(strings.Repeat("x", 100), 16); len(got) != 16 {
		t.Fatalf("CleanText length = %d, want 16", len(got))
	}
}

func TestSecurityBoundaryAnalyticsGoalsRejectSecretBearingOrUnboundedMatches(t *testing.T) {
	invalid := [][]Goal{
		{{Name: "", Kind: "path", Match: "/"}},
		{{Name: "bad-kind", Kind: "script", Match: "/"}},
		{{Name: "secret", Kind: "uri", Match: "/login?token=secret"}},
		{{Name: strings.Repeat("n", 65), Kind: "action", Match: "click"}},
		{{Name: "long", Kind: "action", Match: strings.Repeat("x", 257)}},
	}
	for _, goals := range invalid {
		if err := ValidateGoals(goals); err == nil {
			t.Fatalf("SECURITY: invalid analytics goal was accepted: %#v", goals)
		}
	}
	many := make([]Goal, 33)
	for i := range many {
		many[i] = Goal{Name: "x", Kind: "action", Match: "click"}
	}
	if err := ValidateGoals(many); err == nil {
		t.Fatal("SECURITY: analytics accepted more than 32 goals")
	}
}

func TestSecurityBoundaryAnalyticsStoreRejectsDomainInjectionAndSymlinkDatabase(t *testing.T) {
	root := t.TempDir()
	for _, domain := range []string{"", "bad\x00domain", "bad\ndomain", strings.Repeat("a", 254)} {
		if _, err := storeDirectory(root, domain); err == nil {
			t.Fatalf("SECURITY: unsafe analytics domain %q was accepted", domain)
		}
	}

	directory, err := storeDirectory(root, "example.org")
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.db")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	databasePath := filepath.Join(directory, "analytics.db")
	if err := os.Symlink(outside, databasePath); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	store := OpenStore(root)
	defer store.Close()
	result := store.Exchange(StorageRequest{Operation: SaveTechnical, Domain: "example.org", Report: "{}"})
	if result.Err == nil || !strings.Contains(result.Err.Error(), "unsafe analytics database") {
		t.Fatalf("SECURITY: analytics followed database symlink: %v", result.Err)
	}
}

func TestSecurityBoundaryAnalyticsStorageBudgetsFailClosed(t *testing.T) {
	store := OpenStore(t.TempDir())
	defer store.Close()

	tests := []StorageRequest{
		{Operation: SaveSecurity, Domain: "example.org", State: strings.Repeat("s", (2<<20)+1)},
		{Operation: SaveTechnical, Domain: "example.org", Report: strings.Repeat("r", (2<<20)+1)},
		{Operation: SaveBrowser, Domain: "example.org", State: strings.Repeat("s", (6<<20)+1), Report: "{}"},
		{Operation: SaveBrowser, Domain: "example.org", State: "{}", Report: strings.Repeat("r", (3<<20)+1)},
	}
	for _, request := range tests {
		if result := store.Exchange(request); result.Err == nil {
			t.Fatalf("SECURITY: oversized analytics payload was accepted for operation %d", request.Operation)
		}
	}

	if result := store.Exchange(StorageRequest{Operation: StorageOperation(255), Domain: "example.org"}); result.Err == nil {
		t.Fatal("unknown analytics storage operation was accepted")
	}
}

func TestSecurityBoundaryAnalyticsArchiveRejectsMalformedOrOversizedData(t *testing.T) {
	if _, err := DecodeArchive(strings.NewReader("not-gzip")); err == nil {
		t.Fatal("malformed analytics archive was accepted")
	}

	var buffer bytes.Buffer
	compressor := gzip.NewWriter(&buffer)
	if _, err := compressor.Write([]byte("{")); err != nil {
		t.Fatal(err)
	}
	if err := compressor.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeArchive(bytes.NewReader(buffer.Bytes())); err == nil {
		t.Fatal("malformed JSON analytics archive was accepted")
	}

	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	report := Report{Generated: now, PeriodEnd: now, Views: 1}
	encoded, _ := json.Marshal(map[string]Report{
		"not-a-date": report,
		dayKey(now.AddDate(0, 0, 1)): report,
		dayKey(now.AddDate(0, 0, -100)): report,
	})
	directory := t.TempDir()
	if err := writeDailyArchive(directory, string(encoded), now); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(directory, "archives"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("invalid/future/expired archive dates were written: %v", entries)
	}
}

func TestAnalyticsStoreExchangeStopsAfterCloseOrCancellation(t *testing.T) {
	store := OpenStore(t.TempDir())
	stop := make(chan struct{})
	close(stop)
	result := store.Exchange(StorageRequest{Operation: SaveTechnical, Domain: "example.org", Report: "{}", Stop: stop})
	if result.Err == nil || !strings.Contains(result.Err.Error(), "canceled") {
		t.Fatalf("cancelled analytics request = %v", result.Err)
	}
	store.Close()
	result = store.Exchange(StorageRequest{Operation: SaveTechnical, Domain: "example.org", Report: "{}"})
	if result.Err == nil || !strings.Contains(result.Err.Error(), "closed") {
		t.Fatalf("closed analytics store = %v", result.Err)
	}
}

func TestAnalyticsCheckpointReadBudget(t *testing.T) {
	root := t.TempDir()
	store := OpenStore(root)
	defer store.Close()
	if result := store.Exchange(StorageRequest{Operation: SaveTechnical, Domain: "example.org", Report: strings.Repeat("x", 1024)}); result.Err != nil {
		t.Fatal(result.Err)
	}
	result := store.Exchange(StorageRequest{Operation: ReadTechnicalReport, Domain: "example.org", Limit: 10})
	if result.Err == nil || !strings.Contains(result.Err.Error(), "read budget") {
		t.Fatalf("checkpoint budget was not enforced: %v", result.Err)
	}
}

func TestAnalyticsWriteDailyArchiveRejectsMalformedJSON(t *testing.T) {
	err := writeDailyArchive(t.TempDir(), "{", time.Now().UTC())
	if err == nil {
		t.Fatal("malformed archive summary was accepted")
	}
	var syntaxError *json.SyntaxError
	if !errors.As(err, &syntaxError) {
		t.Fatalf("unexpected malformed archive error: %T %v", err, err)
	}
}

func TestAnalyticsStoreContextCancellationBeforeQueue(t *testing.T) {
	store := OpenStore(t.TempDir())
	defer store.Close()
	stop := make(chan struct{})
	close(stop)
	result := store.Exchange(StorageRequest{Operation: LoadHistory, Domain: "example.org", Stop: stop})
	if result.Err == nil {
		t.Fatal("cancelled analytics load unexpectedly succeeded")
	}
	_ = context.Canceled
}
