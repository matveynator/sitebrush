package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInventoryIncludesOtherPlatformsAndCapturedWrites(t *testing.T) {
	root := t.TempDir()
	source := `package fixture
var cache map[string][]byte
type Request struct { Reply chan []byte; Nested map[string][]*int }
func work() {
    result := "before"
    go func() { result = "after" }()
    _ = result
}
`
	if err := os.WriteFile(filepath.Join(root, "fixture_openbsd.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	var report bytes.Buffer
	if err := inventory(root, &report); err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{"fixture_openbsd.go", "\tglobal\t", "\treference-field\t", "\tgoroutine\t", "\tcaptured-write\tresult"} {
		if !strings.Contains(report.String(), fragment) {
			t.Fatalf("inventory omitted %q: %s", fragment, report.String())
		}
	}
}

func TestInventoryDoesNotTreatClosureLocalsAsCapturedWrites(t *testing.T) {
	root := t.TempDir()
	source := `package fixture
func work() { go func() { result := 1; result++; _ = result }() }
`
	if err := os.WriteFile(filepath.Join(root, "fixture.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	var report bytes.Buffer
	if err := inventory(root, &report); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(report.String(), "\tcaptured-write\t") {
		t.Fatalf("local mutation was classified as captured: %s", report.String())
	}
}
