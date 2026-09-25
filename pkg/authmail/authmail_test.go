package authmail

import (
	"strings"
	"testing"
)

func TestAccountMailEscapesContentAndKeepsCodeReadable(t *testing.T) {
	plain, html, err := Render(Content{Language: "ru", Direction: "ltr", Domain: "example.org", Title: "Email change", Reason: "Approve the change", Email: "new@example.org", Previous: "old@example.org", Code: "012345", Link: "https://example.org/?email_confirm=abc", IP: "192.0.2.1", Time: "2026-09-23T10:00:00Z", Ignore: "<script>unsafe</script>"})
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"012345", "old@example.org", "new@example.org", "192.0.2.1", "2026-09-23T10:00:00Z"} {
		if !strings.Contains(plain, expected) || !strings.Contains(html, expected) {
			t.Fatalf("missing %s", expected)
		}
	}
	if !strings.Contains(html, ">012345</strong>") || strings.Contains(html, "<script>") {
		t.Fatal("code emphasis or escaping failed")
	}
}


// BEGIN authentication mail rendering security tests.

func TestAccountMailRejectsExecutableLinkAndEscapesMetadata(t *testing.T) {
	_, html, err := Render(Content{
		Language:  "en",
		Direction: "ltr",
		Domain:    "<img src=x onerror=alert(1)>",
		Title:     "<script>alert(1)</script>",
		Reason:    "Security notice",
		Email:     "owner@example.org",
		Link:      "javascript:alert(1)",
		Button:    "Open",
		IPLabel:   "IP",
		IP:        "<svg onload=alert(1)>",
		TimeLabel: "Time",
		Time:      "now",
		Expiry:    "Expires soon",
		Ignore:    "Ignore",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"<script>", "<img src=x", "<svg onload", "href=\"javascript:"} {
		if strings.Contains(html, forbidden) {
			t.Fatalf("unsafe mail HTML contains %q: %s", forbidden, html)
		}
	}
	if !strings.Contains(html, "#ZgotmplZ") {
		t.Fatalf("unsafe link scheme was not rejected by the template: %s", html)
	}
}

// END authentication mail rendering security tests.
