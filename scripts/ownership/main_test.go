package main

import (
	"bytes"
	"errors"
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

func TestInventoryFindsNestedCapturesAndChannelLifetimes(t *testing.T) {
	root := t.TempDir()
	source := `package fixture
type Request struct { Count int; Names []string }
func work(request *Request, output chan int) {
    count := 0
    go func() {
        request.Count++
        request.Names[0] = "changed"
        *request = Request{}
        count++
        output <- count
        close(output)
    }()
}
`
	if err := os.WriteFile(filepath.Join(root, "fixture.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	var report bytes.Buffer
	if err := inventory(root, &report); err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{"\tcaptured-write\trequest.Count++", "\tcaptured-write\trequest.Names[0]", "\tcaptured-write\t*request", "\tcaptured-write\tcount++", "\tsend\toutput", "\tclose\tclose(output)"} {
		if !strings.Contains(report.String(), fragment) {
			t.Fatalf("inventory omitted %q: %s", fragment, report.String())
		}
	}
}

func TestInventoryReportsIncompleteInputAndOutput(t *testing.T) {
	root := t.TempDir()
	if err := inventory(filepath.Join(root, "missing"), &bytes.Buffer{}); err == nil {
		t.Fatal("missing repository produced a successful inventory")
	}
	if err := os.WriteFile(filepath.Join(root, "broken.go"), []byte("package"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := inventory(root, &bytes.Buffer{}); err == nil {
		t.Fatal("invalid Go file produced a successful inventory")
	}
	if err := os.Remove(filepath.Join(root, "broken.go")); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("inventory destination unavailable")
	if err := inventory(root, failingInventoryWriter{failure}); !errors.Is(err, failure) {
		t.Fatalf("output failure was hidden: %v", err)
	}
}

type failingInventoryWriter struct{ failure error }

func (writer failingInventoryWriter) Write([]byte) (int, error) { return 0, writer.failure }
