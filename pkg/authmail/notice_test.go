package authmail

import (
	"strings"
	"testing"
)

func TestReleaseNoticeEscapesContentAndRendersAllDestinations(t *testing.T) {
	notice := Notice{Language: "he", Direction: "rtl", Title: "Release <script>alert(1)</script>", Introduction: "Free checked update", Reason: "Important security fix", Button: "View and update", Details: "Release notes", DetailsURL: "https://github.com/matveynator/sitebrush/releases/tag/release-21", Links: []NoticeLink{{Label: "First <installation>", URL: "https://first.example/?server_update"}, {Label: "Second", URL: "https://second.example/?server_update"}}}
	text, markup, err := RenderNotice(notice)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`lang="he"`, `dir="rtl"`, "#087f8c", "&lt;script&gt;", notice.Links[0].URL, notice.Links[1].URL, notice.DetailsURL} {
		if !strings.Contains(markup, expected) {
			t.Fatal("missing notice content", expected)
		}
	}
	if strings.Contains(markup, "<script>") {
		t.Fatal("unescaped HTML in mail")
	}
	if !strings.Contains(text, notice.Links[0].URL) || !strings.Contains(text, notice.Reason) {
		t.Fatal("plain text fallback lost information")
	}
	notice.Links[0].URL = "javascript:alert(1)"
	_, markup, err = RenderNotice(notice)
	if err != nil || strings.Contains(markup, `href="javascript:`) {
		t.Fatal("unsafe mail button", err)
	}
}
