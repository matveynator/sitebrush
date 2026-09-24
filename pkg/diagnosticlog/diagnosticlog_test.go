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

func TestSQLSummaryCompactsAndBoundsQueries(t *testing.T) {
	if got := SQLSummary(" SELECT\n *\tFROM   sites "); got != "SELECT * FROM sites" {
		t.Fatalf("SQLSummary compact result=%q", got)
	}
	if got := SQLSummary(strings.Repeat("x", maxSQLSummaryLength+5)); got != strings.Repeat("x", maxSQLSummaryLength)+"..." {
		t.Fatalf("SQLSummary truncated result length=%d", len(got))
	}
	if got := SafeLogValue("\x00\x01\n\r"); got != "-" {
		t.Fatalf("control-only log value=%q", got)
	}
}
