package database

import (
	"errors"
	"testing"
)

func TestUniqueConstraintErrorRecognizesDriverMessages(t *testing.T) {
	for _, message := range []string{"unique constraint failed", "duplicate key value", "CONSTRAINT FAILED", "unique violation"} {
		if !isUniqueConstraintError(errors.New(message)) {
			t.Errorf("duplicate error %q not recognized", message)
		}
	}
	if isUniqueConstraintError(nil) || isUniqueConstraintError(errors.New("network unavailable")) {
		t.Fatal("non-duplicate error was recognized")
	}
}
