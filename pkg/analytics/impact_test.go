package analytics

import (
	"fmt"
	"testing"
	"time"
)

func TestPublicationImpactUsesRealChangeAndEqualFollowup(t *testing.T) {
	changed := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	site := New(changed.Add(-ImpactWindow))
	site.Goals = []Goal{{Name: "Download", Primary: true, Kind: "event", Match: "download"}}
	addVisitors := func(count, completed int, visited time.Time, offset int) {
		for index := 0; index < count; index++ {
			identifier := fmt.Sprintf("%016x", offset+index+1)
			event := conversionEvent(identifier, identifier, "/pricing", 1, Signal{Kind: "page"})
			if index < completed {
				event.Signals = append(event.Signals, Signal{Kind: "goal", Name: "download", At: 1000})
			}
			site.Record(event, visited, 16<<20)
		}
	}
	addVisitors(100, 10, changed.Add(-24*time.Hour), 0)
	change := PublicationChange("/pricing", `<a href="/download">Download</a>`, `<a href="/download">Download for Linux</a>`, changed)
	site.RecordPageChange(change)
	addVisitors(100, 20, changed.Add(24*time.Hour), 100)
	impact := site.Report(changed.Add(3*24*time.Hour), 30).Conversion.Impacts[0]
	if impact.BeforeRate != "10.0%" || impact.AfterRate != "20.0%" || impact.Relative != "+100.0%" || impact.BeforeLabel != "Download" || impact.AfterLabel != "Download for Linux" || !impact.Partial || impact.SmallSample {
		t.Fatal("incorrect publication comparison", impact)
	}
	site.Prune(changed.Add(2 * ImpactWindow))
	frozen := site.Report(changed.Add(2*ImpactWindow), 30).Conversion.Impacts[0]
	site.Prune(changed.Add(100 * 24 * time.Hour))
	later := site.Report(changed.Add(100*24*time.Hour), 90).Conversion.Impacts[0]
	if frozen.Partial || later.BeforeRate != frozen.BeforeRate || later.AfterRate != frozen.AfterRate {
		t.Fatal("aggregate impact disappeared with visitor expiry")
	}
}
func TestDeviceAndFunnelInsightsNeedMeaningfulSamples(t *testing.T) {
	report := ConversionReport{Devices: []ConversionDevice{{Name: "mobile", Visitors: 100, Completed: 10}, {Name: "desktop", Visitors: 100, Completed: 20}}, Funnels: []ConversionFunnel{{Primary: true, Users: 100, Steps: []ConversionStep{{Name: "Visitors", Users: 100, Lost: 42}, {Name: "Pricing", Users: 58}}}}}
	insights := conversionInsights(report)
	if len(insights) != 2 || insights[0].Kind != "device" || insights[0].Rate != "50.0%" || insights[1].Kind != "funnel" || insights[1].Rate != "42.0%" {
		t.Fatal("missing deterministic insights", insights)
	}
	report.Devices[0].Visitors = 5
	report.Funnels[0].Users = 5
	if len(conversionInsights(report)) != 0 {
		t.Fatal("tiny samples produced insights")
	}
}
