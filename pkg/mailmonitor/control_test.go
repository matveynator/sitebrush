package mailmonitor

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/matveynator/sitebrush/v2/pkg/mailout"
)

func TestMailCancellationCommandsKeepInstallationOwnershipAndTerminalResult(t *testing.T) {
	boundary := context.Background()
	store := Store{DB: journalTestDatabase(t)}
	for _, identity := range [][2]string{{"", "message"}, {"desktop", ""}, {strings.Repeat("x", 129), "message"}, {"desktop", strings.Repeat("x", 129)}} {
		if store.RequestCancel(boundary, identity[0], identity[1]) == nil {
			t.Fatal("invalid cancellation identity accepted", identity)
		}
	}
	for _, message := range []string{"cancelled", "too_late", "not_found"} {
		for range 2 {
			if err := store.RequestCancel(boundary, "desktop", message); err != nil {
				t.Fatal(err)
			}
		}
		if err := store.RequestCancel(boundary, "other", message); err != nil {
			t.Fatal(err)
		}
		if err := store.CompleteCommand(boundary, "desktop", message, "accepted"); err == nil {
			t.Fatal("invalid cancellation result accepted")
		}
		if err := store.CompleteCommand(boundary, "desktop", message, message); err != nil {
			t.Fatal(err)
		}
		if err := store.CompleteCommand(boundary, "desktop", message, "cancelled"); err != nil {
			t.Fatal(err)
		}
		status, err := store.CancelStatus(boundary, "desktop", message)
		if err != nil || status != message {
			t.Fatal("terminal cancellation was overwritten", status, err)
		}
	}
	commands, err := store.Commands(boundary, "desktop")
	if err != nil || len(commands) != 0 {
		t.Fatal("completed command remains pending", commands, err)
	}
	commands, err = store.Commands(boundary, "other")
	if err != nil || len(commands) != 3 {
		t.Fatal("another installation's commands changed", commands, err)
	}
	status, err := store.CancelStatus(boundary, "desktop", "absent")
	if err != nil || status != "" {
		t.Fatal(status, err)
	}
}

func TestMailBlockValidationAndPolicyReplay(t *testing.T) {
	boundary := context.Background()
	store := Store{DB: journalTestDatabase(t)}
	for _, block := range []IPBlock{
		{Scope: "global", IP: "invalid"}, {Scope: "global", IP: "10.0.0.1"}, {Scope: "unknown", IP: "192.0.2.1"},
		{Scope: "installation", IP: "192.0.2.1"}, {Scope: "installation", OriginInstallationID: strings.Repeat("x", 129), IP: "192.0.2.1"},
		{Scope: "installation", OriginInstallationID: "bad\norigin", IP: "192.0.2.1"},
	} {
		if store.SetBlock(boundary, block, true) == nil {
			t.Fatal("invalid mail block accepted", block)
		}
	}
	block := IPBlock{Scope: "installation", OriginInstallationID: "desktop", IP: "::ffff:192.0.2.1", Reason: "spam"}
	for range 2 {
		if err := store.SetBlock(boundary, block, true); err != nil {
			t.Fatal(err)
		}
	}
	blocks, err := store.Blocks(boundary)
	if err != nil || len(blocks) != 1 || blocks[0].IP != "192.0.2.1" {
		t.Fatal("block identity was not canonical", blocks, err)
	}
	if err := store.SetBlock(boundary, block, false); err != nil {
		t.Fatal(err)
	}
	if blocked, err := store.Blocked(boundary, "desktop", ""); err != nil || blocked {
		t.Fatal(blocked, err)
	}
	if _, err := store.Blocked(boundary, "desktop", "invalid"); err == nil {
		t.Fatal("invalid block lookup accepted")
	}
	revision, err := store.BumpRevision(boundary)
	if err != nil || revision != 1 {
		t.Fatal(revision, err)
	}
	revision, err = store.BumpRevision(boundary)
	if err != nil || revision != 2 {
		t.Fatal(revision, err)
	}
	policy := ControlResponse{Revision: 1, Blocks: []IPBlock{{Scope: "global", IP: "192.0.2.3"}, {Scope: "installation", OriginInstallationID: "desktop", IP: "::1"}}}
	if err := store.ApplyPolicy(boundary, policy); err != nil {
		t.Fatal(err)
	}
	if err := store.ApplyPolicy(boundary, ControlResponse{Revision: 1}); err != nil {
		t.Fatal(err)
	}
	if blocked, err := store.Blocked(boundary, "other", "192.0.2.3"); err != nil || !blocked {
		t.Fatal("equal revision replaced active policy", blocked, err)
	}
	if store.ApplyPolicy(boundary, ControlResponse{Revision: 2, Blocks: []IPBlock{{Scope: "global", IP: "127.0.0.1"}}}) == nil {
		t.Fatal("invalid central policy accepted")
	}
	if blocked, err := store.Blocked(boundary, "other", "192.0.2.3"); err != nil || !blocked {
		t.Fatal("invalid policy erased valid restrictions", blocked, err)
	}
}

func TestRelayEventCursorDoesNotCrossInstallation(t *testing.T) {
	boundary := context.Background()
	store := Store{DB: journalTestDatabase(t)}
	now := time.Now().UTC().Add(-time.Minute)
	for _, origin := range []string{"desktop", "other"} {
		for index, stage := range []string{"queued", "attempt", "accepted"} {
			event := Event{ID: fmt.Sprintf("%s-%d", origin, index), MessageID: origin + "-message", FlowID: origin + "-flow", Kind: "login_code", Stage: stage, Route: "local", CreatedAt: now.Add(time.Duration(index) * time.Second).Format(time.RFC3339Nano), Metadata: &mailout.Metadata{OriginInstallationID: origin}}
			if err := store.Append(boundary, event); err != nil {
				t.Fatal(err)
			}
		}
	}
	events, cursor, err := store.RelayEvents(boundary, "desktop", "")
	if err != nil || len(events) != 3 || cursor == "" {
		t.Fatal(events, cursor, err)
	}
	for _, event := range events {
		if event.Metadata == nil || event.Metadata.OriginInstallationID != "desktop" {
			t.Fatal("relay event leaked another installation", event)
		}
	}
	again, next, err := store.RelayEvents(boundary, "desktop", cursor)
	if err != nil || len(again) != 0 || next != cursor {
		t.Fatal("acknowledged relay events replayed", again, next, err)
	}
}

func TestAbuseImportsValidateEvidenceBeforeChangingCounts(t *testing.T) {
	boundary := context.Background()
	store := Store{DB: journalTestDatabase(t)}
	now := time.Now().UTC().Truncate(10 * time.Minute)
	valid := Abuse{OriginInstallationID: "desktop", IP: "192.0.2.4", Kind: "recovery", Bucket: now.Format(time.RFC3339Nano), FirstAt: now.Format(time.RFC3339Nano), LastAt: now.Add(time.Minute).Format(time.RFC3339Nano), Attempts: 2, Samples: []string{"token=secret"}}
	for _, mutate := range []func(*Abuse){
		func(report *Abuse) { report.OriginInstallationID = "foreign" }, func(report *Abuse) { report.Attempts = 0 },
		func(report *Abuse) { report.Samples = make([]string, 21) }, func(report *Abuse) { report.Kind = strings.Repeat("x", 61) },
		func(report *Abuse) { report.IP = "invalid" }, func(report *Abuse) { report.Bucket = "bad" },
		func(report *Abuse) { report.Bucket = now.Add(24 * time.Hour).Format(time.RFC3339) },
		func(report *Abuse) { report.Bucket = now.Add(-Retention - time.Hour).Format(time.RFC3339) },
		func(report *Abuse) { report.FirstAt = "bad" }, func(report *Abuse) { report.LastAt = "bad" },
		func(report *Abuse) { report.FirstAt = now.Add(-time.Second).Format(time.RFC3339) },
		func(report *Abuse) { report.LastAt = now.Add(11 * time.Minute).Format(time.RFC3339) },
	} {
		invalid := valid
		mutate(&invalid)
		if store.ImportAbuse(boundary, "desktop", []Abuse{invalid}) == nil {
			t.Fatal("invalid report accepted", invalid)
		}
	}
	if store.ImportAbuse(boundary, "desktop", make([]Abuse, 51)) == nil {
		t.Fatal("unbounded abuse batch accepted")
	}
	for _, count := range []int{2, 1, 3} {
		report := valid
		report.Attempts = count
		if err := store.ImportAbuse(boundary, "desktop", []Abuse{report}); err != nil {
			t.Fatal(err)
		}
	}
	reports, err := store.Abuse(boundary, now)
	if err != nil || len(reports) != 1 || reports[0].Attempts != 3 || strings.Contains(reports[0].Samples[0], "secret") {
		t.Fatal("report replay inflated counts or exposed secrets", reports, err)
	}
	if err := store.RejectRate(boundary, "source_ip", "", now); err != nil {
		t.Fatal(err)
	}
}
