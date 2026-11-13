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
