package httpsecurity

import (
	"testing"
	"time"
)

func TestConfirmedRegistrationMailAbuseBlocksSiteForThreeDays(t *testing.T) {
	guard, err := NewAttackGuard("")
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()
	now := time.Now().UTC()
	block, blocked := guard.ObserveSiteIncident("example.com", "192.0.2.10", "registration-mail-abuse", "Four failed registrations to different recipients", now)
	if !blocked || block.Reason != "registration-mail-abuse" || !block.ExpiresAt.Equal(now.Add(72*time.Hour)) {
		t.Fatal("confirmed spam did not block for three days", block, blocked)
	}
	if _, blocked := guard.CheckSite("other.example", "192.0.2.10", now); blocked {
		t.Fatal("site block crossed domain boundary")
	}
}
