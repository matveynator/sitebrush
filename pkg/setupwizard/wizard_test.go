package setupwizard

import "testing"

func TestValidateDefaultDBType(t *testing.T) {
	options := availableDBTypes()

	if err := validateDefaultDBType("sqlite", options); err != nil {
		t.Fatalf("sqlite should be accepted: %v", err)
	}
	if err := validateDefaultDBType("", options); err != nil {
		t.Fatalf("empty default should be accepted: %v", err)
	}
	if err := validateDefaultDBType("duckdb", options); err == nil {
		t.Fatalf("duckdb should be rejected when unavailable")
	}
}

