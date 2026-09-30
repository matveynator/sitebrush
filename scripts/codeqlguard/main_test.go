package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// --- SARIF result enforcement ---

func TestScanDirectoryAcceptsCleanSARIF(t *testing.T) {
	directory := t.TempDir()
	writeSARIF(t, directory, "clean.sarif", `{"version":"2.1.0","runs":[{"results":[]}]}`)

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
	writeSARIF(t, directory, "go.sarif", `{"version":"2.1.0","runs":[{"results":[{"ruleId":"go/log-injection"},{"ruleId":"go/log-injection"}]}]}`)
	writeSARIF(t, directory, "actions.sarif", `{"version":"2.1.0","runs":[{"results":[{"ruleId":"actions/code-injection/critical"}]}]}`)

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
	writeSARIF(t, directory, "suppressed.sarif", `{"version":"2.1.0","runs":[{"results":[
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

func TestScanDirectoryFailsClosedForStructurallyInvalidSARIF(t *testing.T) {
	for name, content := range map[string]string{
		"empty-object":  `{}`,
		"null":          `null`,
		"missing-runs":  `{"version":"2.1.0"}`,
		"empty-runs":    `{"version":"2.1.0","runs":[]}`,
		"wrong-version": `{"version":"2.0.0","runs":[{"results":[]}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			directory := t.TempDir()
			writeSARIF(t, directory, "invalid.sarif", content)
			if _, err := scanDirectory(directory); err == nil {
				t.Fatalf("structurally invalid SARIF %s must fail closed", content)
			}
		})
	}
}



func TestScanDirectoryAcceptsOnlyExactValidatedBaselineFingerprint(t *testing.T) {
	directory := t.TempDir()
	writeSARIF(t, directory, "baseline.sarif", `{"version":"2.1.0","runs":[{"results":[
		{"ruleId":"go/request-forgery","locations":[{"physicalLocation":{"artifactLocation":{"uri":"pkg/crawler/download.go"}}}],"partialFingerprints":{"primaryLocationLineHash":"1b25405598db72a4:1"}},
		{"ruleId":"go/request-forgery","locations":[{"physicalLocation":{"artifactLocation":{"uri":"pkg/crawler/download.go"}}}],"partialFingerprints":{"primaryLocationLineHash":"new-location:1"}}
	]}]}`)

	summary, err := scanDirectory(directory)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Total != 1 || summary.ByRule["go/request-forgery"] != 1 {
		t.Fatalf("unexpected findings: %#v", summary)
	}

	data, err := os.ReadFile(filepath.Join(directory, "baseline.sarif"))
	if err != nil {
		t.Fatal(err)
	}
	rewritten := string(data)
	if strings.Contains(rewritten, "1b25405598db72a4:1") {
		t.Fatal("validated baseline result must be removed from reviewed SARIF")
	}
	if !strings.Contains(rewritten, "new-location:1") {
		t.Fatal("unmatched finding must remain in reviewed SARIF")
	}
}

// --- Workflow integration enforcement ---

func TestSecurityWorkflowRequiresCleanCodeQLResult(t *testing.T) {
	root := repositoryRoot(t)
	workflowPath := filepath.Join(root, ".github", "workflows", "security.yml")
	data, err := os.ReadFile(workflowPath)
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(data)

	outputMarker := "output: codeql-results"
	noUploadMarker := "upload: never"
	gateMarker := "run: go run ./scripts/codeqlguard"
	uploadMarker := "uses: github/codeql-action/upload-sarif@"
	outputIndex := strings.Index(workflow, outputMarker)
	noUploadIndex := strings.Index(workflow, noUploadMarker)
	gateIndex := strings.Index(workflow, gateMarker)
	uploadIndex := -1
	if gateIndex >= 0 {
		if relativeUploadIndex := strings.Index(workflow[gateIndex:], uploadMarker); relativeUploadIndex >= 0 {
			uploadIndex = gateIndex + relativeUploadIndex
		}
	}
	if outputIndex < 0 {
		t.Fatalf("security workflow must persist CodeQL SARIF with %q", outputMarker)
	}
	if noUploadIndex < 0 {
		t.Fatalf("CodeQL analyze must not upload unreviewed SARIF; missing %q", noUploadMarker)
	}
	if gateIndex < 0 {
		t.Fatalf("security workflow must enforce CodeQL results with %q", gateMarker)
	}
	if uploadIndex < 0 {
		t.Fatalf("security workflow must upload reviewed SARIF with %q", uploadMarker)
	}
	if gateIndex < outputIndex || uploadIndex < gateIndex {
		t.Fatal("CodeQL workflow order must be analyze -> review gate -> reviewed SARIF upload")
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(directory, "go.mod")); err == nil {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			t.Fatal("repository root containing go.mod was not found")
		}
		directory = parent
	}
}

// --- Test support ---

func writeSARIF(t *testing.T, directory, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(directory, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
