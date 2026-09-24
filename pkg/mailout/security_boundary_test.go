package mailout

import (
	"context"
	"errors"
	"net"
	"net/mail"
	"strings"
	"testing"
	"time"
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


func TestDirectSenderSendValidationLookupAndFailoverBranches(t *testing.T) {
	ctx := context.Background()
	sender := DirectSender{}

	if err := sender.Send(ctx, Message{From: "bad\r\nBcc:x", To: "to@example.com"}); err == nil {
		t.Fatal("SECURITY: invalid sender address was accepted")
	}
	if err := sender.Send(ctx, Message{From: "from@example.com", To: "bad\r\nBcc:x"}); err == nil {
		t.Fatal("SECURITY: invalid recipient address was accepted")
	}
	if err := sender.Send(ctx, Message{From: "from@example.com", To: "localuser"}); err == nil {
		t.Fatal("recipient without domain was accepted")
	}

	sentinel := errors.New("resolver failed")
	sender.lookupHosts = func(context.Context, string) ([]string, error) {
		return nil, sentinel
	}
	if err := sender.Send(ctx, Message{From: "from@example.com", To: "to@example.net"}); !errors.Is(err, sentinel) {
		t.Fatalf("lookup error = %v, want sentinel", err)
	}

	sender.lookupHosts = func(context.Context, string) ([]string, error) {
		return nil, nil
	}
	if err := sender.Send(ctx, Message{From: "from@example.com", To: "to@example.net"}); err == nil || !strings.Contains(err.Error(), "no mail hosts") {
		t.Fatalf("empty MX list error = %v", err)
	}

	var endpoints []string
	sender.lookupHosts = func(context.Context, string) ([]string, error) {
		return []string{"mx1.example.net", "mx2.example.net"}, nil
	}
	sender.dialContext = func(_ context.Context, _, endpoint string) (net.Conn, error) {
		endpoints = append(endpoints, endpoint)
		return nil, errors.New("dial failed")
	}
	err := sender.Send(ctx, Message{From: "from@example.com", To: "to@example.net", Subject: "subject", Body: "body"})
	if err == nil || len(endpoints) != 2 {
		t.Fatalf("MX failover attempts = %#v err=%v", endpoints, err)
	}
}

func TestMailDeliveryErrorAndClassificationBranches(t *testing.T) {
	cause := errors.New("tls failure")
	wrapped := startTLSError{cause: cause}
	if !errors.Is(wrapped, cause) || wrapped.Unwrap() != cause {
		t.Fatal("STARTTLS error did not unwrap to cause")
	}

	permanent := PermanentError{Err: cause}
	if !errors.Is(permanent, cause) || permanent.Unwrap() != cause || permanent.Error() != cause.Error() {
		t.Fatal("permanent delivery error did not preserve cause")
	}
	emptyPermanent := PermanentError{}
	if emptyPermanent.Error() == "" || emptyPermanent.Unwrap() != nil {
		t.Fatal("empty permanent error behavior invalid")
	}

	for _, tc := range []struct {
		err  error
		want bool
	}{
		{nil, false},
		{PermanentError{Err: errors.New("blocked")}, true},
		{errors.New("SMTP 550 mailbox unavailable"), true},
		{errors.New("temporary 450 mailbox busy"), false},
		{errors.New("text 5x0 malformed"), false},
	} {
		if got := IsPermanentFailure(tc.err); got != tc.want {
			t.Fatalf("IsPermanentFailure(%v) = %v, want %v", tc.err, got, tc.want)
		}
	}
}

func TestMailRetryDelayBoundaries(t *testing.T) {
	expected := []time.Duration{
		5 * time.Second,
		5 * time.Second,
		30 * time.Second,
		2 * time.Minute,
		10 * time.Minute,
		30 * time.Minute,
		time.Hour,
	}
	for attempts, want := range expected {
		if got := RetryDelay(attempts); got != want {
			t.Fatalf("RetryDelay(%d) = %v, want %v", attempts, got, want)
		}
		jittered := RetryDelayWithJitter(attempts)
		if jittered < want*80/100 || jittered > want*120/100 {
			t.Fatalf("RetryDelayWithJitter(%d) = %v outside expected band around %v", attempts, jittered, want)
		}
	}
}
