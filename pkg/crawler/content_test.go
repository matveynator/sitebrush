package crawler

import (
	"encoding/hex"
	"testing"
)

func TestDetectedResourceContentTypeUsesContentSignature(t *testing.T) {
	jpegSample, decodeErr := hex.DecodeString("ffd8ffe000104a464946000101")
	if decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if contentType := DetectedResourceContentType("https://example.test/timthumb.php", "text/html", jpegSample); contentType != "image/jpeg" {
		t.Fatalf("JPEG content type = %q", contentType)
	}
	if contentType := DetectedResourceContentType("https://example.test/photo.jpg", "image/jpeg", []byte("<!doctype html><html><body>Not an image</body></html>")); contentType != "text/html" {
		t.Fatalf("HTML content type = %q", contentType)
	}
	if contentType := DetectedResourceContentType("https://example.test/app.js", "text/html", []byte("window.ready = true;")); contentType != "application/javascript" {
		t.Fatalf("JavaScript content type = %q", contentType)
	}
}

func TestDetectedResourceContentTypeRecognizesSVGXML(t *testing.T) {
	testCases := []struct {
		name                string
		declaredContentType string
		content             string
		expectedContentType string
	}{
		{
			name:                "XML declaration and doctype",
			declaredContentType: "image/svg+xml",
			content:             `<?xml version="1.0" standalone="no"?><!DOCTYPE svg PUBLIC "-//W3C//DTD SVG 1.1//EN" "http://www.w3.org/Graphics/SVG/1.1/DTD/svg11.dtd"><svg xmlns="http://www.w3.org/2000/svg"><defs/></svg>`,
			expectedContentType: "image/svg+xml",
		},
		{
			name:                "plain text header",
			declaredContentType: "text/plain",
			content:             `<svg xmlns="http://www.w3.org/2000/svg"><path d="M0 0"/></svg>`,
			expectedContentType: "image/svg+xml",
		},
		{
			name:                "ordinary XML",
			declaredContentType: "application/xml",
			content:             `<?xml version="1.0"?><catalog><entry/></catalog>`,
			expectedContentType: "text/xml",
		},
		{
			name:                "HTML containing SVG",
			declaredContentType: "image/svg+xml",
			content:             `<!doctype html><html><body><svg></svg></body></html>`,
			expectedContentType: "text/html",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			contentType := DetectedResourceContentType("https://example.test/resource.svg", testCase.declaredContentType, []byte(testCase.content))
			if contentType != testCase.expectedContentType {
				t.Fatalf("content type = %q, want %q", contentType, testCase.expectedContentType)
			}
		})
	}
}

func TestDownloadedResourceExtensionUsesDownloadedContentType(t *testing.T) {
	testCases := []struct {
		name        string
		resourceURL string
		contentType string
		expected    string
	}{
		{name: "dynamic image", resourceURL: "https://example.com/timthumb.php?src=photo.jpg", contentType: "image/jpeg", expected: ".jpg"},
		{name: "mismatched image", resourceURL: "https://example.com/photo.jpg", contentType: "image/webp", expected: ".webp"},
		{name: "dynamic PDF", resourceURL: "https://example.com/download.php?id=7", contentType: "application/pdf", expected: ".pdf"},
		{name: "generic response", resourceURL: "https://example.com/archive.zip", contentType: "application/octet-stream", expected: ".zip"},
		{name: "plain text response", resourceURL: "https://example.com/readme.txt", contentType: "text/plain; charset=utf-8", expected: ".txt"},
		{name: "unknown response", resourceURL: "https://example.com/file.custom", contentType: "application/x-unknown", expected: ".custom"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			actual := DownloadedResourceExtension(testCase.resourceURL, testCase.contentType)
			if actual != testCase.expected {
				t.Fatalf("DownloadedResourceExtension(%q, %q) = %q, want %q", testCase.resourceURL, testCase.contentType, actual, testCase.expected)
			}
		})
	}
}
