package crawler

import (
	"strings"
	"testing"
)

func TestReadHTMLBodyWithLimitRejectsOversizedBody(t *testing.T) {
	if _, err := readHTMLBodyWithLimit(strings.NewReader("abcdef"), 5); err == nil {
		t.Fatal("oversized html body was allowed")
	}
	body, err := readHTMLBodyWithLimit(strings.NewReader("abcde"), 5)
	if err != nil {
		t.Fatalf("html body at limit was rejected: %v", err)
	}
	if string(body) != "abcde" {
		t.Fatalf("html body = %q", string(body))
	}
}
