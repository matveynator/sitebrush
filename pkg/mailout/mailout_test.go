package mailout

import (
	"net/mail"
	"strings"
	"testing"
)

func TestBuildMessagePayloadIncludesMessageID(t *testing.T) {
	fromAddress, err := mail.ParseAddress("SiteBrush <noreply@example.com>")
	if err != nil {
		t.Fatal(err)
	}
	toAddress, err := mail.ParseAddress("User <user@example.net>")
	if err != nil {
		t.Fatal(err)
	}

	payload := string(buildMessagePayload(fromAddress, toAddress, "Привет", "Body"))
	if !strings.Contains(payload, "\r\nMessage-ID: <") {
		t.Fatalf("payload missing Message-ID header: %s", payload)
	}
	if !strings.Contains(payload, "@example.com>\r\n") {
		t.Fatalf("payload Message-ID does not use sender domain: %s", payload)
	}
	if !strings.Contains(payload, "\r\nSubject: =?utf-8?") {
		t.Fatalf("payload missing encoded subject: %s", payload)
	}
}
