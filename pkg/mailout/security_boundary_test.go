package mailout

import (
	"context"
	"net/mail"
	"strings"
	"testing"
)

func TestSecurityBoundaryRejectsAddressHeaderInjection(t *testing.T) {
	for _, raw := range []string{
		"sender@example.com\r\nBcc: attacker@example.net",
		"Sender <sender@example.com>\nCc: attacker@example.net",
		"recipient@example.com\r\nSubject: injected",
	} {
		if _, err := parseSingleAddress(raw); err == nil {
			t.Fatalf("SECURITY: mail address with header injection was accepted: %q", raw)
		}
	}
}

func TestSecurityBoundarySubjectInjectionStaysInsideSubjectHeader(t *testing.T) {
	from, _ := mail.ParseAddress("sender@example.com")
	to, _ := mail.ParseAddress("recipient@example.net")
	payload := string(buildMessagePayloadWithID(from, to, "hello\r\nBcc: attacker@example.net", "body", "", "stable-id"))
	header, _, _ := strings.Cut(payload, "\r\n\r\n")
	if strings.Contains(header, "\r\nBcc: attacker@example.net") {
		t.Fatalf("SECURITY: subject injected a Bcc header: %s", header)
	}
	if strings.Count(header, "\r\nSubject:") != 1 {
		t.Fatalf("unexpected Subject header count: %s", header)
	}
}

func TestSecurityBoundaryStableMessageIDRejectsHeaderMetacharacters(t *testing.T) {
	from, _ := mail.ParseAddress("sender@example.com")
	for _, injected := range []string{
		"stable\r\nBcc:attacker",
		"<owned@example.net>",
		"stable@evil.example",
		"stable id",
	} {
		got := messageIDForDelivery(from, injected)
		if strings.Contains(got, injected) || strings.Contains(got, "\r") || strings.Contains(got, "\n") {
			t.Fatalf("SECURITY: unsafe stable Message-ID survived validation: %q -> %q", injected, got)
		}
		if !strings.HasPrefix(got, "<") || !strings.HasSuffix(got, "@example.com>") {
			t.Fatalf("fallback Message-ID malformed: %q", got)
		}
	}
}

func TestSecurityBoundaryDeliveryWorkerStopsOnCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := make(chan struct{}, 1)
	jobs := StartDeliveryWorker(ctx, func(context.Context, Message) error {
		called <- struct{}{}
		return nil
	})
	select {
	case jobs <- DeliveryJob{Message: Message{To: "victim@example.net"}}:
	default:
	}
	select {
	case <-called:
		t.Fatal("SECURITY: cancelled mail delivery worker processed a queued message")
	default:
	}
}
