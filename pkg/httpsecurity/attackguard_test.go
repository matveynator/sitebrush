package httpsecurity

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAttackGuardDoesNotBlockReadBurst(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	guard, err := NewAttackGuard("")
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()
	for i := 0; i < 600; i++ {
		if block, blocked := guard.ObserveFast("203.0.113.7", "/same", false, now.Add(time.Duration(i)*time.Millisecond)); blocked {
			t.Fatalf("read burst unexpectedly blocked: %#v", block)
		}
	}
}

func TestAttackGuardStillBlocksMassWrites(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	guard, err := NewAttackGuard("")
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()
	var block SecurityBlock
	var blocked bool
	for i := 0; i < 120; i++ {
		block, blocked = guard.ObserveRequestFast("203.0.113.9", "/api/page", "POST", false, now.Add(time.Duration(i)*time.Millisecond))
	}
	if !blocked || block.Reason != "mass-write" {
		t.Fatalf("expected mass-write block, got blocked=%v reason=%q", blocked, block.Reason)
	}
}

func TestAttackGuardDoesNotRateBlockTrustedTraffic(t *testing.T) {
	now := time.Now().UTC()
	guard, err := NewAttackGuard("")
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()
	for i := 0; i < 2000; i++ {
		if _, blocked := guard.ObserveFast("203.0.113.8", "/import/page", true, now); blocked {
			t.Fatal("trusted traffic was rate blocked")
		}
	}
}

func TestAttackGuardBlocksSevereIncidentAndPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "security.json")
	now := time.Now().UTC()
	guard, err := NewAttackGuard(path)
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()
	block, blocked := guard.ObserveIncident("2001:db8::1", "injection", "SQL injection pattern", now)
	if !blocked || block.Source != "local" {
		t.Fatalf("unexpected incident block: %#v", block)
	}
	if err := guard.saveSnapshot(); err != nil {
		t.Fatal(err)
	}
	loaded, err := NewAttackGuard(path)
	if err != nil {
		t.Fatal(err)
	}
	defer loaded.Close()
	persisted, ok := loaded.Check("2001:db8::1", now)
	if !ok || persisted.IncidentID != block.IncidentID {
		t.Fatalf("block did not survive reload: %#v", persisted)
	}
}

func TestAttackGuardManualAndEscalation(t *testing.T) {
	now := time.Now().UTC()
	guard, err := NewAttackGuard("")
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()
	first, err := guard.Add("198.51.100.4", "manual", "abuse report", "manual", time.Time{}, now)
	if err != nil {
		t.Fatal(err)
	}
	if first.Violations != 1 {
		t.Fatalf("violations=%d", first.Violations)
	}
	third := first
	for i := 0; i < 2; i++ {
		third, _ = guard.ObserveIncident("198.51.100.4", "injection", "repeat", now.Add(time.Duration(i+1)*time.Minute))
	}
	if third.ExpiresAt.Sub(third.LastEvent) < 29*24*time.Hour {
		t.Fatalf("repeat attacker was not escalated: %s", third.ExpiresAt.Sub(third.LastEvent))
	}
	if err := guard.Remove("198.51.100.4"); err != nil {
		t.Fatal(err)
	}
	if _, ok := guard.Check("198.51.100.4", now); ok {
		t.Fatal("manual unblock failed")
	}
}

func TestBlockedHTMLIncludesDiagnosticsAndDeveloperGuidance(t *testing.T) {
	now := time.Date(2026, 9, 24, 10, 0, 0, 0, time.UTC)
	block := SecurityBlock{
		IP: "203.0.113.17", Reason: "authentication-failures", Description: "failed login at /?login",
		LastEvent: now, ExpiresAt: now.Add(7 * 24 * time.Hour), IncidentID: "abc", Source: "global",
		ReasonLog: []SecurityReasonEvent{{First: now.Add(-2 * time.Minute), At: now, Reason: "authentication-failures", Description: "Repeated authentication failures; latest request: /?login (HTTP 401) <script>", Count: 3, Source: "global"}},
	}
	body := blockedHTMLAt("ru-RU", block, now)
	for _, expected := range []string{"Запрос заблокирован", "203.0.113.17", "Источник блокировки", "global", "7 дн. 0 ч.", "2026-10-01T10:00:00Z", "2026-09-24T09:58:00Z", "Повторные ошибки аутентификации", "Отклонённая попытка входа; последний запрос: /?login", "×3", "localhost", "httptest", "Auto-block", "&lt;script&gt;"} {
		if !strings.Contains(body, expected) {
			t.Errorf("blocked page missing %q", expected)
		}
	}
	if strings.Contains(body, "<script>") {
		t.Fatalf("unescaped block description in HTML: %s", body)
	}
	if len(body) > 5000 {
		t.Fatalf("blocked response unexpectedly large: %d", len(body))
	}
}

func TestAttackGuardGlobalSyncDefaultsOn(t *testing.T) {
	guard, err := NewAttackGuard(filepath.Join(t.TempDir(), "security.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()
	settings, err := guard.Settings()
	if err != nil {
		t.Fatal(err)
	}
	if !settings.GlobalSync {
		t.Fatal("global reputation sync must default to enabled")
	}
}

func TestAttackGuardAllowlistOverridesLocalAndGlobalBlocks(t *testing.T) {
	now := time.Now().UTC()
	guard, err := NewAttackGuard("")
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()

	if _, err := guard.Add("203.0.113.111", "manual", "temporary test", "manual", now.Add(time.Hour), now); err != nil {
		t.Fatal(err)
	}
	if err := guard.Allow("203.0.113.111", "trusted proxy"); err != nil {
		t.Fatal(err)
	}
	if !guard.IsAllowed("203.0.113.111") {
		t.Fatal("allowlisted IP was not recognized")
	}
	if _, blocked := guard.Check("203.0.113.111", now); blocked {
		t.Fatal("allowlisted IP remained blocked")
	}
	if err := guard.ApplyGlobal("203.0.113.111", "scanner-client", "confirmed globally", now, now.Add(7*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, blocked := guard.Check("203.0.113.111", now); blocked {
		t.Fatal("global reputation bypassed allowlist")
	}
}

func TestAttackGuardAdministratorTrustIsPerSiteAndExpires(t *testing.T) {
	guard, err := NewAttackGuard("")
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	if err := guard.TrustAdminIP("Example.COM.", "203.0.113.45", now); err != nil {
		t.Fatal(err)
	}
	if !guard.AdminIPTrusted("example.com", "203.0.113.45", now.Add(time.Minute)) {
		t.Fatal("authenticated administrator address was not trusted for its site")
	}
	if guard.AdminIPTrusted("another.example", "203.0.113.45", now.Add(time.Minute)) {
		t.Fatal("administrator address trust leaked to a different site")
	}
	if guard.AdminIPTrusted("example.com", "203.0.113.45", now.Add(trustedAdminIPCacheTTL+time.Second)) {
		t.Fatal("expired administrator address remained trusted")
	}
	if !guard.ClaimAdminIPLookup("example.com", "203.0.113.45", now) || guard.ClaimAdminIPLookup("example.com", "203.0.113.45", now.Add(time.Second)) || !guard.ClaimAdminIPLookup("example.com", "203.0.113.45", now.Add(time.Minute)) {
		t.Fatal("administrator trust database lookup was not rate bounded")
	}
}

func TestAttackGuardKeepsSevenDayReasonHistory(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	guard, err := NewAttackGuard("")
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()

	first, blocked := guard.ObserveIncident("198.51.100.81", "injection", "first pattern", now.Add(-8*24*time.Hour))
	if !blocked {
		t.Fatal("first incident did not block")
	}
	if len(first.ReasonLog) != 1 {
		t.Fatalf("first reason log=%d", len(first.ReasonLog))
	}
	second, blocked := guard.ObserveIncident("198.51.100.81", "traversal", "second pattern", now)
	if !blocked {
		t.Fatal("second incident did not block")
	}
	if len(second.ReasonLog) != 1 {
		t.Fatalf("expired reason history was not pruned: %#v", second.ReasonLog)
	}
	if second.ReasonLog[0].Reason != "traversal" {
		t.Fatalf("unexpected current reason history: %#v", second.ReasonLog)
	}
}

func TestAttackGuardPersistsAllowlist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "security.json")
	guard, err := NewAttackGuard(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := guard.Allow("192.0.2.91", "office gateway"); err != nil {
		t.Fatal(err)
	}
	if err := guard.saveSnapshot(); err != nil {
		t.Fatal(err)
	}
	guard.Close()

	loaded, err := NewAttackGuard(path)
	if err != nil {
		t.Fatal(err)
	}
	defer loaded.Close()
	if !loaded.IsAllowed("192.0.2.91") {
		t.Fatal("allowlist did not survive reload")
	}
	entries, err := loaded.Allowlist()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Comment != "office gateway" {
		t.Fatalf("unexpected persisted allowlist: %#v", entries)
	}
}

func TestAttackGuardAggregatesRepeatedReasonHistory(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	guard, err := NewAttackGuard("")
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()

	var block SecurityBlock
	for index := 0; index < 50; index++ {
		block, _ = guard.ObserveIncident("198.51.100.82", "secret", "sensitive file probe", now.Add(time.Duration(index)*time.Second))
	}
	if len(block.ReasonLog) != 1 {
		t.Fatalf("repeated reason history was not aggregated: %#v", block.ReasonLog)
	}
	event := block.ReasonLog[0]
	if event.Count != 50 || event.Reason != "secret" {
		t.Fatalf("unexpected aggregated reason: %#v", event)
	}
	if event.First != now || !event.At.Equal(now.Add(49*time.Second)) {
		t.Fatalf("unexpected aggregation window: %#v", event)
	}
}

func TestAttackGuardReasonHistoryIsBounded(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	events := make([]SecurityReasonEvent, 0, securityReasonLogLimit+10)
	for index := 0; index < securityReasonLogLimit+10; index++ {
		eventTime := now.Add(time.Duration(index) * time.Minute)
		events = appendSecurityReasonEvent(events, SecurityReasonEvent{
			First:  eventTime,
			At:     eventTime,
			Reason: "reason-" + time.Duration(index).String(),
			Source: "local",
			Count:  1,
		}, eventTime)
	}
	if len(events) != securityReasonLogLimit {
		t.Fatalf("reason history size=%d", len(events))
	}
}

func TestAttackGuardAggregatesRepeatedGlobalReasonHistory(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	guard, err := NewAttackGuard("")
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()

	for index := 0; index < 6; index++ {
		observed := now.Add(time.Duration(index) * 10 * time.Minute)
		if err := guard.ApplyGlobal("198.51.100.90", "secret", "confirmed globally", observed, observed.Add(7*24*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	block, blocked := guard.Check("198.51.100.90", now.Add(time.Hour))
	if !blocked {
		t.Fatal("global block missing")
	}
	if len(block.ReasonLog) != 1 || block.ReasonLog[0].Count != 6 {
		t.Fatalf("global reason history was not aggregated: %#v", block.ReasonLog)
	}
}

func TestAttackGuardReasonAggregateResetsOutsideRetentionWindow(t *testing.T) {
	start := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	events := []SecurityReasonEvent{}
	for day := 0; day < 9; day++ {
		observed := start.Add(time.Duration(day) * 24 * time.Hour)
		events = appendSecurityReasonEvent(events, SecurityReasonEvent{
			First:       observed,
			At:          observed,
			Reason:      "secret",
			Description: "sensitive file probe",
			Source:      "local",
			Count:       1,
		}, observed)
	}
	if len(events) != 1 {
		t.Fatalf("expired aggregate remained visible: %#v", events)
	}
	if events[0].Count != 1 {
		t.Fatalf("expired observations remained in count: %#v", events)
	}
	if !events[0].First.Equal(start.Add(8 * 24 * time.Hour)) {
		t.Fatalf("new aggregate kept stale first timestamp: %#v", events[0])
	}
}

func TestAttackGuardBlocksMassEnumerationEarly(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	guard, err := NewAttackGuard("")
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()

	var block SecurityBlock
	var blocked bool
	for requestIndex := 0; requestIndex < 48; requestIndex++ {
		block, blocked = guard.ObserveRequestFast(
			"203.0.113.84",
			"/scan-"+time.Duration(requestIndex).String(),
			"GET",
			false,
			now.Add(time.Duration(requestIndex)*time.Millisecond),
		)
	}
	if !blocked || block.Reason != "mass-enumeration" {
		t.Fatalf("expected early mass-enumeration block, got blocked=%v reason=%q", blocked, block.Reason)
	}
	if !strings.Contains(block.Description, "48 distinct paths") {
		t.Fatalf("description is not concrete: %q", block.Description)
	}
}


func TestAttackGuardDoesNotBlockAuthenticationFailures(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	guard, err := NewAttackGuard("")
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()

	for index := 0; index < 20; index++ {
		block, blocked := guard.ObserveIncident(
			"203.0.113.200",
			"authentication-failures",
			"repeated login failure",
			now.Add(time.Duration(index)*time.Second),
		)
		if blocked {
			t.Fatalf("authentication failures must not create an IP block: %#v", block)
		}
	}

	if block, blocked := guard.Check("203.0.113.200", now.Add(time.Minute)); blocked {
		t.Fatalf("authentication failure analytics leaked into IP blocking: %#v", block)
	}
}


func TestAutomaticSecurityBlockTTLProgression(t *testing.T) {
	want := []time.Duration{
		24 * time.Hour,
		2 * 24 * time.Hour,
		4 * 24 * time.Hour,
		8 * 24 * time.Hour,
		16 * 24 * time.Hour,
		32 * 24 * time.Hour,
		32 * 24 * time.Hour,
	}
	for i, expected := range want {
		if got := automaticSecurityBlockTTL(i + 1); got != expected {
			t.Fatalf("violation %d ttl=%s want=%s", i+1, got, expected)
		}
	}
}

func TestAttackGuardRetainsEscalationAfterBlockExpiry(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	guard, err := NewAttackGuard("")
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()

	first, blocked := guard.ObserveIncident("203.0.113.201", "repository", "requested /.git/config", now)
	if !blocked {
		t.Fatal("first confirmed repository probe did not block")
	}
	if got := first.ExpiresAt.Sub(first.LastEvent); got != 24*time.Hour {
		t.Fatalf("first block ttl=%s", got)
	}
	if _, blocked := guard.Check("203.0.113.201", now.Add(25*time.Hour)); blocked {
		t.Fatal("expired first block remained active")
	}
	second, blocked := guard.ObserveIncident("203.0.113.201", "repository", "requested /.git/config again", now.Add(25*time.Hour))
	if !blocked {
		t.Fatal("repeat repository probe did not block")
	}
	if got := second.ExpiresAt.Sub(second.LastEvent); got != 2*24*time.Hour {
		t.Fatalf("second block ttl=%s want=48h", got)
	}
}

func TestAttackGuardRequiresRepeatedHeuristicIncidents(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	guard, err := NewAttackGuard("")
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()

	for i := 1; i <= 2; i++ {
		if block, blocked := guard.ObserveIncident("203.0.113.202", "scanner-client", "scanner-like user agent", now.Add(time.Duration(i)*time.Second)); blocked {
			t.Fatalf("scanner-client blocked on signal %d: %#v", i, block)
		}
	}
	if block, blocked := guard.ObserveIncident("203.0.113.202", "scanner-client", "scanner-like user agent", now.Add(3*time.Second)); !blocked {
		t.Fatal("third scanner-client signal did not block")
	} else if block.ExpiresAt.Sub(block.LastEvent) != 24*time.Hour {
		t.Fatalf("unexpected first automatic ttl: %s", block.ExpiresAt.Sub(block.LastEvent))
	}
}
