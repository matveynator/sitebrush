package diagnosticlog

import (
	"strings"
	"unicode"
)

const maxSQLSummaryLength = 180

// SQLSummary keeps database diagnostics readable without logging complete
// statements that can contain large HTML payloads or tokens.
func SQLSummary(query string) string {
	fields := strings.FieldsFunc(strings.TrimSpace(query), func(nextRune rune) bool {
		return unicode.IsSpace(nextRune)
	})
	summary := strings.Join(fields, " ")
	if len(summary) <= maxSQLSummaryLength {
		return summary
	}
	return summary[:maxSQLSummaryLength] + "..."
}

func SafeLogValue(rawValue string) string {
	cleanValue := strings.TrimSpace(rawValue)
	if cleanValue == "" {
		return "-"
	}
	cleanValue = strings.Map(func(nextRune rune) rune {
		if unicode.IsControl(nextRune) {
			return -1
		}
		return nextRune
	}, cleanValue)
	if cleanValue == "" {
		return "-"
	}
	return cleanValue
}
