package analytics

import (
	"testing"
	"time"
)

func TestRegistrationMailAbuseNeedsConfirmedServerEvidence(t *testing.T) {
	now := time.Now().UTC()
	state := &SecurityState{}
	ordinary := RequestObservation{Time: now, IP: "192.0.2.10", Path: "/", Query: "register&category=registration-mail-abuse", Method: "POST", Status: 429}
	if category := state.Record(ordinary); category != "" {
		t.Fatal("URL or cooldown alone became registration spam", category)
	}
	confirmed := ordinary
	confirmed.ConfirmedCategory = "registration-mail-abuse"
	confirmed.Blocked = true
	if category := state.Record(confirmed); category != "registration-mail-abuse" {
		t.Fatal("confirmed registration spam missing", category)
	}
	report := state.Report(now, 1)
	if len(report.Incidents) != 1 || report.Incidents[0].IP != ordinary.IP || report.Incidents[0].Categories["registration-mail-abuse"] != 1 {
		t.Fatal("spam IP missing from security incidents", report.Incidents)
	}
}
