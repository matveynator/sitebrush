package diagnosticlog

import (
	"strings"
	"testing"
)

func TestSafeLogValueRemovesLogEntryDelimiters(t *testing.T) {
	safeValue := SafeLogValue("first\r\nforged\x00entry")
	if strings.ContainsAny(safeValue, "\r\n\x00") {
		t.Fatalf("safe log value contains a control character: %q", safeValue)
	}
	if safeValue != "firstforgedentry" {
		t.Fatalf("safe log value = %q, want %q", safeValue, "firstforgedentry")
	}
}
