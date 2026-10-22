package mailmonitor

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/matveynator/sitebrush/v2/pkg/mailout"
)

func TestJournalCancellationAndAccountOutcomesKeepFinalState(t *testing.T) {
	for _, scenario := range []struct {
		stages []string
		final  string
	}{
		{[]string{"queued", "attempt", "failed"}, "failed"},
		{[]string{"queued", "cancel_requested"}, "cancel_requested"},
		{[]string{"queued", "cancel_requested", "cancel_too_late"}, "cancel_too_late"},
		{[]string{"queued", "cancel_requested", "cancelled"}, "cancelled"},
		{[]string{"registration_requested", "queued", "accepted", "registration_complete"}, "registration_complete"},
		{[]string{"queued", "failed", "accepted"}, "accepted"},
	} {
		t.Run(scenario.final, func(t *testing.T) {
			store := Store{DB: journalTestDatabase(t)}
			boundary := context.Background()
			now := time.Now().UTC().Add(-time.Minute)
			for index, stage := range scenario.stages {
				event := Event{ID: fmt.Sprint(index), FlowID: "flow", MessageID: "message", Kind: "email_confirm", Stage: stage, Recipient: "literal!_%@example.com", CreatedAt: now.Add(time.Duration(index) * time.Second).Format(time.RFC3339Nano), Metadata: &mailout.Metadata{OriginInstallationID: "desktop", InitiatorIP: "192.0.2.6", InstallationIP: "192.0.2.7"}}
				if err := store.Append(boundary, event); err != nil {
					t.Fatal(err)
				}
			}
			operations, count, err := store.Journal(boundary, Filter{InstallationID: "local", Recipient: "!_%@EXAMPLE.COM", Limit: 100, Offset: -1})
			if err != nil || count != 1 || len(operations) != 1 || operations[0].Stage != scenario.final || operations[0].InstallationIP != "192.0.2.7" {
				t.Fatal("wrong terminal state or literal recipient filter", operations, count, err)
			}
			counts, err := store.JournalCounts(boundary, "local", now)
			if err != nil {
				t.Fatal(err)
			}
			if scenario.final == "accepted" && (counts.Accepted != 1 || counts.Failed != 0) {
				t.Fatal("late delivery retained failure count", counts)
			}
			if scenario.final == "cancelled" && counts.Cancelled != 1 {
				t.Fatal("cancelled message disappeared", counts)
			}
			if scenario.final == "registration_complete" && (counts.RegistrationRequested != 1 || counts.RegistrationComplete != 1) {
				t.Fatal("registration funnel disappeared", counts)
			}
		})
	}
}

func TestLegacyDiagnosticsSurviveProjectionAndOlderAttemptReplay(t *testing.T) {
	store := Store{DB: journalTestDatabase(t)}
	boundary := context.Background()
	if err := store.SetState(boundary, "local_origin", "desktop"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(-time.Minute)
	for _, event := range []Event{
		{ID: "structured", FlowID: "one", Kind: "recover", Stage: "queued", Detail: (Diagnostic{ClientIP: "::ffff:192.0.2.8", Transport: "netchan(quic:9876)"}).String(), CreatedAt: now.Format(time.RFC3339Nano)},
		{ID: "attempt-new", FlowID: "one", MessageID: "message", Kind: "recover", Stage: "attempt", Attempt: 2, CreatedAt: now.Add(3 * time.Second).Format(time.RFC3339Nano)},
		{ID: "attempt-old", FlowID: "one", MessageID: "message", Kind: "recover", Stage: "attempt", Attempt: 1, CreatedAt: now.Add(time.Second).Format(time.RFC3339Nano)},
	} {
		if err := store.Append(boundary, event); err != nil {
			t.Fatal(err)
		}
	}
	operations, count, err := store.Journal(boundary, Filter{InstallationID: "desktop", Kind: "login_code"})
	if err != nil || count != 1 || len(operations) != 1 || operations[0].InitiatorIP != "192.0.2.8" {
		t.Fatal(operations, count, err)
	}
	for _, step := range operations[0].Steps {
		if step.Stage == "attempt" && step.Attempt != 2 {
			t.Fatal("older attempt overwrote the latest timestamp", step)
		}
	}
	if !strings.Contains(operations[0].Steps[0].Transport, "netchan(quic:9876)") {
		t.Fatal("legacy transport disappeared", operations)
	}
}

func TestMailBatchRejectsInvalidMetadataWithoutPartialImport(t *testing.T) {
	boundary := context.Background()
	store := Store{DB: journalTestDatabase(t)}
	valid := Event{ID: "one", Kind: "recovery", Stage: "queued", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}
	for _, mutate := range []func(*Event){
		func(event *Event) { event.ID = "" }, func(event *Event) { event.ID = strings.Repeat("x", 129) },
		func(event *Event) { event.Stage = "bad\nstage" }, func(event *Event) { event.Domain = strings.Repeat("x", 254) },
		func(event *Event) { event.Recipient = strings.Repeat("x", 321) }, func(event *Event) { event.Detail = strings.Repeat("x", 501) },
		func(event *Event) { event.Transport = strings.Repeat("x", 501) }, func(event *Event) { event.Attempt = -1 },
		func(event *Event) { event.CreatedAt = time.Now().Add(time.Hour).Format(time.RFC3339Nano) },
		func(event *Event) { event.Metadata = &mailout.Metadata{InitiatorIP: "invalid"} },
		func(event *Event) { event.Metadata = &mailout.Metadata{OriginInstallationID: strings.Repeat("x", 129)} },
	} {
		invalid := valid
		mutate(&invalid)
		if err := store.Import(boundary, "desktop", []Event{valid, invalid}); err == nil {
			t.Fatal("invalid batch accepted", invalid)
		}
	}
	foreign := valid
	foreign.Metadata = &mailout.Metadata{OriginInstallationID: "other"}
	if store.Import(boundary, "desktop", []Event{foreign}) == nil {
		t.Fatal("foreign installation identity accepted")
	}
	if (Store{}).Append(boundary, valid) == nil {
		t.Fatal("absent SQL owner acknowledged an event")
	}
	events, err := store.Unsynced(boundary, 0)
	if err != nil || len(events) != 0 {
		t.Fatal("invalid batch committed a prefix", events, err)
	}
}
