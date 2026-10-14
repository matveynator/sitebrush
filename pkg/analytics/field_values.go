package analytics

import (
	"sort"
	"strings"
	"time"
)

// Only explicitly marked fields reach this boundary. Values have a shorter
// lifetime than funnel outcomes and never enter long-lived daily archives.
type RecordedField struct {
	Text string
	At   time.Time
}
type FormFieldValue struct {
	Field, Text string
	Visitors    int
}

func SafeFieldValue(name, text string) bool {
	if len(name) > 128 || len(text) > 256 || strings.ContainsAny(text, "\x00\r\n") {
		return false
	}
	lower := strings.ToLower(name)
	for _, sensitive := range []string{"password", "passwd", "secret", "token", "authorization", "credit", "card", "cvc", "cvv", "cc-", "otp", "passcode", "парол"} {
		if strings.Contains(lower, sensitive) {
			return false
		}
	}
	digits := strings.NewReplacer(" ", "", "-", "").Replace(text)
	if len(digits) >= 13 && len(digits) <= 19 {
		checksum := 0
		numeric := true
		for index := len(digits) - 1; index >= 0; index-- {
			if digits[index] < '0' || digits[index] > '9' {
				numeric = false
				break
			}
			number := int(digits[index] - '0')
			if (len(digits)-1-index)%2 == 1 {
				number *= 2
				if number > 9 {
					number -= 9
				}
			}
			checksum += number
		}
		if numeric && checksum%10 == 0 {
			return false
		}
	}
	if strings.HasPrefix(strings.ToLower(text), "bearer ") || strings.HasPrefix(text, "eyJ") {
		return false
	}
	return true
}

func conversionFieldValues(state *ConversionState, start, now time.Time) map[string][]FormFieldValue {
	counts := map[string]map[string]*FormFieldValue{}
	for _, visitor := range state.Visitors {
		counted := map[string]bool{}
		for _, form := range visitor.Forms {
			if form.Date < dayKey(start) {
				continue
			}
			formKey := form.Page + "\x1f" + form.Form
			for field, record := range form.Values {
				if record.At.After(now) || now.Sub(record.At) > 7*24*time.Hour {
					continue
				}
				textKey := field + "\x1f" + record.Text
				if counted[formKey+"\x1f"+textKey] {
					continue
				}
				counted[formKey+"\x1f"+textKey] = true
				if counts[formKey] == nil {
					counts[formKey] = map[string]*FormFieldValue{}
				}
				if counts[formKey][textKey] == nil {
					counts[formKey][textKey] = &FormFieldValue{Field: field, Text: record.Text}
				}
				counts[formKey][textKey].Visitors++
			}
		}
	}
	result := map[string][]FormFieldValue{}
	for formKey, fields := range counts {
		for _, field := range fields {
			result[formKey] = append(result[formKey], *field)
		}
		sort.Slice(result[formKey], func(left, right int) bool {
			return result[formKey][left].Field+result[formKey][left].Text < result[formKey][right].Field+result[formKey][right].Text
		})
	}
	return result
}
