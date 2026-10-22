package mailmonitor

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDiagnosticBoundsAndSMTPOutcome(t *testing.T) {
	for _, diagnostic := range []Diagnostic{
		{Transport: "netchan(quic:9876)", ClientIP: "192.0.2.1", SMTPServer: "mx.example:25", SMTPCode: "250", SMTPReply: "OK", Failure: "stale failure", Identifiers: "trace"},
		{Transport: strings.Repeat("q", 100), ClientIP: strings.Repeat("a", 80), SMTPServer: strings.Repeat("x", 300), SMTPCode: "550long", SMTPReply: strings.Repeat("y", 100), Identifiers: strings.Repeat("z", 1000)},
		{SMTPServer: strings.Repeat("世", 100), Failure: strings.Repeat("error", 30), Identifiers: strings.Repeat("trace", 200)},
	} {
		encoded := diagnostic.String()
		if len(encoded) > 500 {
			t.Fatalf("oversized diagnostic: %d", len(encoded))
		}
		var decoded Diagnostic
		if err := json.Unmarshal([]byte(encoded), &decoded); err != nil {
			t.Fatal(err)
		}
		if decoded.SMTPReply != "" && decoded.Failure != "" {
			t.Fatal("accepted reply retained stale failure", decoded)
		}
		if decoded != (Event{Detail: encoded}).Diagnostic() {
			t.Fatal("structured diagnostic did not round trip")
		}
	}
}

func TestHistoricalDiagnosticAndDisplayedDeliveryBoundary(t *testing.T) {
	for _, scenario := range []struct {
		event                     Event
		transport, failure, stage string
	}{
		{Event{Stage: "queued", Detail: "transport=netchan(quic:9876) source=192.0.2.1:5000"}, "netchan(quic:9876)", "", "queued"},
		{Event{Stage: "transport_sent", Detail: "transport=https"}, "https", "", "sent"},
		{Event{Stage: "accepted", AcceptedBy: "central_relay"}, "", "", "sent"},
		{Event{Stage: "accepted", AcceptedBy: "recipient_smtp"}, "", "", "accepted"},
		{Event{Stage: "failed", SMTPCode: "550", Detail: "untrusted peer prose"}, "", "Delivery failed", "rejected"},
		{Event{Stage: "retry", SMTPCode: "451", Detail: "untrusted peer prose"}, "", "Delivery failed", "temporary_failure"},
		{Event{Stage: "recovery_mail_failed", Detail: "connection unavailable"}, "", "Delivery failed", "recovery_mail_failed"},
		{Event{Stage: "queued", Detail: "transport=smtp"}, "", "", "queued"},
	} {
		diagnostic := scenario.event.Diagnostic()
		if diagnostic.Transport != scenario.transport || diagnostic.Failure != scenario.failure || scenario.event.DisplayStage() != scenario.stage {
			t.Fatalf("wrong diagnostic: %+v %+v", scenario, diagnostic)
		}
		if scenario.event.Detail == "transport=smtp" && diagnostic.Identifiers != scenario.event.Detail {
			t.Fatal("legacy identifiers lost")
		}
	}
}
