package analytics

import "testing"

func TestConversionURISignalsAndConstraints(t *testing.T) {
	event := Event{Path: "/offer", BrowserContext: BrowserContext{URI: "/offer"}}
	step := Goal{Kind: "uri", Match: "/offer?plan=small#buy"}
	cases := []struct {
		name    string
		step    Goal
		signal  Signal
		matches bool
	}{
		{"exact browser signal", step, Signal{Kind: "goal", Name: URIEventName(step.Match)}, true},
		{"different browser signal", step, Signal{Kind: "goal", Name: URIEventName("/offer?plan=large#buy")}, false},
		{"page alone cannot match exact query", step, Signal{Kind: "page"}, false},
		{"path page compatibility", Goal{Kind: "path", Match: "/offer"}, Signal{Kind: "page"}, true},
		{"page constraint", Goal{Kind: "uri", Match: step.Match, Page: "/elsewhere"}, Signal{Kind: "goal", Name: URIEventName(step.Match)}, false},
		{"element constraint", Goal{Kind: "uri", Match: step.Match, Element: "key:buy"}, Signal{Kind: "goal", Name: URIEventName(step.Match)}, false},
		{"element match", Goal{Kind: "uri", Match: step.Match, Element: "key:buy"}, Signal{Kind: "goal", Name: URIEventName(step.Match), Element: "key:buy"}, true},
		{"action download", Goal{Kind: "action", Match: "download"}, Signal{Kind: "download", Name: "download"}, true},
		{"action mismatch", Goal{Kind: "action", Match: "download"}, Signal{Kind: "goal", Name: "other"}, false},
		{"scroll threshold", Goal{Kind: "scroll", Threshold: 70}, Signal{Kind: "scroll", Scroll: 70}, true},
		{"scroll too shallow", Goal{Kind: "scroll", Threshold: 70}, Signal{Kind: "scroll", Scroll: 69}, false},
		{"event", Goal{Kind: "event", Match: "buy"}, Signal{Kind: "goal", Name: "buy"}, true},
		{"ordinary kind", Goal{Kind: "click"}, Signal{Kind: "click"}, true},
	}
	for _, scenario := range cases {
		t.Run(scenario.name, func(t *testing.T) {
			if actual := stepMatches(scenario.step, event, scenario.signal); actual != scenario.matches {
				t.Fatalf("stepMatches = %t, want %t", actual, scenario.matches)
			}
		})
	}
}
