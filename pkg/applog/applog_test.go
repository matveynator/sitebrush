package applog

import (
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestJournalRotatesExistingDayAndCompresses(t *testing.T) {
	storagePath := t.TempDir()
	logDirectory := filepath.Join(storagePath, "storage", "logs")
	if err := os.MkdirAll(logDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	previousDay := time.Now().UTC().AddDate(0, 0, -1)
	activePath := filepath.Join(logDirectory, activeName)
	if err := os.WriteFile(activePath, []byte("previous day\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(activePath, previousDay, previousDay); err != nil {
		t.Fatal(err)
	}
	journal, err := Open(storagePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := journal.Write([]byte("current day\n")); err != nil {
		t.Fatal(err)
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	active, err := os.ReadFile(activePath)
	if err != nil || string(active) != "current day\n" {
		t.Fatalf("active log = %q, error = %v", active, err)
	}
	archivePath := filepath.Join(logDirectory, "sitebrush-"+previousDay.Format("2006-01-02")+"-000001.log.gz")
	archive, err := os.Open(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	compressed, err := gzip.NewReader(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer compressed.Close()
	contents, err := io.ReadAll(compressed)
	if err != nil || string(contents) != "previous day\n" {
		t.Fatalf("archive = %q, error = %v", contents, err)
	}
}

func TestJournalPrunesOldArchivesAndLegacyLogs(t *testing.T) {
	storagePath := t.TempDir()
	logDirectory := filepath.Join(storagePath, "storage", "logs")
	legacyDirectory := filepath.Join(logDirectory, "example.com")
	if err := os.MkdirAll(legacyDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	oldDate := now.AddDate(0, 0, -31).Format("2006-01-02")
	recentDate := now.AddDate(0, 0, -1).Format("2006-01-02")
	for _, name := range []string{"sitebrush-" + oldDate + "-000001.log.gz", "sitebrush-" + recentDate + "-000001.log.gz"} {
		if err := os.WriteFile(filepath.Join(logDirectory, name), []byte("archive"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{oldDate + ".log", recentDate + ".log"} {
		if err := os.WriteFile(filepath.Join(legacyDirectory, name), []byte("legacy"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	journal, err := Open(storagePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"sitebrush-" + oldDate + "-000001.log.gz", filepath.Join("example.com", oldDate+".log")} {
		if _, err := os.Stat(filepath.Join(logDirectory, name)); !os.IsNotExist(err) {
			t.Fatalf("old log %s still exists: %v", name, err)
		}
	}
	for _, name := range []string{"sitebrush-" + recentDate + "-000001.log.gz", filepath.Join("example.com", recentDate+".log")} {
		if _, err := os.Stat(filepath.Join(logDirectory, name)); err != nil {
			t.Fatalf("recent log %s removed: %v", name, err)
		}
	}
}

func TestCaptureCombinesProcessOutput(t *testing.T) {
	storagePath := t.TempDir()
	closeJournal, err := Capture(storagePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(os.Stdout, "standard output\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(os.Stderr, "standard error\n"); err != nil {
		t.Fatal(err)
	}
	if err := closeJournal(); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(filepath.Join(storagePath, "storage", "logs", activeName))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"standard output", "standard error"} {
		if !strings.Contains(string(contents), expected) {
			t.Fatalf("shared log does not contain %q: %q", expected, contents)
		}
	}
}

func TestCustomLogFileRotatesAndKeepsDirectoryPermissions(t *testing.T) {
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	logFile := filepath.Join(directory, "hosting-events.log")
	yesterday := time.Now().UTC().AddDate(0, 0, -1)
	if err := os.WriteFile(logFile, []byte("previous day\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(logFile, yesterday, yesterday); err != nil {
		t.Fatal(err)
	}
	journal, err := OpenFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := journal.Write([]byte("current day\n")); err != nil {
		t.Fatal(err)
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	directoryInfo, err := os.Stat(directory)
	if err != nil || directoryInfo.Mode().Perm() != 0o755 {
		t.Fatalf("custom log directory permissions changed: %v, %v", directoryInfo, err)
	}
	active, err := os.ReadFile(logFile)
	if err != nil || string(active) != "current day\n" {
		t.Fatalf("custom active log = %q, error = %v", active, err)
	}
	archivePath := filepath.Join(directory, "hosting-events-"+yesterday.Format("2006-01-02")+"-000001.log.gz")
	archive, err := os.Open(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	compressed, err := gzip.NewReader(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer compressed.Close()
	contents, err := io.ReadAll(compressed)
	if err != nil || string(contents) != "previous day\n" {
		t.Fatalf("custom archive = %q, error = %v", contents, err)
	}
}

func TestCustomLogFilePrunesOnlyMatchingArchives(t *testing.T) {
	directory := t.TempDir()
	day := time.Now().UTC().AddDate(0, 0, -31).Format("2006-01-02")
	ownArchive := filepath.Join(directory, "custom-"+day+"-000001.log.gz")
	unrelatedArchive := filepath.Join(directory, "sitebrush-"+day+"-000001.log.gz")
	for _, archive := range []string{ownArchive, unrelatedArchive} {
		if err := os.WriteFile(archive, []byte("archive"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	journal, err := OpenFile(filepath.Join(directory, "custom.log"))
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(ownArchive); !os.IsNotExist(err) {
		t.Fatalf("expired custom archive was not removed: %v", err)
	}
	if _, err := os.Stat(unrelatedArchive); err != nil {
		t.Fatalf("unrelated archive was removed: %v", err)
	}
}

func TestCustomLogFileRejectsInvalidPaths(t *testing.T) {
	for _, logFile := range []string{"relative.log", "", "stdout", filepath.Join(t.TempDir(), "filename.txt")} {
		if journal, err := OpenFile(logFile); err == nil {
			journal.Close()
			t.Fatalf("invalid log path accepted: %q", logFile)
		}
	}
}

func TestCaptureFileCombinesProcessOutput(t *testing.T) {
	logFile := filepath.Join(t.TempDir(), "custom.log")
	closeJournal, err := CaptureFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(os.Stdout, "file stdout\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(os.Stderr, "file stderr\n"); err != nil {
		t.Fatal(err)
	}
	if err := closeJournal(); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), "file stdout") || !strings.Contains(string(contents), "file stderr") {
		t.Fatalf("configured log does not contain both streams: %q", contents)
	}
}
