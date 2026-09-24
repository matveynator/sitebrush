package crawler

import (
	"errors"
	"net/url"
	"testing"
)

func TestCoverageHelpers(t *testing.T) {
	for contentType, want := range map[string]string{
		"text/css": "style", "application/javascript": "script", "image/png": "image",
		"font/woff2": "font", "video/mp4": "video", "audio/ogg": "audio",
		"text/plain": "file", "application/pdf": "file", "unknown": "",
	} {
		if got := ResourceKindFromContentType(contentType); got != want {
			t.Errorf("kind(%q)=%q want %q", contentType, got, want)
		}
	}
	for errText, want := range map[string]string{
		"": "error", "request failed 404 Not Found": "404", "deadline exceeded": "timeout",
		"no such host": "dns", "TLS handshake failed": "tls", "connection refused": "refused",
		"read failure": "read", "broken pipe": "network",
	} {
		var err error
		if errText != "" {
			err = errors.New(errText)
		}
		if got := ErrorReason(err); got != want {
			t.Errorf("reason(%q)=%q want %q", errText, got, want)
		}
	}
	for input, want := range map[string]string{
		"": "", " https://host/a ": "https://host/a", "//host/a": "/host/a",
		"data:image/png,x": "data:image/png,x", "blob:x": "blob:x", " /asset ": "/asset",
	} {
		if got := NormalizeMirroredAssetReference(input); got != want {
			t.Errorf("normalize(%q)=%q want %q", input, got, want)
		}
	}
	if CloneURL(nil) != nil {
		t.Fatal("CloneURL(nil) should be nil")
	}
	original, _ := url.Parse("https://example.test/a")
	clone := CloneURL(original)
	clone.Path = "/b"
	if original.Path != "/a" {
		t.Fatalf("clone shares URL state: %+v", original)
	}
	if CurrentWholeSiteImportURL(nil) != "" || CurrentWholeSiteImportURL([]WholeSitePageJob{{}}) != "" {
		t.Fatal("empty queue URL should be empty")
	}
	if got := CurrentWholeSiteImportURL([]WholeSitePageJob{{URL: original}}); got != original.String() {
		t.Fatalf("queue URL=%q", got)
	}
}
