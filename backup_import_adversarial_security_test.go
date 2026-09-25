package main

import (
	"archive/zip"
	"bytes"
	"context"
	"strings"
	"testing"
	"encoding/json"
)

func backupZIPForSecurityTest(t *testing.T, backup domainBackup) *zip.Reader {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	entry, err := writer.Create("backup.json")
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(backup)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := entry.Write(encoded); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	reader, err := zip.NewReader(bytes.NewReader(buffer.Bytes()), int64(buffer.Len()))
	if err != nil {
		t.Fatal(err)
	}
	return reader
}

func TestSecurityBoundaryBackupImportRejectsTraversalVariants(t *testing.T) {
	for _, entryName := range []string{
		"../outside.txt",
		"files/../../outside.txt",
		"/absolute.txt",
		"\\server\\share\\owned.txt",
		"files\\..\\..\\outside.txt",
	} {
		if normalized, ok := safeBackupZIPEntryName(entryName); ok {
			t.Fatalf("SECURITY: unsafe backup path %q normalized as %q", entryName, normalized)
		}
	}
}

func TestSecurityBoundaryBackupImportRejectsDeclaredUncompressedBomb(t *testing.T) {
	entry := &zip.File{FileHeader: zip.FileHeader{Name: "files/bomb.bin", UncompressedSize64: uint64(backupImportUncompressedLimitBytes + 1)}}
	archive := &zip.Reader{File: []*zip.File{entry}}
	if _, err := (&App{}).importDomainBackupZIP(context.Background(), "localhost", "/", archive); err == nil || !strings.Contains(err.Error(), "uncompressed size") {
		t.Fatalf("SECURITY: oversized declared archive was accepted: %v", err)
	}
}

func TestSecurityBoundaryBackupImportRejectsAggregateUncompressedBomb(t *testing.T) {
	partSize := uint64(backupImportUncompressedLimitBytes/2 + 1)
	archive := &zip.Reader{File: []*zip.File{
		{FileHeader: zip.FileHeader{Name: "files/a.bin", UncompressedSize64: partSize}},
		{FileHeader: zip.FileHeader{Name: "files/b.bin", UncompressedSize64: partSize}},
	}}
	if _, err := (&App{}).importDomainBackupZIP(context.Background(), "localhost", "/", archive); err == nil || !strings.Contains(err.Error(), "uncompressed size") {
		t.Fatalf("SECURITY: aggregate ZIP bomb was accepted: %v", err)
	}
}

func TestSecurityBoundaryBackupImportFailsClosedOnDatabaseWriteError(t *testing.T) {
	application, rawDB := newTestApplication(t)
	if _, err := rawDB.Exec("DROP TABLE pages"); err != nil {
		t.Fatal(err)
	}
	archive := backupZIPForSecurityTest(t, domainBackup{
		Version: 1,
		Pages: []backupPage{{
			Path:  "/restored",
			Title: "Restored",
			HTML:  "<h1>restored</h1>",
		}},
	})
	if _, err := application.importDomainBackupZIP(context.Background(), "localhost", "/", archive); err == nil {
		t.Fatal("SECURITY: backup import reported success after the pages table write failed")
	}
}
