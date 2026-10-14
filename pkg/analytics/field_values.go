package analytics

import "time"

// RecordedField and FormFieldValue remain only for decoding snapshots written by
// development builds of the analytics redesign. New observations never admit or
// retain form field contents; form analytics is structural only.
type RecordedField struct {
	Text string
	At   time.Time
}
type FormFieldValue struct {
	Field, Text string
	Visitors    int
}

// SafeFieldValue deliberately accepts only the empty metadata carried by normal
// signals. A field-value signal necessarily has a field name or value and is
// rejected at the validation boundary before it can enter analytics state.
func SafeFieldValue(name, text string) bool {
	return name == "" && text == ""
}

// Historical development snapshots may contain Values, but current reports do
// not expose, aggregate, archive, or render them. Form drop-off is derived only
// from structural field identifiers and form outcomes, never user-entered text.
func conversionFieldValues(_ *ConversionState, _, _ time.Time) map[string][]FormFieldValue {
	return nil
}
