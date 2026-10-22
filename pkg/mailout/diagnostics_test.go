package mailout

import (
	"errors"
	"net/textproto"
	"strings"
	"testing"
)

func TestSMTPFailureKeepsProtocolCodeWithoutReflectedSecrets(t *testing.T) {
	for _, scenario := range []struct {
		code  int
		reply string
		want  string
	}{
		{550, "User unknown", "User unknown"},
		{451, "Try again later", "Try again later"},
		{550, "5.7.26 secret-password AUTH PLAIN reset?token=abcdef", "5.7.26 · Sender unauthenticated"},
		{550, "secret-password AUTH PLAIN 123456 cookie=abc token=short", "Message rejected"},
	} {
		cause := &textproto.Error{Code: scenario.code, Msg: scenario.reply}
		report := smtpFailure(SMTPReport{Server: "mx.example:25"}, cause)
		wrapped := smtpDeliveryError{report: report, cause: cause}
		if report.Reply != scenario.want || !errors.Is(wrapped, cause) {
			t.Fatalf("unsafe diagnostic or lost error identity: %+v", report)
		}
		if strings.Contains(wrapped.Error(), "secret-password") || strings.Contains(wrapped.Error(), "123456") {
			t.Fatal("SMTP peer reflected a secret into the delivery error")
		}
	}
}

func TestSMTPReplySummariesAndUnavailableReportConsumer(t *testing.T) {
	for _, scenario := range []struct {
		code        int
		reply, want string
	}{
		{250, "OK", "OK"}, {550, "5.1.1 reflected secret", "5.1.1 · User unknown"},
		{452, "4.2.2 reflected secret", "4.2.2 · Mailbox full"}, {552, "5.2.2 reflected secret", "5.2.2 · Mailbox full"},
		{550, "5.7.1 reflected secret", "5.7.1 · Delivery not authorized"},
		{451, "temporary failure", "Try again later"}, {550, "relay denied", "Relay denied"},
		{552, "quota exceeded", "Mailbox full"}, {530, "authentication required", "Authentication failed"},
		{250, "unknown peer text", "Message accepted"}, {421, "unknown peer text", "Temporary SMTP failure"},
		{550, "unknown peer text", "Message rejected"}, {399, "unknown peer text", "SMTP response"},
	} {
		if reply := smtpReply(scenario.code, scenario.reply); reply != scenario.want {
			t.Fatal(scenario, reply)
		}
	}
	report := SMTPReport{Server: "mx.example.com:25", MX: "mx.example.com", Source: "192.0.2.1:5000", Destination: "192.0.2.2:25", Protocol: "starttls", Code: "250", Reply: "OK"}
	replies := make(chan SMTPReport, 1)
	report.publish(nil)
	report.publish(replies)
	report.publish(replies)
	if received := <-replies; received != report {
		t.Fatal("SMTP report changed", received)
	}
	close(replies)
	report.publish(replies)
	if !strings.Contains(report.TransportDetails(), "destination=192.0.2.2:25") || !strings.Contains(report.TransportDetails(), "protocol=starttls") {
		t.Fatal("observed SMTP endpoint lost", report.TransportDetails())
	}
	if (SMTPReport{}).TransportDetails() != "" {
		t.Fatal("unobserved endpoint inferred")
	}
	if report := SMTPFailure(errors.New("untrusted peer prose")); report.Code != "" || report.Reply != "" {
		t.Fatal("SMTP reply invented", report)
	}
	if firstDiagnostic("", "connection failed") != "connection failed" {
		t.Fatal("transport failure hidden")
	}
}
