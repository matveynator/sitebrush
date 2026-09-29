package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// --- SARIF result enforcement ---

func TestScanDirectoryAcceptsCleanSARIF(t *testing.T) {
	directory := t.TempDir()
	writeSARIF(t, directory, "clean.sarif", `{"runs":[{"results":[]}]}`)

	summary, err := scanDirectory(directory)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Total != 0 || len(summary.ByRule) != 0 {
		t.Fatalf("unexpected findings: %#v", summary)
	}
}

func TestScanDirectoryRejectsUnsuppressedFindingsAcrossFiles(t *testing.T) {
	directory := t.TempDir()
	writeSARIF(t, directory, "go.sarif", `{"runs":[{"results":[{"ruleId":"go/log-injection"},{"ruleId":"go/log-injection"}]}]}`)
	writeSARIF(t, directory, "actions.sarif", `{"runs":[{"results":[{"ruleId":"actions/code-injection/critical"}]}]}`)

	summary, err := scanDirectory(directory)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Total != 3 {
		t.Fatalf("total=%d, want 3", summary.Total)
	}
	want := map[string]int{"actions/code-injection/critical": 1, "go/log-injection": 2}
	if !reflect.DeepEqual(summary.ByRule, want) {
		t.Fatalf("rules=%#v, want %#v", summary.ByRule, want)
	}
}

func TestScanDirectoryIgnoresAcceptedSuppressions(t *testing.T) {
	directory := t.TempDir()
	writeSARIF(t, directory, "suppressed.sarif", `{"runs":[{"results":[
		{"ruleId":"go/log-injection","suppressions":[{"status":"accepted"}]},
		{"ruleId":"go/request-forgery","suppressions":[{"status":"under-review"}]}
	]}]}`)

	summary, err := scanDirectory(directory)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Total != 1 || summary.ByRule["go/request-forgery"] != 1 {
		t.Fatalf("unexpected findings: %#v", summary)
	}
}

func TestScanDirectoryFailsClosedWithoutSARIF(t *testing.T) {
	_, err := scanDirectory(t.TempDir())
	if err == nil {
		t.Fatal("missing SARIF must fail closed")
	}
}

func TestScanDirectoryFailsClosedForMalformedSARIF(t *testing.T) {
	directory := t.TempDir()
	writeSARIF(t, directory, "broken.sarif", "{")

	if _, err := scanDirectory(directory); err == nil {
		t.Fatal("malformed SARIF must fail closed")
	}
}

// --- Test support ---

func writeSARIF(t *testing.T, directory, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(directory, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
