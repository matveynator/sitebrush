package httpsecurity

import (
	"path/filepath"
	"testing"
	"time"
)

func TestThrottleGuardIgnoresShortBurst(t *testing.T) {
	guard, err := NewThrottleGuard("")
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()

	started := time.Date(2026, 9, 23, 4, 18, 20, 0, time.UTC)
	for requestIndex := 0; requestIndex < 185; requestIndex++ {
		now := started.Add(time.Duration(requestIndex) * 7 * time.Second / 184)
		decision := guard.ObserveFast("45.138.12.52", false, now)
		if decision.Active || decision.RateLimited {
			t.Fatalf("short burst was throttled at request %d: %#v", requestIndex, decision)
		}
	}
}

func TestThrottleGuardActivatesAfterSustainedMinute(t *testing.T) {
	guard, err := NewThrottleGuard("")
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()

	started := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	var decision ThrottleDecision
	for requestIndex := 0; requestIndex < 610; requestIndex++ {
		now := started.Add(time.Duration(requestIndex) * 100 * time.Millisecond)
		decision = guard.ObserveFast("203.0.113.25", false, now)
	}
	if !decision.Active {
		t.Fatalf("sustained high rate did not activate throttle: %#v", decision)
	}
	if decision.Throttle.Episodes != 1 {
		t.Fatalf("episodes=%d", decision.Throttle.Episodes)
	}
	duration := decision.Throttle.ExpiresAt.Sub(decision.Throttle.LastEvent)
	if duration < 59*time.Minute || duration > 61*time.Minute {
		t.Fatalf("unexpected first throttle duration: %s", duration)
	}
}

func TestThrottleGuardLimitsActiveTrafficWithoutBlockingIP(t *testing.T) {
	guard, err := NewThrottleGuard("")
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()

	started := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	for requestIndex := 0; requestIndex < 610; requestIndex++ {
		guard.ObserveFast("203.0.113.26", false, started.Add(time.Duration(requestIndex)*100*time.Millisecond))
	}
	activeAt := started.Add(61 * time.Second)
	var limited int
	for requestIndex := 0; requestIndex < throttleAllowedRPS+5; requestIndex++ {
		decision := guard.ObserveFast("203.0.113.26", false, activeAt)
		if !decision.Active {
			t.Fatal("active throttle disappeared")
		}
		if decision.RateLimited {
			limited++
		} else if decision.Delay <= 0 {
			t.Fatal("allowed throttled request had no soft delay")
		}
	}
	if limited == 0 {
		t.Fatal("active throttle did not shed excess requests")
	}
}

func TestThrottleGuardEscalatesRepeatedEpisodes(t *testing.T) {
	guard, err := NewThrottleGuard("")
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()

	ip := "198.51.100.70"
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	expected := []time.Duration{
		time.Hour,
		3 * time.Hour,
		6 * time.Hour,
		12 * time.Hour,
		24 * time.Hour,
		48 * time.Hour,
		96 * time.Hour,
		7 * 24 * time.Hour,
		7 * 24 * time.Hour,
	}
	for episodeIndex, expectedDuration := range expected {
		var decision ThrottleDecision
		for requestIndex := 0; requestIndex < 610; requestIndex++ {
			decision = guard.ObserveFast(ip, false, now.Add(time.Duration(requestIndex)*100*time.Millisecond))
		}
		if !decision.Active {
			t.Fatalf("episode %d did not activate", episodeIndex+1)
		}
		duration := decision.Throttle.ExpiresAt.Sub(decision.Throttle.LastEvent)
		if duration != expectedDuration {
			t.Fatalf("episode %d duration=%s want=%s", episodeIndex+1, duration, expectedDuration)
		}
		now = decision.Throttle.ExpiresAt.Add(time.Second)
	}
}

func TestThrottleGuardPersistsEpisodeHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "throttle.json")
	guard, err := NewThrottleGuard(path)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now().UTC().Truncate(time.Second)
	var decision ThrottleDecision
	for requestIndex := 0; requestIndex < 610; requestIndex++ {
		decision = guard.ObserveFast("192.0.2.55", false, started.Add(time.Duration(requestIndex)*100*time.Millisecond))
	}
	if !decision.Active {
		t.Fatal("throttle did not activate")
	}
	if err := guard.saveSnapshot(); err != nil {
		t.Fatal(err)
	}
	guard.Close()

	loaded, err := NewThrottleGuard(path)
	if err != nil {
		t.Fatal(err)
	}
	defer loaded.Close()
	snapshot, err := loaded.Snapshot(started.Add(61 * time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot) != 1 || snapshot[0].Episodes != 1 {
		t.Fatalf("unexpected persisted throttle: %#v", snapshot)
	}
}

func TestThrottleGuardDoesNotThrottleTrustedPeer(t *testing.T) {
	guard, err := NewThrottleGuard("")
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()

	started := time.Now().UTC()
	for requestIndex := 0; requestIndex < 2000; requestIndex++ {
		decision := guard.ObserveFast("203.0.113.90", true, started.Add(time.Duration(requestIndex)*100*time.Millisecond))
		if decision.Active || decision.RateLimited {
			t.Fatalf("trusted peer was throttled: %#v", decision)
		}
	}
}
