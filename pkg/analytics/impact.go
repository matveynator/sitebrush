package analytics

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"golang.org/x/net/html"
)

const ImpactWindow = 7 * 24 * time.Hour

type PageChange struct {
	Page, BeforeLayout, AfterLayout, BeforeLabel, AfterLabel, GoalKey string
	At                                                                time.Time
	Result                                                            *PageImpact
}
type PageImpact struct {
	Page, BeforeLabel, AfterLabel, BeforeRate, AfterRate, Relative, Points string
	At                                                                     time.Time
	BeforeVisitors, BeforeCompleted, AfterVisitors, AfterCompleted         int
	Partial, SmallSample, OtherChanges                                     bool
}

// Publication boundaries are real editor events. Equal observation windows
// describe association, without presenting a before/after comparison as causation.
func (site *Site) RecordPageChange(change PageChange) {
	if change.Page == "" || change.BeforeLayout == change.AfterLayout {
		return
	}
	if site.Conversion == nil {
		site.Conversion = &ConversionState{Version: 2, Visitors: map[string]*ConversionVisitor{}, Configurations: map[string]Goal{}, Daily: map[string]ConversionDaily{}, HourlyConversions: map[string]int{}}
	}
	state := site.Conversion
	for _, known := range state.Changes {
		if known.Page == change.Page && known.AfterLayout == change.AfterLayout && known.At.Equal(change.At) {
			return
		}
	}
	if len(state.Changes) >= 100 {
		state.Changes = append([]PageChange(nil), state.Changes[1:]...)
		state.Used -= 1024
	}
	for _, goal := range site.Goals {
		if change.GoalKey == "" || goal.Primary {
			change.GoalKey = GoalKey(goal)
			if goal.Primary {
				break
			}
		}
	}
	state.Changes = append(state.Changes, change)
	state.Used += 1024
}

func PublicationChange(page, before, after string, at time.Time) PageChange {
	oldDigest := sha256.Sum256([]byte(before))
	nextDigest := sha256.Sum256([]byte(after))
	change := PageChange{Page: page, BeforeLayout: hex.EncodeToString(oldDigest[:8]), AfterLayout: hex.EncodeToString(nextDigest[:8]), At: at}
	oldLabels := publishedActionLabels(before)
	nextLabels := publishedActionLabels(after)
	keys := make([]string, 0, len(oldLabels))
	for key := range oldLabels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		oldText := oldLabels[key]
		if nextText, exists := nextLabels[key]; exists && oldText != nextText {
			change.BeforeLabel = oldText
			change.AfterLabel = nextText
			break
		}
	}
	return change
}
func publishedActionLabels(document string) map[string]string {
	labels := map[string]string{}
	if len(document) > 2<<20 {
		return labels
	}
	root, err := html.Parse(strings.NewReader(document))
	if err != nil {
		return labels
	}
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if len(labels) >= 200 {
			return
		}
		if node.Type == html.ElementNode && (node.Data == "a" || node.Data == "button") {
			key := ""
			for _, attribute := range node.Attr {
				if attribute.Key == "data-analytics-key" || attribute.Key == "id" || attribute.Key == "href" {
					key = node.Data + ":" + attribute.Val
					break
				}
			}
			if key != "" {
				var text strings.Builder
				var collect func(*html.Node)
				collect = func(child *html.Node) {
					if child.Type == html.TextNode {
						text.WriteString(child.Data)
					}
					for nested := child.FirstChild; nested != nil; nested = nested.NextSibling {
						collect(nested)
					}
				}
				collect(node)
				labels[key] = CleanText(strings.Join(strings.Fields(text.String()), " "), 160)
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	return labels
}
func pageImpact(state *ConversionState, change PageChange, now time.Time) PageImpact {
	if change.Result != nil {
		return *change.Result
	}
	impact := PageImpact{Page: change.Page, At: change.At, BeforeLabel: change.BeforeLabel, AfterLabel: change.AfterLabel, Partial: now.Before(change.At.Add(2 * ImpactWindow))}
	for _, visitor := range state.Visitors {
		before, after := time.Time{}, time.Time{}
		for _, observation := range visitor.Behaviour {
			if observation.Kind != "page" || observation.Page != change.Page || observation.FirstAt.IsZero() {
				continue
			}
			visited := observation.FirstAt
			if !visited.Before(change.At.Add(-ImpactWindow)) && visited.Before(change.At) && (before.IsZero() || visited.Before(before)) {
				before = visited
			}
			if !visited.Before(change.At) && visited.Before(change.At.Add(ImpactWindow)) && !visited.After(now) && (after.IsZero() || visited.Before(after)) {
				after = visited
			}
		}
		completion := time.Time{}
		if progress := visitor.Funnels[change.GoalKey]; progress != nil {
			if goal, exists := state.Configurations[change.GoalKey]; exists && len(progress.Reached) == len(goalSteps(goal)) {
				completion = progress.Reached[len(progress.Reached)-1]
			}
		}
		if !before.IsZero() {
			impact.BeforeVisitors++
			if !completion.IsZero() && !completion.Before(before) && !completion.After(before.Add(ImpactWindow)) && !completion.After(now) {
				impact.BeforeCompleted++
			}
		}
		if !after.IsZero() {
			impact.AfterVisitors++
			if !completion.IsZero() && !completion.Before(after) && !completion.After(after.Add(ImpactWindow)) && !completion.After(now) {
				impact.AfterCompleted++
			}
		}
	}
	impact.BeforeRate = percentage(impact.BeforeCompleted, impact.BeforeVisitors)
	impact.AfterRate = percentage(impact.AfterCompleted, impact.AfterVisitors)
	if change.GoalKey == "" {
		impact.BeforeRate = "—"
		impact.AfterRate = "—"
	}
	impact.SmallSample = impact.BeforeVisitors < 30 || impact.AfterVisitors < 30 || impact.BeforeCompleted+impact.AfterCompleted < 3
	if impact.BeforeVisitors > 0 && impact.AfterVisitors > 0 {
		before := float64(impact.BeforeCompleted) / float64(impact.BeforeVisitors)
		after := float64(impact.AfterCompleted) / float64(impact.AfterVisitors)
		impact.Points = fmt.Sprintf("%+.1f", 100*(after-before))
		if before > 0 {
			impact.Relative = fmt.Sprintf("%+.1f%%", 100*(after/before-1))
		}
	}
	for _, other := range state.Changes {
		if other.Page == change.Page && !other.At.Equal(change.At) && other.At.After(change.At.Add(-ImpactWindow)) && other.At.Before(change.At.Add(ImpactWindow)) {
			impact.OtherChanges = true
		}
	}
	return impact
}
func pageImpacts(state *ConversionState, now time.Time) []PageImpact {
	result := []PageImpact{}
	for _, change := range state.Changes {
		if !change.At.After(now) {
			result = append(result, pageImpact(state, change, now))
		}
	}
	sort.Slice(result, func(left, right int) bool { return result[left].At.After(result[right].At) })
	return result
}
