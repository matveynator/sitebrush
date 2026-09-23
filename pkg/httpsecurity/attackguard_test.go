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
	if err != nil { t.Fatal(err) }
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
	if err != nil { t.Fatal(err) }
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
	if err != nil { t.Fatal(err) }
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
	if err != nil { t.Fatal(err) }
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
	if err != nil { t.Fatal(err) }
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
	if err := guard.Remove("198.51.100.4"); err != nil { t.Fatal(err) }
	if _, ok := guard.Check("198.51.100.4", now); ok {
		t.Fatal("manual unblock failed")
	}
}

func TestBlockedHTMLIsSmallLocalizedAndEscaped(t *testing.T) {
	body := BlockedHTML("ru-RU", SecurityBlock{Reason: "<script>", IncidentID: "abc"})
	if len(body) > 2048 {
		t.Fatalf("blocked response too large: %d", len(body))
	}
	if !strings.Contains(body, "Запрос заблокирован") || strings.Contains(body, "<script>") {
		t.Fatalf("unexpected blocked HTML: %s", body)
	}
}
