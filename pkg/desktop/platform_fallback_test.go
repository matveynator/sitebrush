package desktop

import (
	"strings"
	"testing"
)

func TestNonDesktopFallbacksAreExplicit(t *testing.T) {
	if DefaultEnabled() {
		t.Fatal("server build unexpectedly enabled desktop mode")
	}
	if err := RunWebviewWindow("http://127.0.0.1", "test"); err == nil || !strings.Contains(err.Error(), "-tags desktop") {
		t.Fatalf("webview error = %v", err)
	}
	if err := OpenExternalURL("https://example.com"); err == nil {
		t.Fatal("server build unexpectedly opened an external URL")
	}
	if NativeFileDialogSupported() {
		t.Fatal("server build reported native dialog support")
	}
	if paths, err := PickFiles(); err == nil || len(paths) != 0 {
		t.Fatalf("PickFiles = %v, %v", paths, err)
	}
	if name, err := ChooseSaveFilePath("export.json"); err == nil || name != "" {
		t.Fatalf("ChooseSaveFilePath = %q, %v", name, err)
	}
	if name, err := SaveFile("export.json", []byte("data")); err == nil || name != "" {
		t.Fatalf("SaveFile = %q, %v", name, err)
	}
}
