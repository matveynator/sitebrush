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
