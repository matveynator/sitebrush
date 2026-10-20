package mailout

import (
	"context"
	"errors"
	"fmt"
	"net/textproto"
	"regexp"
	"strconv"
	"strings"
)

// SMTPDiagnostic contains only protocol metadata, never server-supplied prose,
// commands, addresses, credentials or message content.
type SMTPDiagnostic struct {
	Host         string
	Stage        string
	Code         string
	EnhancedCode string
	Outcome      string
	TLSMode      string
}
type smtpDiagnosticKey struct{}

// WithSMTPDiagnostics scopes observations to one delivery, including all MX and
// TLS fallback attempts. The observer is called synchronously by that delivery.
func WithSMTPDiagnostics(ctx context.Context, observe func(SMTPDiagnostic)) context.Context {
	return context.WithValue(ctx, smtpDiagnosticKey{}, observe)
}
func reportSMTP(ctx context.Context, diagnostic SMTPDiagnostic) {
	if observe, ok := ctx.Value(smtpDiagnosticKey{}).(func(SMTPDiagnostic)); ok {
		observe(diagnostic)
	}
}

var enhancedSMTPCode = regexp.MustCompile(`^[245]\.[0-9]{1,3}\.[0-9]{1,3}$`)

func smtpReplyCodes(code int, reply string) (string, string) {
	enhanced := ""
	if fields := strings.Fields(reply); len(fields) > 0 && enhancedSMTPCode.MatchString(fields[0]) {
		enhanced = fields[0]
	}
	return strconv.Itoa(code), enhanced
}

// Preserve typed errors for retry classification while keeping reflected SMTP
// replies out of every caller's logs, including callers outside the journal.
type smtpDeliveryError struct {
	diagnostic SMTPDiagnostic
	cause      error
}

func (err smtpDeliveryError) Error() string {
	d := err.diagnostic
	return fmt.Sprintf("SMTP host=%s stage=%s outcome=%s code=%s enhanced=%s", d.Host, d.Stage, d.Outcome, d.Code, d.EnhancedCode)
}
func (err smtpDeliveryError) Unwrap() error { return err.cause }
func smtpFailure(d SMTPDiagnostic, err error) SMTPDiagnostic {
	d.Outcome = "connection_error"
	var reply *textproto.Error
	if errors.As(err, &reply) {
		d.Code, d.EnhancedCode = smtpReplyCodes(reply.Code, reply.Msg)
		switch {
		case reply.Code >= 500:
			d.Outcome = "permanent_rejection"
		case reply.Code >= 400:
			d.Outcome = "temporary_rejection"
		default:
			d.Outcome = "unexpected_reply"
		}
	} else if d.Stage == "starttls" {
		d.Outcome = "tls_negotiation_failed"
	}
	return d
}
