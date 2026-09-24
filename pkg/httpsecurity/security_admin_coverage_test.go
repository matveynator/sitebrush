package httpsecurity

import (
	"strings"
	"testing"
	"time"
)

func TestAttackGuardAdministrativeSecurityLifecycle(t *testing.T) {
	guard, err := NewAttackGuard("")
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()

	now := time.Unix(1_800_300_000, 0).UTC()
	ip := "203.0.113.70"
	block, err := guard.Add(ip, "traversal", "GET /../../etc/passwd", "manual", now.Add(time.Hour), now)
	if err != nil || block.IP != ip {
		t.Fatalf("add block = %#v, %v", block, err)
	}
	if err := guard.Update(ip, "repository", "GET /.git/config"); err != nil {
		t.Fatalf("update block: %v", err)
	}
	snapshot, err := guard.Snapshot(now)
	if err != nil || len(snapshot) != 1 || snapshot[0].Reason != "repository" {
		t.Fatalf("snapshot = %#v, %v", snapshot, err)
	}

	if err := guard.Allow(ip, "security test"); err != nil {
		t.Fatalf("allowlist add: %v", err)
	}
	if !guard.IsAllowed(ip) {
		t.Fatal("allowlisted address was not allowed")
	}
	allowlist, err := guard.Allowlist()
	if err != nil || len(allowlist) != 1 || allowlist[0].IP != ip {
		t.Fatalf("allowlist = %#v, %v", allowlist, err)
	}
	if err := guard.Disallow(ip); err != nil {
		t.Fatalf("allowlist remove: %v", err)
	}
	if guard.IsAllowed(ip) {
		t.Fatal("SECURITY: removed allowlist entry remained active")
	}

	settings := SecuritySettings{AutoBlock: false, GlobalSync: false}
	if err := guard.SetSettings(settings); err != nil {
		t.Fatalf("set settings: %v", err)
	}
	gotSettings, err := guard.Settings()
	if err != nil || gotSettings != settings {
		t.Fatalf("settings = %#v, %v", gotSettings, err)
	}

	if err := guard.Remove(ip); err != nil {
		t.Fatalf("remove block: %v", err)
	}
	if _, blocked := guard.Check(ip, now); blocked {
		t.Fatal("SECURITY: removed block remained enforceable")
	}
}

func TestAttackGuardPruningSecurityBoundaries(t *testing.T) {
	now := time.Unix(1_800_300_100, 0).UTC()
	trusted := map[string]time.Time{
		"fresh": now.Add(-time.Minute),
		"stale": now.Add(-trustedAdminIPCacheTTL - time.Second),
	}
	lookups := map[string]time.Time{
		"fresh": now.Add(-time.Minute),
		"stale": now.Add(-time.Hour - time.Second),
	}
	pruneAdminIPTrustState(trusted, lookups, now)
	if _, ok := trusted["stale"]; ok {
		t.Fatal("SECURITY: stale administrator trust was not pruned")
	}
	if _, ok := lookups["stale"]; ok {
		t.Fatal("SECURITY: stale administrator lookup cache was not pruned")
	}

	blocks := map[string]SecurityBlock{
		"old": {
			IP:        "203.0.113.71",
			LastEvent: now.Add(-securityEscalationHistoryTTL - time.Second),
		},
		"fresh": {
			IP:        "203.0.113.72",
			LastEvent: now,
			ReasonLog: []SecurityReasonEvent{
				{At: now.Add(-securityReasonHistoryTTL - time.Second), Reason: "old"},
				{At: now, Reason: "fresh"},
			},
		},
	}
	windows := map[string]*attackWindow{
		"old": {Started: now.Add(-time.Minute - time.Second)},
		"fresh": {Started: now},
	}
	incidents := map[string]*incidentWindow{
		"old": {Started: now.Add(-time.Minute - time.Second)},
		"fresh": {Started: now},
	}
	pruneAttackGuardState(blocks, windows, incidents, now)
	if _, ok := blocks["old"]; ok {
		t.Fatal("stale security escalation history was not pruned")
	}
	if len(blocks["fresh"].ReasonLog) != 1 || blocks["fresh"].ReasonLog[0].Reason != "fresh" {
		t.Fatalf("reason history = %#v", blocks["fresh"].ReasonLog)
	}
	if _, ok := windows["old"]; ok {
		t.Fatal("stale attack window was not pruned")
	}
	if _, ok := incidents["old"]; ok {
		t.Fatal("stale incident window was not pruned")
	}
}

func TestBlockedHTMLAndSecurityLabelsEscapeAttackerControlledContent(t *testing.T) {
	block := SecurityBlock{
		IP:          "<script>alert(1)</script>",
		IncidentID:  "<img src=x onerror=alert(1)>",
		Reason:      "traversal",
		Description: "<svg/onload=alert(1)>",
		Source:      "<b>global</b>",
		LastEvent:   time.Unix(1_800_300_000, 0).UTC(),
		ExpiresAt:   time.Unix(1_800_300_000, 0).UTC().Add(time.Hour),
		ReasonLog: []SecurityReasonEvent{{
			At:          time.Unix(1_800_300_000, 0).UTC(),
			Reason:      "repository",
			Description: "<script>event</script>",
			Source:      "local",
		}},
	}
	for _, language := range []string{"en", "ru", "de"} {
		body := BlockedHTML(language, block)
		if strings.Contains(body, "<script>") || strings.Contains(body, "<svg") || strings.Contains(body, "<img ") {
			t.Fatalf("SECURITY: blocked page rendered injected HTML for %s: %s", language, body)
		}
		if !strings.Contains(body, "&lt;") {
			t.Fatalf("blocked page did not escape attacker-controlled fields for %s", language)
		}
	}

	for _, duration := range []time.Duration{-time.Minute, 30 * time.Second, 90 * time.Minute, 49 * time.Hour} {
		if formatSecurityDuration(duration, "en") == "" || formatSecurityDuration(duration, "ru") == "" {
			t.Fatalf("empty duration label for %v", duration)
		}
	}
	for _, reason := range []string{"authentication-failures", "repository", "secret", "source-backup", "scanner-client", "injection", "traversal", "enumeration", "mass-write", "unknown"} {
		for _, language := range []string{"en", "ru", "de"} {
			if securityReasonLabel(reason, language) == "" {
				t.Fatalf("empty reason label %q/%q", reason, language)
			}
		}
	}
	if got := cleanSecurityText("  hello\nworld\t "); got == "" || strings.ContainsAny(got, "\n\r\t") {
		t.Fatalf("unsafe cleaned security text: %q", got)
	}
	if got := boundedPath(strings.Repeat("x", 5000)); len(got) > 1024 {
		t.Fatalf("bounded path length = %d", len(got))
	}
}

func TestThrottlePruningAndAdministrativeBranches(t *testing.T) {
	now := time.Unix(1_800_300_200, 0).UTC()
	observations := map[string]throttleObservation{
		"old": {Last: now.Add(-throttleContinuityGap - time.Second)},
		"fresh": {Last: now},
	}
	throttles := map[string]SecurityThrottle{
		"old": {IP: "203.0.113.80", ExpiresAt: now.Add(-throttleHistoryTTL - time.Second)},
		"fresh": {IP: "203.0.113.81", ExpiresAt: now.Add(time.Hour)},
	}
	rates := map[string]throttleRateWindow{
		"old": {Started: now.Add(-time.Minute - time.Second)},
		"fresh": {Started: now},
	}
	pruneThrottleState(observations, throttles, rates, now)
	if _, ok := observations["old"]; ok {
		t.Fatal("stale throttle observation was not pruned")
	}
	if _, ok := throttles["old"]; ok {
		t.Fatal("stale throttle history was not pruned")
	}
	if _, ok := rates["old"]; ok {
		t.Fatal("stale throttle rate window was not pruned")
	}

	guard, err := NewThrottleGuard("")
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()
	if err := guard.Remove("not-an-ip"); err == nil {
		t.Fatal("invalid throttle removal address was accepted")
	}
	if _, err := guard.Snapshot(now); err != nil {
		t.Fatalf("empty throttle snapshot: %v", err)
	}
	if _, err := guard.history(now); err != nil {
		t.Fatalf("empty throttle history: %v", err)
	}
	result, ok := guard.exchangeAdmin(throttleRequest{Operation: throttleRemove, IP: "invalid", Now: now})
	if !ok || result.Err == nil {
		t.Fatalf("invalid admin exchange = %#v, ok=%v", result, ok)
	}
}
