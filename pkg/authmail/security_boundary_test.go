package authmail

import (
	"strings"
	"testing"
)

func TestSecurityBoundaryMailTemplateEscapesInjectedHTMLAndUnsafeLink(t *testing.T) {
	plain, htmlBody, err := Render(Content{
		Language:  "en",
		Direction: "ltr",
		Domain:    "<img src=x onerror=alert(1)>",
		Title:     "<script>alert(1)</script>",
		Reason:    "<b>owned</b>",
		Email:     "owner@example.com",
		Link:      "javascript:alert(1)",
		Button:    "Open",
		Ignore:    "<svg/onload=alert(1)>",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(htmlBody, "<script>") || strings.Contains(htmlBody, "<img ") || strings.Contains(htmlBody, "<svg") {
		t.Fatalf("SECURITY: account mail rendered injected HTML: %s", htmlBody)
	}
	if strings.Contains(htmlBody, "href=\"javascript:") {
		t.Fatalf("SECURITY: unsafe javascript link survived html/template URL filtering: %s", htmlBody)
	}
	if !strings.Contains(plain, "javascript:alert(1)") {
		t.Fatal("plain-text mail unexpectedly lost the literal link text")
	}
}

func TestSecurityBoundaryMailCodeCannotBreakMarkup(t *testing.T) {
	_, htmlBody, err := Render(Content{
		Language:  "en",
		Direction: "ltr",
		Domain:    "example.org",
		Title:     "Security code",
		Reason:    "Use this code",
		Email:     "owner@example.org",
		CodeLabel: "Code",
		Code:      "123</strong><script>alert(1)</script>",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(htmlBody, "<script>") {
		t.Fatalf("SECURITY: code field injected executable markup: %s", htmlBody)
	}
}
