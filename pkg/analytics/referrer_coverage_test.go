package analytics

import (
	"strings"
	"testing"
)

func TestSafeReferrerCoverageBoundaries(t *testing.T) {
	if safe := SafeReferrer("%"); safe != "" {
		t.Fatalf("invalid referrer accepted: %q", safe)
	}
	if safe := SafeReferrer("https:///missing-host"); safe != "" {
		t.Fatalf("hostless referrer accepted: %q", safe)
	}

	longQuery := "https://example.org/news?note=" + strings.Repeat("a", 1100)
	if safe := SafeReferrer(longQuery); safe != "https://example.org/news" {
		t.Fatalf("long query did not fall back to path-only referrer: %q", safe)
	}

	longHost := "https://" + strings.Repeat("a", 1100) + ".example/news"
	if safe := SafeReferrer(longHost); safe != "" {
		t.Fatalf("oversized host referrer accepted: %q", safe)
	}
}

func TestExperienceDirectReturnInsightCoverage(t *testing.T) {
	report := ExperienceReport{Recent: []SessionSummary{{
		Source:      Attribution{Name: "direct"},
		FirstSource: "GitHub",
		Class:       "human-likely",
		Tabs:        map[string][]string{"tab": {"/", "/docs/"}},
	}}}
	view := report.View(ExperienceFilter{})
	for _, insight := range view.Insights {
		if insight.Kind == "hidden-return" {
			if insight.Numerator != 1 {
				t.Fatalf("hidden return numerator = %d", insight.Numerator)
			}
			return
		}
	}
	t.Fatal("direct return insight missing")
}
