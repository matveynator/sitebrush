package analytics

import (
	"strings"
	"time"
)

const audienceVisitBudget int64 = 512
const audiencePageBudget int64 = 2048

// Audience observations belong to the existing site worker and checkpoint.
// Session identities never enter the public aggregate report or AI response.
type AudienceVisit struct {
	Session     string
	First, Last time.Time
	Pages       map[string]time.Time
}

type AudienceMetrics struct {
	Since                    time.Time
	Sessions, ReturningUsers int
	Incomplete               bool
}

func (site *Site) recordAudience(visitor *ConversionVisitor, event Event, now time.Time) {
	state := site.Conversion
	if state.AudienceSince.IsZero() {
		// Restored checkpoints cannot reconstruct sessions that were never retained.
		state.AudienceSince = now
	}
	var visit *AudienceVisit
	for index := len(visitor.Visits) - 1; index >= 0; index-- {
		candidate := visitor.Visits[index]
		if candidate.Session == event.Session && now.Sub(candidate.Last) <= 30*time.Minute {
			visit = candidate
			break
		}
	}
	if visit == nil {
		if len(visitor.Visits) >= 256 {
			state.Incomplete = true
			return
		}
		visit = &AudienceVisit{Session: event.Session, First: now, Pages: map[string]time.Time{}}
		visitor.Visits = append(visitor.Visits, visit)
		if visitor.FirstVisit.IsZero() {
			visitor.FirstVisit = now
		}
		state.Used += audienceVisitBudget
	}
	visit.Last = now
	for _, signal := range event.Signals {
		if signal.Kind != "page" {
			continue
		}
		key := dayKey(now) + "\x1f" + event.Path
		if _, known := visit.Pages[key]; !known {
			if len(visit.Pages) >= 128 {
				state.Incomplete = true
				return
			}
			state.Used += audiencePageBudget
		}
		visit.Pages[key] = now
	}
}

func pruneAudience(state *ConversionState, visitor *ConversionVisitor, now time.Time) {
	retained := visitor.Visits[:0]
	cutoff := dayStart(now.UTC()).AddDate(0, 0, -89)
	for _, visit := range visitor.Visits {
		if visit.Last.Before(cutoff) {
			continue
		}
		for key, stamp := range visit.Pages {
			if stamp.Before(cutoff) {
				delete(visit.Pages, key)
			}
		}
		retained = append(retained, visit)
		state.Used += audienceVisitBudget + int64(len(visit.Pages))*audiencePageBudget
	}
	clear(visitor.Visits[len(retained):])
	visitor.Visits = retained
}

func addAudience(result *ConversionReport, visitor *ConversionVisitor, pages map[string]*PageAudience, start, now time.Time) {
	if result.Audience == nil {
		return
	}
	pageSessions := map[string]int{}
	pageDates := map[string]map[string]int{}
	returned := false
	for index, visit := range visitor.Visits {
		if visit.Last.Before(start) || visit.First.After(now) {
			continue
		}
		result.Audience.Sessions++
		returned = returned || index > 0 || visit.First.After(visitor.FirstVisit)
		seen := map[string]bool{}
		for key, stamp := range visit.Pages {
			if stamp.Before(start) || stamp.After(now) {
				continue
			}
			date, page, found := strings.Cut(key, "\x1f")
			if !found {
				continue
			}
			if !seen[page] {
				pageSessions[page]++
				seen[page] = true
			}
			if pageDates[page] == nil {
				pageDates[page] = map[string]int{}
			}
			pageDates[page][date]++
		}
	}
	if returned {
		result.Audience.ReturningUsers++
	}
	for page, sessions := range pageSessions {
		audience := pages[page]
		if audience == nil {
			continue
		}
		if audience.Sessions == nil {
			audience.Sessions, audience.ReturningUsers, audience.ReturningSameDay = new(int), new(int), new(int)
		}
		*audience.Sessions += sessions
		if sessions > 1 {
			*audience.ReturningUsers++
		}
		for _, count := range pageDates[page] {
			if count > 1 {
				*audience.ReturningSameDay++
				break
			}
		}
	}
}
