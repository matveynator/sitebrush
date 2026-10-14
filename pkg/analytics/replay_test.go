package analytics

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestReplaySamplingPrivacyDeduplicationAndExpiry(t *testing.T) {
	preferences := ReplayPreferences{Enabled: true, Sample: 5}
	sampled := 0
	for index := 0; index < 10000; index++ {
		if ReplaySampled(fmt.Sprintf("%032x", index), preferences) {
			sampled++
		}
	}
	if sampled < 400 || sampled > 600 || ReplaySampled("session", ReplayPreferences{Sample: 5}) {
		t.Fatal("sampling is not optional or approximately five percent", sampled)
	}
	now := time.Now().UTC()
	site := New(now)
	event := conversionEvent("1111111111111111", "2222222222222222", "/", 1, Signal{Kind: "page"})
	event.ReplayAllowed = true
	event.Replay = []ReplayEvent{{Kind: "navigation", Width: 1280, Height: 800}, {Kind: "field", Element: "key:password", Filled: true, At: 1000}, {Kind: "click", Element: "key:download", At: 1100}}
	event.Signals = append(event.Signals, Signal{Kind: "rage-click", Element: "key:download"})
	if !Valid(event) || !site.Record(event, now, 4<<20) || site.Record(event, now, 4<<20) {
		t.Fatal("replay sequence admission failed")
	}
	summary := site.Report(now, 7).Conversion.Sessions
	if len(summary) != 1 || summary[0].Events != 3 || len(summary[0].Reasons) != 1 {
		t.Fatal("replay missing or duplicated", summary)
	}
	var injected ReplayEvent
	if err := json.Unmarshal([]byte(`{"kind":"field","element":"key:password","filled":true,"field_text":"password-secret","email":"personal@example.com"}`), &injected); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(injected)
	if strings.Contains(string(encoded), "secret") || strings.Contains(string(encoded), "personal") {
		t.Fatal("untyped replay values survived")
	}
	site.Prune(now.Add(ReplayRetention + time.Hour))
	if len(site.Report(now.Add(ReplayRetention+time.Hour), 30).Conversion.Sessions) != 0 || len(site.Conversion.Visitors) != 1 {
		t.Fatal("replay expiry changed ordinary analytics")
	}
}
func TestReplayDocumentIsInertAndVersionBound(t *testing.T) {
	content := `<html><body><input value="password-secret"><textarea>private-message</textarea><script data-sitebrush-owned="true" data-analytics-layout="abcdef0123456789">execute()</script><button data-analytics-key="download">Download</button></body></html>`
	snapshot, err := ReplayDocument(content, "/", "abcdef0123456789")
	if err != nil || strings.Contains(snapshot, "password-secret") || strings.Contains(snapshot, "private-message") || strings.Contains(snapshot, "<script") || !strings.Contains(snapshot, "Content-Security-Policy") {
		t.Fatal("snapshot contains private or executable content", err, snapshot)
	}
	if _, err = ReplayDocument(content, "/", "ffff"); err == nil {
		t.Fatal("another published version used for replay")
	}
	if _, err = ReplayDocument(strings.Repeat("large", MaximumReplayDocument), "/", "abcd"); err == nil {
		t.Fatal("unbounded snapshot accepted")
	}
}
func TestReplayBudgetDoesNotEvictConversion(t *testing.T) {
	now := time.Now().UTC()
	site := New(now)
	site.Goals = []Goal{{Name: "Purchase", Kind: "event", Match: "purchase"}}
	event := conversionEvent("1111111111111111", "2222222222222222", "/", 1, Signal{Kind: "page"}, Signal{Kind: "goal", Name: "purchase"})
	event.ReplayAllowed = true
	event.Replay = []ReplayEvent{{Kind: "click", Element: "key:download"}}
	site.Record(event, now, 4<<20)
	for index := 0; index < 100; index++ {
		event.Sequence++
		event.Signals = nil
		event.Replay = make([]ReplayEvent, 20)
		for action := range event.Replay {
			event.Replay[action] = ReplayEvent{Kind: "click", Element: "key:download", At: int64(action)}
		}
		site.Record(event, now.Add(time.Duration(index+1)*time.Second), 4<<20)
	}
	report := site.Report(now.Add(time.Hour), 7).Conversion
	if report.Funnels[0].Completed != 1 || len(report.Sessions) != 1 || !report.Sessions[0].Limited || report.Sessions[0].Events > MaximumReplayEvents {
		t.Fatal("replay pressure changed admitted conversion")
	}
}
