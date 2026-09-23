package analytics

import (
	"testing"
	"time"
)

func TestIndexingCrawlerSkipsEnumerationButNotExploitProbe(t *testing.T) {
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	state := &SecurityState{}
	for index := 0; index < 64; index++ {
		category := state.Record(RequestObservation{
			Time:            now.Add(time.Duration(index) * time.Millisecond),
			IP:              "192.0.2.10",
			Path:            "/page-" + string(rune('a'+index%26)) + "-" + time.Duration(index).String(),
			Method:          "GET",
			Agent:           "Googlebot/2.1",
			Status:          200,
			IndexingCrawler: true,
		})
		if category != "" {
			t.Fatalf("indexing crawl classified as attack: %q", category)
		}
	}

	category := state.Record(RequestObservation{
		Time:            now.Add(time.Second),
		IP:              "192.0.2.10",
		Path:            "/.git/config",
		Method:          "GET",
		Agent:           "Googlebot/2.1",
		Status:          404,
		IndexingCrawler: true,
	})
	if category != "repository" {
		t.Fatalf("exploit probe category = %q, want repository", category)
	}
}
