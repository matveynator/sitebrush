package httpsecurity

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
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
	if block, blocked := guard.ObserveIncident("2001:db8::1", "injection", "SQL injection pattern", now); blocked {
		t.Fatalf("single injection heuristic unexpectedly blocked: %#v", block)
	}
	block, blocked := guard.ObserveIncident("2001:db8::1", "injection", "SQL injection pattern", now.Add(time.Second))
	if !blocked || block.Source != "local" {
		t.Fatalf("repeated injection incident did not block: %#v", block)
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
	if block, blocked := guard.ObserveIncident("198.51.100.4", "injection", "repeat", now.Add(time.Second)); blocked {
		t.Fatalf("single injection heuristic unexpectedly blocked: %#v", block)
	}
	third, blocked := guard.ObserveIncident("198.51.100.4", "injection", "repeat", now.Add(2*time.Second))
	if !blocked {
		t.Fatal("repeated injection heuristic did not block")
	}
	if got := third.ExpiresAt.Sub(third.LastEvent); got != 2*24*time.Hour {
		t.Fatalf("repeat attacker ttl=%s want=48h", got)
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
	for _, expected := range []string{"Запрос заблокирован", "203.0.113.17", "Источник блокировки", "Глобальная репутация", "7 дн. 0 ч.", "2026-10-01T10:00:00Z", "2026-09-24T09:58:00Z", "Повторные ошибки аутентификации", "Отклонённая попытка входа; последний запрос: /?login", "×3", "Отправить инцидент администратору", "Аналитика → Безопасность", "localhost", "httptest", "Auto-block", "&lt;script&gt;"} {
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

	first, blocked := guard.ObserveIncident("198.51.100.81", "repository", "first pattern", now.Add(-8*24*time.Hour))
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

func TestLocalAutomaticBlockAppliesOnlyToItsSite(t *testing.T) {
	now := time.Now().UTC()
	guard, err := NewAttackGuard("")
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()

	const clientIP = "203.0.113.45"
	for requestIndex := 0; requestIndex < 48; requestIndex++ {
		guard.ObserveSiteNotFoundFastDisposition("a.sitebrush.com", clientIP, "/missing-"+strconv.Itoa(requestIndex), now)
	}

	block, blocked := guard.CheckSite("a.sitebrush.com", clientIP, now)
	if !blocked || block.Source != "local" || block.Domain != "a.sitebrush.com" {
		t.Fatalf("original site block = %#v, blocked=%t", block, blocked)
	}
	if block, blocked := guard.CheckSite("b.sitebrush.com", clientIP, now); blocked {
		t.Fatalf("local block leaked to neighboring site: %#v", block)
	}
	if block, blocked := guard.Check(clientIP, now); !blocked || block.Domain != "a.sitebrush.com" {
		t.Fatalf("instance security view lost the local block: %#v, blocked=%t", block, blocked)
	}
}

func TestManualSecurityBlockAppliesAcrossSites(t *testing.T) {
	now := time.Now().UTC()
	guard, err := NewAttackGuard("")
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()

	const clientIP = "203.0.113.46"
	if _, err := guard.Add(clientIP, "manual", "administrator block", "manual", time.Time{}, now); err != nil {
		t.Fatal(err)
	}
	for _, domain := range []string{"a.sitebrush.com", "b.sitebrush.com"} {
		if block, blocked := guard.CheckSite(domain, clientIP, now); !blocked || block.Source != "manual" {
			t.Fatalf("manual block on %s = %#v, blocked=%t", domain, block, blocked)
		}
	}
}

func TestAttackGuardCountsOnlyNotFoundPathsAsEnumeration(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	guard, err := NewAttackGuard("")
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()

	for requestIndex := 0; requestIndex < 64; requestIndex++ {
		_, blocked, _ := guard.ObserveSiteRequestFastDisposition(
			"example.org",
			"203.0.113.85",
			"/page-"+strconv.Itoa(requestIndex),
			"GET",
			false,
			now.Add(time.Duration(requestIndex)*time.Millisecond),
		)
		if blocked {
			t.Fatalf("valid page request %d was blocked", requestIndex+1)
		}
	}
	if block, blocked := guard.Check("203.0.113.85", now.Add(time.Second)); blocked {
		t.Fatalf("valid page traffic created an enumeration block: %#v", block)
	}

	for requestIndex := 0; requestIndex < 48; requestIndex++ {
		block, blocked, _ := guard.ObserveSiteNotFoundFastDisposition(
			"example.org",
			"203.0.113.86",
			"/missing-"+strconv.Itoa(requestIndex),
			now.Add(time.Duration(requestIndex)*time.Millisecond),
		)
		if requestIndex < 47 && blocked {
			t.Fatalf("not-found path %d triggered early block: %#v", requestIndex+1, block)
		}
		if requestIndex == 47 && (!blocked || block.Reason != "mass-enumeration") {
			t.Fatalf("48th not-found path did not block enumeration: %#v", block)
		}
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


func TestAttackGuardIncidentReportIsOneShot(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	guard, err := NewAttackGuard("")
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()

	block, blocked := guard.ObserveSiteIncident("example.com", "203.0.113.203", "repository", "requested /.git/config", now)
	if !blocked {
		t.Fatal("repository probe did not block")
	}
	if block.Domain != "example.com" {
		t.Fatalf("block domain=%q", block.Domain)
	}
	claimed, allowed, err := guard.ClaimIncidentReport(block.IP, block.IncidentID, now.Add(time.Minute))
	if err != nil || !allowed {
		t.Fatalf("first report claim allowed=%v err=%v block=%#v", allowed, err, claimed)
	}
	if _, allowed, err := guard.ClaimIncidentReport(block.IP, block.IncidentID, now.Add(time.Minute)); err != nil || allowed {
		t.Fatalf("second report claim allowed=%v err=%v", allowed, err)
	}
	reported, err := guard.FinishIncidentReport(block.IP, block.IncidentID, "shared VPN address", true, now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if reported.ReportedAt.IsZero() || reported.ReportMessage != "shared VPN address" {
		t.Fatalf("report status not stored: %#v", reported)
	}
	if _, allowed, err := guard.ClaimIncidentReport(block.IP, block.IncidentID, now.Add(3*time.Minute)); err != nil || allowed {
		t.Fatalf("reported incident became claimable again: allowed=%v err=%v", allowed, err)
	}
}


func TestAttackGuardDropsPersistedAuthenticationFailureBlocks(t *testing.T) {
	now := time.Now().UTC()
	path := filepath.Join(t.TempDir(), "security.json")
	state := attackGuardDiskState{
		Version: 2,
		Settings: SecuritySettings{AutoBlock: true, GlobalSync: true},
		Blocks: []SecurityBlock{
			{
				IP: "203.0.113.210",
				Reason: "authentication-failures",
				Description: "legacy automatic auth failure block",
				Source: "local",
				LastEvent: now,
				ExpiresAt: now.Add(7 * 24 * time.Hour),
				Violations: 1,
				IncidentID: "legacy-auth",
			},
			{
				IP: "203.0.113.211",
				Reason: "authentication-failures",
				Description: "administrator chose to block manually",
				Source: "manual",
				LastEvent: now,
				ExpiresAt: now.Add(7 * 24 * time.Hour),
				Violations: 1,
				IncidentID: "manual-auth",
			},
		},
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, encoded, 0600); err != nil {
		t.Fatal(err)
	}

	guard, err := NewAttackGuard(path)
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()

	if block, blocked := guard.Check("203.0.113.210", now); blocked {
		t.Fatalf("legacy automatic authentication block survived upgrade: %#v", block)
	}
	if block, blocked := guard.Check("203.0.113.211", now); !blocked || block.Source != "manual" {
		t.Fatalf("manual administrator block was incorrectly removed: %#v", block)
	}
}


func TestAttackGuardIncidentReportFailureReleasesClaim(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	guard, err := NewAttackGuard("")
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()

	block, blocked := guard.ObserveSiteIncident("example.com", "203.0.113.204", "repository", "requested /.git/config", now)
	if !blocked {
		t.Fatal("repository probe did not block")
	}
	if _, allowed, err := guard.ClaimIncidentReport(block.IP, block.IncidentID, now.Add(time.Minute)); err != nil || !allowed {
		t.Fatalf("initial report claim allowed=%v err=%v", allowed, err)
	}
	if _, err := guard.FinishIncidentReport(block.IP, block.IncidentID, "mail failed", false, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, allowed, err := guard.ClaimIncidentReport(block.IP, block.IncidentID, now.Add(3*time.Minute)); err != nil || !allowed {
		t.Fatalf("failed delivery did not release claim: allowed=%v err=%v", allowed, err)
	}
}

func TestAttackGuardRejectsIncidentReportForWrongIncidentOrIP(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	guard, err := NewAttackGuard("")
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()

	block, blocked := guard.ObserveSiteIncident("example.com", "203.0.113.205", "repository", "requested /.git/config", now)
	if !blocked {
		t.Fatal("repository probe did not block")
	}
	if _, allowed, err := guard.ClaimIncidentReport(block.IP, "wrong-incident", now.Add(time.Minute)); err == nil || allowed {
		t.Fatalf("wrong incident unexpectedly claimable: allowed=%v err=%v", allowed, err)
	}
	if _, allowed, err := guard.ClaimIncidentReport("203.0.113.206", block.IncidentID, now.Add(time.Minute)); err == nil || allowed {
		t.Fatalf("wrong IP unexpectedly claimable: allowed=%v err=%v", allowed, err)
	}
}

func TestSecuritySourceLabels(t *testing.T) {
	tests := []struct {
		source   string
		language string
		want     string
	}{
		{"local", "ru", "Обнаружено этим сервером"},
		{"global", "ru", "Глобальная репутация: подтверждено несколькими серверами SiteBrush"},
		{"manual", "ru", "Добавлено администратором вручную"},
		{"local", "de", "Von diesem Server erkannt"},
		{"global", "de", "Globale Reputation: von mehreren SiteBrush-Servern bestätigt"},
		{"manual", "de", "Manuell vom Administrator hinzugefügt"},
		{"local", "en", "Detected by this server"},
		{"global", "en", "Global reputation: confirmed by multiple SiteBrush servers"},
		{"manual", "en", "Added manually by the administrator"},
		{"custom", "en", "custom"},
	}
	for _, test := range tests {
		if got := securitySourceLabel(test.source, test.language); got != test.want {
			t.Fatalf("source=%q language=%q got=%q want=%q", test.source, test.language, got, test.want)
		}
	}
}

func TestSecurityCategoryThresholds(t *testing.T) {
	tests := map[string]int{
		"repository": 1,
		"secret": 1,
		"traversal": 1,
		"enumeration": 1,
		"injection": 2,
		"source-backup": 3,
		"scanner-client": 3,
		"authentication-failures": 0,
		"unknown": 0,
	}
	for category, want := range tests {
		if got := securityCategoryThreshold(category); got != want {
			t.Fatalf("category=%q threshold=%d want=%d", category, got, want)
		}
		if got := securityCategoryBlocks(category); got != (want > 0) {
			t.Fatalf("category=%q blocks=%v want=%v", category, got, want > 0)
		}
	}
}

func TestAttackGuardPersistenceSnapshotKeepsExpiredEscalationHistory(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	guard, err := NewAttackGuard("")
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()

	if _, blocked := guard.ObserveIncident("203.0.113.207", "repository", "requested /.git/config", now); !blocked {
		t.Fatal("repository probe did not block")
	}
	if snapshot, err := guard.Snapshot(now.Add(25 * time.Hour)); err != nil {
		t.Fatal(err)
	} else if len(snapshot) != 0 {
		t.Fatalf("expired block remained in active snapshot: %#v", snapshot)
	}
	history, err := guard.persistenceSnapshot(now.Add(25 * time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].Violations != 1 {
		t.Fatalf("expired escalation history missing: %#v", history)
	}
}


// BEGIN bounded security state and queue regression tests.

func TestAttackGuardBoundsFreshEnumerationWindows(t *testing.T) {
	blocks := map[string]SecurityBlock{}
	allowlist := map[string]SecurityAllow{}
	trustedAdminIPs := map[string]time.Time{}
	adminIPLookups := map[string]time.Time{}
	windows := map[string]*attackWindow{}
	incidents := map[string]*incidentWindow{}
	reportClaims := map[string]bool{}
	settings := SecuritySettings{AutoBlock: true}
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)

	for index := 0; index < attackGuardWindowLimit+512; index++ {
		ip := "203.0." + strconv.Itoa(index/256) + "." + strconv.Itoa(index%256)
		result := handleAttackGuardRequest(
			blocks,
			allowlist,
			trustedAdminIPs,
			adminIPLookups,
			windows,
			incidents,
			reportClaims,
			&settings,
			attackGuardRequest{
				Operation:  attackGuardObserveFast,
				IP:         ip,
				Path:       "/probe-" + strconv.Itoa(index),
				Method:     http.MethodGet,
				StatusCode: http.StatusNotFound,
				Now:        now,
			},
		)
		if result.Blocked {
			t.Fatalf("single probe for %s unexpectedly blocked", ip)
		}
	}
	if len(windows) != attackGuardWindowLimit {
		t.Fatalf("fresh enumeration windows=%d want=%d", len(windows), attackGuardWindowLimit)
	}
}

func TestAttackGuardBoundsFreshIncidentWindows(t *testing.T) {
	blocks := map[string]SecurityBlock{}
	allowlist := map[string]SecurityAllow{}
	trustedAdminIPs := map[string]time.Time{}
	adminIPLookups := map[string]time.Time{}
	windows := map[string]*attackWindow{}
	incidents := map[string]*incidentWindow{}
	reportClaims := map[string]bool{}
	settings := SecuritySettings{AutoBlock: true}
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)

	for index := 0; index < attackGuardWindowLimit+512; index++ {
		ip := "198.51." + strconv.Itoa(index/256) + "." + strconv.Itoa(index%256)
		result := handleAttackGuardRequest(
			blocks,
			allowlist,
			trustedAdminIPs,
			adminIPLookups,
			windows,
			incidents,
			reportClaims,
			&settings,
			attackGuardRequest{
				Operation:   attackGuardObserveIncident,
				IP:          ip,
				Category:    "injection",
				Description: "single suspicious request",
				Now:         now,
			},
		)
		if result.Blocked {
			t.Fatalf("single injection signal for %s unexpectedly blocked", ip)
		}
	}
	if len(incidents) != attackGuardWindowLimit {
		t.Fatalf("fresh incident windows=%d want=%d", len(incidents), attackGuardWindowLimit)
	}
}

func TestAttackGuardFullShardQueueReturnsBackpressureWithoutHanging(t *testing.T) {
	guard, err := NewAttackGuard("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		close(guard.shutdown)
		select {
		case <-guard.done:
		case <-time.After(time.Second):
			t.Fatal("AttackGuard persistence goroutine did not stop")
		}
	})

	const ip = "203.0.113.208"
	shardIndex := attackGuardShardIndex(ip)
	blockedReply := make(chan attackGuardResult)
	guard.shards[shardIndex] <- attackGuardRequest{
		Operation: attackGuardCheck,
		IP:        ip,
		Now:       time.Now().UTC(),
		Reply:     blockedReply,
	}

	deadline := time.Now().Add(time.Second)
	for len(guard.shards[shardIndex]) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("AttackGuard shard did not begin the blocking reply")
		}
		time.Sleep(time.Millisecond)
	}

	for queued := 0; queued < cap(guard.shards[shardIndex]); queued++ {
		guard.shards[shardIndex] <- attackGuardRequest{
			Operation: attackGuardCheck,
			IP:        ip,
			Now:       time.Now().UTC(),
		}
	}

	fastDone := make(chan bool, 1)
	go func() {
		_, blocked := guard.ObserveFast(ip, "/probe", false, time.Now().UTC())
		fastDone <- blocked
	}()
	select {
	case blocked := <-fastDone:
		if blocked {
			t.Fatal("queue backpressure was reported as a security block")
		}
	case <-time.After(time.Second):
		t.Fatal("AttackGuard fast path hung behind a full shard queue")
	}

	adminDone := make(chan error, 1)
	go func() {
		_, addErr := guard.Add(ip, "manual", "test", "manual", time.Now().Add(time.Hour), time.Now().UTC())
		adminDone <- addErr
	}()
	select {
	case addErr := <-adminDone:
		if addErr == nil || !strings.Contains(addErr.Error(), "busy") {
			t.Fatalf("full queue admin result=%v want busy error", addErr)
		}
	case <-time.After(time.Second):
		t.Fatal("AttackGuard admin path hung behind a full shard queue")
	}
}

// END bounded security state and queue regression tests.
