package httpsecurity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestThrottleInternalSecurityBranches(t *testing.T) {
	now := time.Unix(1_800_600_000, 0).UTC()
	observations := map[string]throttleObservation{}
	throttles := map[string]SecurityThrottle{}
	rates := map[string]throttleRateWindow{}

	if result := handleThrottleRequest(observations, throttles, rates, throttleRequest{
		Operation: throttleObserve, IP: "invalid", Now: now,
	}); result.Decision.Active {
		t.Fatal("invalid IP activated throttle")
	}
	if result := handleThrottleRequest(observations, throttles, rates, throttleRequest{
		Operation: throttleObserve, IP: "203.0.113.10", Trusted: true, Now: now,
	}); result.Decision.Active {
		t.Fatal("trusted IP activated throttle")
	}
	if result := handleThrottleRequest(observations, throttles, rates, throttleRequest{
		Operation: throttleObserve, IP: "203.0.113.10", ExemptObservation: true, Now: now,
	}); result.Decision.Active || len(observations) != 0 {
		t.Fatal("exempt observation polluted throttle history")
	}

	observations["203.0.113.20"] = throttleObservation{
		Started: now.Add(-throttleSustainedMinimum),
		Last:    now,
		Count:   throttleSustainedMinimumCount - 1,
	}
	result := handleThrottleRequest(observations, throttles, rates, throttleRequest{
		Operation: throttleObserve, IP: "203.0.113.20", Now: now,
	})
	if !result.Changed || !result.Decision.Active {
		t.Fatalf("sustained traffic did not begin throttle: %#v", result)
	}
	for index := 0; index < throttleAllowedRPS+1; index++ {
		result = handleThrottleRequest(observations, throttles, rates, throttleRequest{
			Operation: throttleObserve, IP: "203.0.113.20", Now: now.Add(time.Second),
		})
	}
	if !result.Decision.RateLimited || result.Decision.RetryAfter != time.Second {
		t.Fatalf("active throttle did not rate-limit excess traffic: %#v", result.Decision)
	}

	if got := handleThrottleRequest(observations, throttles, rates, throttleRequest{
		Operation: throttleSnapshot, Now: now.Add(time.Second),
	}); len(got.Throttles) != 1 {
		t.Fatalf("active snapshot=%#v", got.Throttles)
	}
	if got := handleThrottleRequest(observations, throttles, rates, throttleRequest{
		Operation: throttleHistory, Now: now.Add(time.Second),
	}); len(got.Throttles) != 1 {
		t.Fatalf("history=%#v", got.Throttles)
	}
	if got := handleThrottleRequest(observations, throttles, rates, throttleRequest{
		Operation: throttleRemove, IP: "203.0.113.20", Now: now,
	}); !got.Changed {
		t.Fatal("existing throttle removal was not reported")
	}
	if got := handleThrottleRequest(observations, throttles, rates, throttleRequest{
		Operation: throttleRemove, IP: "203.0.113.20", Now: now,
	}); got.Changed {
		t.Fatal("missing throttle removal reported a change")
	}
	if got := handleThrottleRequest(observations, throttles, rates, throttleRequest{
		Operation: throttleOperation(255), Now: now,
	}); got.Err == nil {
		t.Fatal("unknown throttle operation was accepted")
	}
}

func TestThrottlePruningAndDurationBoundaries(t *testing.T) {
	now := time.Unix(1_800_600_100, 0).UTC()
	observations := map[string]throttleObservation{
		"old": {Last: now.Add(-throttleContinuityGap - time.Second)},
		"new": {Last: now},
	}
	throttles := map[string]SecurityThrottle{
		"old": {ExpiresAt: now.Add(-throttleHistoryTTL - time.Second)},
		"new": {ExpiresAt: now.Add(time.Hour)},
	}
	rates := map[string]throttleRateWindow{
		"old": {Started: now.Add(-time.Minute - time.Second)},
		"new": {Started: now},
	}
	pruneThrottleState(observations, throttles, rates, now)
	if _, ok := observations["old"]; ok || observations["new"].Last.IsZero() {
		t.Fatalf("observation pruning=%#v", observations)
	}
	if _, ok := throttles["old"]; ok || throttles["new"].ExpiresAt.IsZero() {
		t.Fatalf("throttle pruning=%#v", throttles)
	}
	if _, ok := rates["old"]; ok || rates["new"].Started.IsZero() {
		t.Fatalf("rate pruning=%#v", rates)
	}

	if throttleDuration(-1) != time.Hour || throttleDuration(1) != time.Hour || throttleDuration(999) != 7*24*time.Hour {
		t.Fatal("throttle duration bounds are incorrect")
	}
	previous := SecurityThrottle{Episodes: 0}
	first := beginThrottle(previous, "203.0.113.1", now)
	if first.Episodes != 1 || first.LimitRPS != throttleAllowedRPS {
		t.Fatalf("first throttle=%#v", first)
	}
}

func TestThrottleDiskStateAndAdminErrorBranches(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.json")
	state, err := loadThrottleDiskState(missing)
	if err != nil || len(state.Throttles) != 0 {
		t.Fatalf("missing throttle state=%#v err=%v", state, err)
	}
	bad := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(bad, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadThrottleDiskState(bad); err == nil {
		t.Fatal("malformed throttle state was accepted")
	}

	guard, err := NewThrottleGuard("")
	if err != nil {
		t.Fatal(err)
	}
	if err := guard.Remove("not-an-ip"); err == nil {
		guard.Close()
		t.Fatal("invalid IP removal was accepted")
	}
	if err := guard.saveSnapshot(); err != nil {
		guard.Close()
		t.Fatal(err)
	}
	guard.Close()
	if _, ok := guard.exchangeFast(throttleRequest{Operation: throttleObserve, IP: "203.0.113.8"}); ok {
		t.Fatal("closed throttle guard accepted fast request")
	}
}

func TestAttackGuardDiskAndAdministrationFailureBranches(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.json")
	state, err := loadAttackGuardDiskState(missing)
	if err != nil || !state.Settings.AutoBlock || !state.Settings.GlobalSync {
		t.Fatalf("missing attack state=%#v err=%v", state, err)
	}
	bad := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(bad, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadAttackGuardDiskState(bad); err == nil {
		t.Fatal("malformed attack state was accepted")
	}

	now := time.Unix(1_800_600_200, 0).UTC()
	blocks := map[string]SecurityBlock{}
	allow := map[string]SecurityAllow{}
	trusted := map[string]time.Time{}
	lookups := map[string]time.Time{}
	windows := map[string]*attackWindow{}
	incidents := map[string]*incidentWindow{}
	claims := map[string]bool{}
	settings := SecuritySettings{AutoBlock: true, GlobalSync: true}

	if result := handleAttackGuardRequest(blocks, allow, trusted, lookups, windows, incidents, claims, &settings, attackGuardRequest{
		Operation: attackGuardAdd, IP: "invalid", Now: now,
	}); result.Err == nil {
		t.Fatal("invalid manual block IP was accepted")
	}
	handleAttackGuardRequest(blocks, allow, trusted, lookups, windows, incidents, claims, &settings, attackGuardRequest{
		Operation: attackGuardAllowAdd, IP: "203.0.113.30", Comment: "<script>trusted</script>", Now: now,
	})
	if result := handleAttackGuardRequest(blocks, allow, trusted, lookups, windows, incidents, claims, &settings, attackGuardRequest{
		Operation: attackGuardAdd, IP: "203.0.113.30", Now: now,
	}); result.Err == nil {
		t.Fatal("allowlisted address was manually blocked")
	}
	if result := handleAttackGuardRequest(blocks, allow, trusted, lookups, windows, incidents, claims, &settings, attackGuardRequest{
		Operation: attackGuardUpdate, IP: "203.0.113.31", Now: now,
	}); result.Err == nil {
		t.Fatal("missing block update was accepted")
	}
	if result := handleAttackGuardRequest(blocks, allow, trusted, lookups, windows, incidents, claims, &settings, attackGuardRequest{
		Operation: attackGuardTrustAdminIP, Domain: "", IP: "203.0.113.31", Now: now,
	}); result.Err == nil {
		t.Fatal("admin trust without domain was accepted")
	}
	if result := handleAttackGuardRequest(blocks, allow, trusted, lookups, windows, incidents, claims, &settings, attackGuardRequest{
		Operation: attackGuardApplyGlobal, IP: "invalid", Now: now,
	}); result.Err == nil {
		t.Fatal("global block with invalid IP was accepted")
	}
	if result := handleAttackGuardRequest(blocks, allow, trusted, lookups, windows, incidents, claims, &settings, attackGuardRequest{
		Operation: attackGuardSetSettings, Settings: SecuritySettings{AutoBlock:false, GlobalSync:false}, Now: now,
	}); result.Settings.AutoBlock || result.Settings.GlobalSync {
		t.Fatalf("settings were not updated: %#v", result.Settings)
	}
	if result := handleAttackGuardRequest(blocks, allow, trusted, lookups, windows, incidents, claims, &settings, attackGuardRequest{
		Operation: attackGuardOperation(255), Now: now,
	}); result.Err == nil {
		t.Fatal("unknown attack-guard operation was accepted")
	}
}

func TestHTTPSSecurityTextAndCrawlerEdgeBranches(t *testing.T) {
	if got := cleanSecurityText("<script>\x00attack\r\n", 6); strings.ContainsAny(got, "<>\x00\r\n") || len(got) > 6 {
		t.Fatalf("security text was not bounded: %q", got)
	}
	for _, language := range []string{"ru", "de", "en", "unknown"} {
		_ = localizeSecurityDescription("repository", "requested /.git/config", language)
		_ = localizeSecurityDescription("scanner-client", "scanner client", language)
		_ = localizeSecurityDescription("custom", "description", language)
	}
	for _, agent := range []string{
		"Googlebot/2.1",
		"bingbot/2.0",
		"GPTBot/1.0",
		"ClaudeBot/1.0",
		"PerplexityBot/1.0",
		"CCBot/2.0",
		"curl/8.0",
		"",
	} {
		_ = IsIndexingCrawlerAgent(agent)
	}
}
