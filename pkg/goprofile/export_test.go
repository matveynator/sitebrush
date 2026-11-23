package goprofile

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/matveynator/sitebrush/v2/pkg/publicsite"
	"github.com/matveynator/sitebrush/v2/pkg/requesttiming"
)

func TestExportIncludesUnfilteredEvidenceAndBoundedUnicode(t *testing.T) {
	report := Report{RunID: "measurement", Role: "application", PID: 123, User: "_sitebrush", System: "openbsd", Architecture: "amd64", GoVersion: "go-test", Version: "42", Started: time.Now(), CaptureFinished: time.Now(), CPU: 300, Heap: 400, Allocated: 500,
		Functions:  []Function{{ID: 1, Name: "main.работа", File: "sitebrush.go", Line: 42, Self: 300, Total: 300}},
		Code:       []Function{{Name: "main.работа", File: "sitebrush.go", Line: 43, Self: 300, Total: 300}},
		Memory:     []Function{{Name: "allocate", Self: 100, Total: 100}},
		Stages:     []Stage{{Name: "front_state", Count: 1, MeanMS: 7, P95MS: 8}},
		HTTPCalls:  []requesttiming.HTTPCall{{Method: "GET", Host: "localhost", Route: "/?files", Status: 200, DurationUS: 1520}},
		Goroutines: "goroutine 1 [chan receive]:\nmain.wait(0xabc)\n\t/sitebrush.go:12 +0x1\n\n",
	}
	text := exportText(report)
	for _, fragment := range []string{"measurement", "openbsd/amd64", "PID: 123", "_sitebrush", "main.работа", "sitebrush.go:42", "sitebrush.go:43", "front_state", "Журнал HTTP-вызовов", "/?files", "1.520", "allocate", "[chan receive]", "main.wait @ /sitebrush.go:12"} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("export missing %q", fragment)
		}
	}
	if strings.Contains(text, "0xabc") {
		t.Fatal("stack argument addresses retained")
	}
	report.Functions[0].Name = strings.Repeat("я", exportLimit)
	text = exportText(report)
	if len(text) > exportLimit || !utf8.ValidString(text) || !strings.Contains(text, "Отчёт сокращён") {
		t.Fatal("text cap or UTF-8 boundary lost", len(text))
	}
}

func TestArtifactReaderOwnsFilesAndExportsCompleteReport(t *testing.T) {
	directory := t.TempDir()
	summary, err := temporaryProfile(directory)
	if err != nil {
		t.Fatal(err)
	}
	report := Report{RunID: "shared", Functions: []Function{{ID: 1, Name: "main.hiddenByFilter", Self: 99, Total: 99}}}
	if err = json.NewEncoder(summary).Encode(report); err != nil {
		t.Fatal(err)
	}
	info, err := summary.Stat()
	if err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	defer close(stop)
	record := ownArtifact(artifact{summary: summary, size: info.Size()}, stop)
	if record.summary != nil || record.reader == nil {
		t.Fatal("file ownership did not transfer")
	}
	reply := make(chan State, 1)
	record.reader <- readTask{task: task{command: Command{Action: "export", Search: "does-not-match", Page: 999}, reply: reply}}
	select {
	case state := <-reply:
		if !strings.Contains(state.Text, "main.hiddenByFilter") {
			t.Fatal("export followed active table filters", state)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reader did not reply")
	}
	closeArtifact(record)
	select {
	case <-record.done:
	case <-time.After(5 * time.Second):
		t.Fatal("reader session retained its descriptors")
	}
	if _, err = summary.Seek(0, 0); err == nil {
		t.Fatal("summary descriptor retained")
	}
}

func TestGroupedWaitingStacksExcludeArgumentsAndCountDuplicates(t *testing.T) {
	stacks := "goroutine 1 [chan receive]:\nmain.wait(0x111)\n\t/server.go:20 +0x1\n\n" +
		"goroutine 2 [chan receive]:\nmain.wait(0x222)\n\t/server.go:20 +0x2\n\n"
	groups := groupedStacks(stacks)
	if len(groups) != 1 || !strings.Contains(groups[0], "2 goroutine") || strings.Contains(groups[0], "0x") {
		t.Fatal(groups)
	}
}

func TestExportIncludesSafeRAMPolicyAndRecoveryDiagnostics(t *testing.T) {
	report := Report{RAM: &RAMReport{RAMCacheStats: publicsite.RAMCacheStats{Sites: []publicsite.RAMSiteStats{{Domain: "vz.sitebrush.com", RootRecovered: true, RecoveredPages: 2, Policy: &publicsite.RAMPolicyStats{RoutingVersion: 3, AdministratorIPRules: 1, ServerIPRules: 2}}}}}}
	output := exportText(report)
	for _, fragment := range []string{"domain=vz.sitebrush.com", "root_recovered=true", "recovered_pages=2", "frozen=false", "versioned=false", "admin_ip_rules=1", "server_ip_rules=2", "root_prepared=false"} {
		if !strings.Contains(output, fragment) {
			t.Fatal("missing diagnostics", fragment)
		}
	}
}
