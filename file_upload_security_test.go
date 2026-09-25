package main

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSecurityBoundaryFileUploadRejectsBodyLargerThanAvailableStorage(t *testing.T) {
	application, rawDB := newTestApplication(t)
	if _, err := rawDB.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`,
		"localhost", "admin@example.com", "password"); err != nil {
		t.Fatal(err)
	}
	if _, err := rawDB.Exec(`INSERT OR REPLACE INTO domain_storage_usage(domain,page_bytes,published_page_bytes,revision_bytes,file_bytes,published_static_bytes,limit_bytes,updated_at) VALUES(?,?,?,?,?,?,?,?)`,
		"localhost", 0, 0, 0, 0, 0, 1, time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	fileWriter, err := writer.CreateFormFile("upload_files", "oversized.bin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fileWriter.Write(bytes.Repeat([]byte("A"), int(fileUploadMultipartOverheadBytes)+2)); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteField("action", "upload"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "http://localhost:8080/?files", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
	response := httptest.NewRecorder()

	application.route(response, request)

	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("SECURITY: oversized upload status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestSecurityBoundaryFileUploadFilenameTraversalIsNotAccepted(t *testing.T) {
	for _, fileName := range []string{
		"../outside.txt",
		"..\\outside.txt",
		"folder/../../outside.txt",
		"folder\\..\\..\\outside.txt",
	} {
		if safeFileName(fileName) != "" {
			t.Fatalf("SECURITY: traversal upload filename was accepted: %q", fileName)
		}
	}
}

func TestSecurityBoundaryFileUploadStreamsContentWithoutOriginalName(t *testing.T) {
	application, _ := newTestApplication(t)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	fileWriter, err := writer.CreateFormFile("upload_files", "folder/secret-name.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fileWriter.Write([]byte("streamed upload")); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteField("action", "upload"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "http://localhost:8080/docs?files", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.Header.Set("Accept", "application/json")
	response := httptest.NewRecorder()

	application.uploadFiles(response, request, "/docs")

	if response.Code != http.StatusOK {
		t.Fatalf("upload status=%d body=%q", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "secret-name") {
		t.Fatalf("SECURITY: original upload filename leaked into public stored name: %q", response.Body.String())
	}
}
