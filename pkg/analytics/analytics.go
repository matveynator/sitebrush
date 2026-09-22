// Package analytics maintains bounded browser observations. Each Site belongs to
// one channel worker; snapshots transfer ownership to persistence workers.
package analytics

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

const RetentionDays = 90
const MaximumPages = 64

// Event carries cumulative engagement so retransmission cannot inflate totals.
type Event struct {
	BrowserContext
	Attribution Attribution `json:"-"`
	Visitor     string      `json:"visitor"`
	View        string      `json:"view"`
	Sequence    int         `json:"sequence"`
	Path        string      `json:"path"`
	Referrer    string      `json:"referrer"`
	Source      string      `json:"source"`
	ActiveMS    int64       `json:"active_ms"`
	Scroll      int         `json:"scroll"`
	Persistent  bool        `json:"persistent"`
}

type Observation struct {
	Views     int
	Sessions  int
	ActiveMS  int64
	Returning bool
}

type Page struct {
	RecentDays  [3]string
	LastDay     string
	Days        int
	LastSession int
	Scroll      int
	LastSeen    time.Time
}

type View struct {
	Started         time.Time
	Actions         map[string]int
	Acted, Next     bool
	Sequence        int
	ActiveMS        int64
	Scroll          int
	Path            string
	Session         int
	Seen            time.Time
	PreviousScroll  int
	PreviousSession int
	Continued       bool
}

type Visitor struct {
	First         time.Time
	Last          time.Time
	FirstSource   string
	SessionSource string
	Session       int
	SessionViews  int
	SessionDay    string
	LastPath      string
	LastPathDay   string
	Days          map[string]*Observation
	Pages         map[string]*Page
	Views         map[string]*View `json:"-"`
	Trail         []string         `json:"-"`
	TrailStart    time.Time
	DirectDays    map[string]bool
	Persistent    bool
	Limited       bool
}

type Day struct {
	Views           int
	Sessions        int
	SingleSessions  int
	ActiveMS        int64
	Scroll          [4]int
	Pages           map[string]int
	Sources         map[string]int
	Entries         map[string]int
	Exits           map[string]int
	Transitions     map[string]int
	Insights        map[string]int
	ReturnSources   map[string]int
	ReturnIntervals map[string]int
}

type Site struct {
	Goals           []Goal
	Experience      Experience
	ProcessingLagMS int64
	QueuePercent    int
	Started         time.Time
	Visitors        map[string]*Visitor
	Days            map[string]*Day
	Incomplete      bool
	HistoryLimited  bool
	LastEvent       time.Time
	Used            int64
}

type Row struct {
	Label string
	Count int
}
type Retention struct {
	Day      int
	Eligible int
	Returned int
}
type Report struct {
	Experience          ExperienceReport
	ProcessingLagMS     int64
	QueuePercent        int
	EstimatedBytes      int64
	ComparisonAvailable bool
	PeriodStart         time.Time
	PeriodEnd           time.Time
	Generated           time.Time
	Started             time.Time
	Days                int
	Views               int
	Visitors            int
	New                 int
	Returning           int
	Continuing          int
	Resurrected         int
	Dormant             int
	Sessions            int
	SingleSessions      int
	ActiveMS            int64
	Scroll              [4]int
	Pages               []Row
	Sources             []Row
	FirstSources        []Row
	ReturnSources       []Row
	Entries             []Row
	Exits               []Row
	Transitions         []Row
	Insights            []Row
	ActiveDays          []Row
	ReturnIntervals     []Row
	Retention           []Retention
	Incomplete          bool
	HistoryLimited      bool
	Temporary           int
}

func New(now time.Time) *Site {
	return &Site{Started: now, Visitors: make(map[string]*Visitor), Days: make(map[string]*Day)}
}

func dayKey(now time.Time) string { return now.UTC().Format("2006-01-02") }
func dayStart(now time.Time) time.Time {
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
}

// Recount also discards stale state before a restored snapshot is admitted.
func (site *Site) Prune(now time.Time) {
	cutoff := dayKey(dayStart(now.UTC()).AddDate(0, 0, -RetentionDays+1))
	site.pruneExperience(now)
	site.Used = 1024 + site.experienceBytes()
	if site.Visitors == nil {
		site.Visitors = make(map[string]*Visitor)
	}
	if site.Days == nil {
		site.Days = make(map[string]*Day)
	}
	for date := range site.Days {
		if date < cutoff {
			delete(site.Days, date)
		} else {
			daily := site.Days[date]
			if daily.ReturnSources == nil {
				daily.ReturnSources = map[string]int{}
			}
			if daily.ReturnIntervals == nil {
				daily.ReturnIntervals = map[string]int{}
			}
			site.Used += dayBytes(daily)
		}
	}
	for identity, visitor := range site.Visitors {
		if dayKey(visitor.Last) < cutoff {
			delete(site.Visitors, identity)
			continue
		}
		if visitor.Views == nil {
			visitor.Views = make(map[string]*View)
		}
		for date := range visitor.Days {
			if date < cutoff {
				delete(visitor.Days, date)
			}
		}
		for date := range visitor.DirectDays {
			if date < cutoff {
				delete(visitor.DirectDays, date)
			}
		}
		for pathname, page := range visitor.Pages {
			if page.LastDay < cutoff {
				delete(visitor.Pages, pathname)
			}
		}
		for identity, view := range visitor.Views {
			if now.Sub(view.Seen) > 24*time.Hour {
				delete(visitor.Views, identity)
			}
		}
		for _, view := range visitor.Views {
			site.Used += viewBytes(view)
		}
		site.Used += 4096 + int64(len(visitor.Pages))*768 + int64(len(visitor.Days))*128
	}
}

func viewBytes(view *View) int64 {
	total := int64(768)
	if view != nil {
		for key := range view.Actions {
			total += int64(len(key) + 96)
		}
	}
	return total
}

func dayBytes(day *Day) int64 {
	total := int64(1024)
	for _, counters := range []map[string]int{day.Pages, day.Sources, day.Entries, day.Exits, day.Transitions, day.Insights, day.ReturnSources, day.ReturnIntervals} {
		for key := range counters {
			total += int64(len(key) + 96)
		}
	}
	return total
}

func newDay() *Day {
	return &Day{Pages: map[string]int{}, Sources: map[string]int{}, Entries: map[string]int{}, Exits: map[string]int{}, Transitions: map[string]int{}, Insights: map[string]int{}, ReturnSources: map[string]int{}, ReturnIntervals: map[string]int{}}
}

// Fixed cardinality prevents hostile paths and sources from growing counters.
func increment(counters map[string]int, label string) {
	if _, exists := counters[label]; !exists && len(counters) >= 32 {
		label = "(other)"
	}
	counters[label]++
}

func (site *Site) Record(event Event, now time.Time, limit int64) bool {
	if dayKey(site.LastEvent) != dayKey(now) {
		site.Prune(now)
	}
	if site.Used+16384 > limit {
		for len(site.Experience.Recent) > 0 && site.Used+16384 > limit {
			site.evictExperience()
			site.Prune(now)
		}
		// Preserve aggregate traffic while rotating bounded visitor detail. A full
		// history must not disable all collection until the retention window ends.
		oldestID := ""
		oldestSeen := now
		for identity, visitor := range site.Visitors {
			if identity != event.Visitor && (oldestID == "" || visitor.Last.Before(oldestSeen)) {
				oldestID = identity
				oldestSeen = visitor.Last
			}
		}
		if oldestID != "" {
			delete(site.Visitors, oldestID)
			site.HistoryLimited = true
			site.Incomplete = true
			site.Prune(now)
		}
		if site.Used+8192 > limit {
			site.Incomplete = true
			return false
		}
	}
	date := dayKey(now)
	daily := site.Days[date]
	if daily == nil {
		daily = newDay()
		site.Days[date] = daily
		site.Used += 1024
	}
	beforeBytes := dayBytes(daily)
	defer func() { site.Used += dayBytes(daily) - beforeBytes }()
	visitor := site.Visitors[event.Visitor]
	if visitor == nil {
		visitor = &Visitor{First: now, FirstSource: event.Source, Days: map[string]*Observation{}, Pages: map[string]*Page{}, Views: map[string]*View{}, DirectDays: map[string]bool{}, Persistent: event.Persistent}
		site.Visitors[event.Visitor] = visitor
		site.Used += 4096
	}
	observation := visitor.Days[date]
	if observation == nil {
		observation = &Observation{}
		visitor.Days[date] = observation
		site.Used += 128
	}
	priorView := visitor.Views[event.View]
	fresh := priorView == nil
	if priorView != nil {
		if event.Sequence <= priorView.Sequence || event.Path != priorView.Path {
			return false
		}
		// A resumed tab after inactivity starts a new session only with a new view ID.
		// Engagement alone cannot resurrect an expired session.
		if priorView.Session != visitor.Session || now.Sub(visitor.Last) > 30*time.Minute {
			return false
		}
		priorView.Sequence = event.Sequence
		hasNewActions := false
		for _, action := range event.Actions {
			if action.Count > priorView.Actions[action.Name+"\x1f"+action.Target] {
				hasNewActions = true
			}
		}
		if event.ActiveMS <= priorView.ActiveMS && event.Scroll <= priorView.Scroll && !hasNewActions {
			return false
		}
	} else {
		if event.Sequence != 1 {
			site.Incomplete = true
			return false
		}
		if len(visitor.Views) >= 32 {
			oldestID := ""
			oldestTime := now
			for identity, view := range visitor.Views {
				if oldestID == "" || view.Seen.Before(oldestTime) {
					oldestID = identity
					oldestTime = view.Seen
				}
			}
			site.Used -= viewBytes(visitor.Views[oldestID])
			delete(visitor.Views, oldestID)
			visitor.Limited = true
			site.HistoryLimited = true
		}
		if now.Before(visitor.Last) {
			site.Incomplete = true
			return false
		}
		previousSeen := visitor.Last
		if visitor.Session == 0 || now.Sub(visitor.Last) > 30*time.Minute {
			visitor.Session++
			visitor.SessionViews = 0
			visitor.SessionDay = date
			visitor.SessionSource = event.Source
			visitor.Trail = nil
			visitor.TrailStart = now
			observation.Sessions++
			daily.Sessions++
			daily.SingleSessions++
			increment(daily.Entries, event.Path)
			if visitor.Session > 1 {
				observation.Returning = true
				gap := now.Sub(previousSeen)
				increment(daily.ReturnSources, event.Source)
				gapLabel := "30m–1h"
				if gap >= 14*24*time.Hour {
					gapLabel = "14+ days"
				} else if gap >= 7*24*time.Hour {
					gapLabel = "7–14 days"
				} else if gap >= 24*time.Hour {
					gapLabel = "1–7 days"
				} else if gap >= time.Hour {
					gapLabel = "1–24 hours"
				}
				increment(daily.ReturnIntervals, gapLabel)
				if gap >= 14*24*time.Hour {
					increment(daily.Insights, "Returned after 14 days")
				}
				if event.Source == "direct" && visitor.FirstSource != "direct" {
					visitor.DirectDays[date] = true
					if len(visitor.DirectDays) == 2 {
						increment(daily.Insights, "External discovery to direct visits")
					}
				}
			}
		}
		visitor.SessionViews++
		if visitor.SessionViews == 2 {
			if sessionDay := site.Days[visitor.SessionDay]; sessionDay != nil {
				sessionDay.SingleSessions--
			}
		}
		if visitor.SessionViews > 1 {
			increment(daily.Transitions, visitor.LastPath+" → "+event.Path)
			if sessionDay := site.Days[visitor.LastPathDay]; sessionDay != nil && sessionDay.Exits[visitor.LastPath] > 0 {
				sessionDay.Exits[visitor.LastPath]--
			}
		}
		increment(daily.Exits, event.Path)
		visitor.LastPath = event.Path
		visitor.LastPathDay = date
		daily.Views++
		observation.Views++
		increment(daily.Pages, event.Path)
		increment(daily.Sources, visitor.SessionSource)
		page := visitor.Pages[event.Path]
		if page == nil {
			if len(visitor.Pages) >= MaximumPages {
				oldestPath := ""
				oldestSeen := now
				for pathname, candidate := range visitor.Pages {
					if oldestPath == "" || candidate.LastSeen.Before(oldestSeen) {
						oldestPath = pathname
						oldestSeen = candidate.LastSeen
					}
				}
				delete(visitor.Pages, oldestPath)
				site.Used -= 768
				visitor.Limited = true
				site.HistoryLimited = true
			}
			page = &Page{}
			visitor.Pages[event.Path] = page
			site.Used += 768
			if visitor.Session > 1 {
				increment(daily.Insights, "Explored a new observed page")
			}
		}
		if page.LastDay != date {
			page.Days++
			page.RecentDays = [3]string{page.RecentDays[1], page.RecentDays[2], date}
			page.LastDay = date
		}
		if page.RecentDays[0] != "" && page.RecentDays[0] >= dayKey(now.AddDate(0, 0, -89)) && page.LastSession != visitor.Session {
			increment(daily.Insights, "Anchor page: "+event.Path)
		}
		page.LastSeen = now
		if event.Tab == "" {
			visitor.Trail = append(visitor.Trail, event.Path)
		}
		if len(visitor.Trail) > 4 {
			visitor.Trail = visitor.Trail[1:]
			visitor.TrailStart = now
		}
		if len(visitor.Trail) == 4 && now.Sub(visitor.TrailStart) <= 5*time.Minute && visitor.Trail[0] == visitor.Trail[2] && visitor.Trail[1] == visitor.Trail[3] && visitor.Trail[0] != visitor.Trail[1] {
			increment(daily.Insights, "Navigation loop")
			visitor.Trail = nil
			visitor.TrailStart = now
		}
		priorView = &View{Started: now, PreviousScroll: page.Scroll, PreviousSession: page.LastSession, Sequence: event.Sequence, Path: event.Path, Session: visitor.Session, Seen: now}
		visitor.Views[event.View] = priorView
		site.Used += 768
	}
	elapsed := now.Sub(priorView.Seen).Milliseconds() + 1000
	delta := event.ActiveMS - priorView.ActiveMS
	if delta < 0 {
		delta = 0
	}
	if delta > elapsed {
		delta = elapsed
	}
	if delta > 31000 {
		delta = 31000
	}
	viewBefore := viewBytes(priorView)
	experienceBefore := site.experienceBytes()
	site.recordExperience(event, now, visitor, priorView, fresh, delta, priorView.Scroll)
	site.Used += site.experienceBytes() - experienceBefore + viewBytes(priorView) - viewBefore
	daily.ActiveMS += delta
	observation.ActiveMS += delta
	priorView.ActiveMS = event.ActiveMS
	for index, threshold := range []int{25, 50, 75, 100} {
		if priorView.Scroll < threshold && event.Scroll >= threshold {
			daily.Scroll[index]++
		}
	}
	if page := visitor.Pages[event.Path]; page != nil {
		if !priorView.Continued && priorView.PreviousSession > 0 && priorView.PreviousSession != visitor.Session && event.Scroll >= priorView.PreviousScroll+25 && event.ActiveMS >= 10000 {
			priorView.Continued = true
			increment(daily.Insights, "Possible continued reading")
			page.LastSession = visitor.Session
		}
		if event.Scroll > page.Scroll {
			page.Scroll = event.Scroll
		}
		page.LastSession = visitor.Session
	}
	if event.Scroll > priorView.Scroll {
		priorView.Scroll = event.Scroll
	}
	priorView.Seen = now
	visitor.Last = now
	site.LastEvent = now
	return true
}

func rows(counters map[string]int) []Row {
	result := make([]Row, 0, len(counters))
	for label, count := range counters {
		if count > 0 {
			result = append(result, Row{label, count})
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Count == result[j].Count {
			return result[i].Label < result[j].Label
		}
		return result[i].Count > result[j].Count
	})
	if len(result) > 32 {
		result = result[:32]
	}
	return result
}
func merge(target, source map[string]int) {
	for label, count := range source {
		target[label] += count
	}
}

func (site *Site) Report(now time.Time, days int) Report {
	start := dayStart(now.UTC()).AddDate(0, 0, -days+1)
	report := Report{ProcessingLagMS: site.ProcessingLagMS, QueuePercent: site.QueuePercent, EstimatedBytes: site.Used, ComparisonAvailable: days*2 <= RetentionDays && !site.Started.After(start.AddDate(0, 0, -days)), PeriodStart: start, PeriodEnd: now, Generated: now, Started: site.Started, Days: days, Incomplete: site.Incomplete, HistoryLimited: site.HistoryLimited}
	pages, sources, entries, exits, transitions, insights := map[string]int{}, map[string]int{}, map[string]int{}, map[string]int{}, map[string]int{}, map[string]int{}
	activeDays, intervals := map[string]int{}, map[string]int{}
	firstSources, returnSources := map[string]int{}, map[string]int{}
	for date, daily := range site.Days {
		if date < dayKey(start) || date > dayKey(now) {
			continue
		}
		report.Views += daily.Views
		report.Sessions += daily.Sessions
		report.SingleSessions += daily.SingleSessions
		report.ActiveMS += daily.ActiveMS
		for index := range report.Scroll {
			report.Scroll[index] += daily.Scroll[index]
		}
		merge(pages, daily.Pages)
		merge(sources, daily.Sources)
		merge(entries, daily.Entries)
		merge(exits, daily.Exits)
		merge(transitions, daily.Transitions)
		merge(insights, daily.Insights)
		merge(returnSources, daily.ReturnSources)
		merge(intervals, daily.ReturnIntervals)
	}
	for _, visitor := range site.Visitors {
		active := 0
		returning := false
		for date, observation := range visitor.Days {
			if date >= dayKey(start) && date <= dayKey(now) && observation.Views > 0 {
				active++
				returning = returning || observation.Returning
			}
		}
		previousActive := false
		previousStart := start.AddDate(0, 0, -days)
		for date, observation := range visitor.Days {
			if date >= dayKey(previousStart) && date < dayKey(start) && observation.Views > 0 {
				previousActive = true
			}
		}
		if active == 0 {
			if previousActive {
				report.Dormant++
			}
			continue
		}
		if visitor.First.Before(start) {
			if previousActive {
				report.Continuing++
			} else {
				report.Resurrected++
			}
		}
		firstSources[visitor.FirstSource]++
		report.Visitors++
		if !visitor.Persistent {
			report.Temporary++
		}
		if !visitor.First.Before(start) {
			report.New++
		}
		if visitor.First.Before(start) || returning || active > 1 {
			report.Returning++
		}
		activeDays[fmt.Sprintf("%d active days", active)]++
		weeks := map[string]bool{}
		weekday := (int(now.UTC().Weekday()) + 6) % 7
		weekStart := dayStart(now.UTC()).AddDate(0, 0, -weekday)
		for date := range visitor.Days {
			parsed, _ := time.Parse("2006-01-02", date)
			if parsed.Before(weekStart) && !parsed.Before(weekStart.AddDate(0, 0, -28)) {
				weeks[fmt.Sprint(int(weekStart.Sub(parsed).Hours()/24-1)/7)] = true
			}
		}
		if len(weeks) >= 3 {
			insights["Returning habit: 3 of 4 completed weeks"]++
		}
	}
	for _, offset := range []int{1, 7, 30} {
		retention := Retention{Day: offset}
		for _, visitor := range site.Visitors {
			if !visitor.Persistent || visitor.First.Before(start) {
				continue
			}
			target := dayStart(visitor.First.UTC()).AddDate(0, 0, offset)
			if !target.Before(dayStart(now.UTC())) {
				continue
			}
			retention.Eligible++
			if observation := visitor.Days[dayKey(target)]; observation != nil && observation.Views > 0 {
				retention.Returned++
			}
		}
		report.Retention = append(report.Retention, retention)
	}
	report.Experience = site.ExperienceReport(now, days, true)
	report.Pages = rows(pages)
	report.Sources = rows(sources)
	report.FirstSources = rows(firstSources)
	report.ReturnSources = rows(returnSources)
	report.Entries = rows(entries)
	report.Exits = rows(exits)
	report.Transitions = rows(transitions)
	report.Insights = rows(insights)
	report.ActiveDays = rows(activeDays)
	report.ReturnIntervals = rows(intervals)
	return report
}

func Valid(event Event) bool {
	if len(event.Visitor) < 16 || len(event.Visitor) > 64 || len(event.View) < 16 || len(event.View) > 64 || event.Sequence < 1 || event.Sequence > 100000 || event.ActiveMS < 0 || event.ActiveMS > 24*60*60*1000 || event.Scroll < 0 || event.Scroll > 100 {
		return false
	}
	for _, identity := range []string{event.Visitor, event.View} {
		for _, character := range identity {
			if !strings.ContainsRune("0123456789abcdef-", character) {
				return false
			}
		}
	}
	if len(event.Tab) > 64 || len(event.Session) > 64 || len(event.Actions) > 16 || len(event.Language) > 32 || len(event.PageLanguage) > 32 || len(event.Timezone) > 64 {
		return false
	}
	for _, action := range event.Actions {
		if action.Name == "" || len(action.Name) > 64 || len(action.Target) > 256 || action.Count < 0 || action.Count > 10000 {
			return false
		}
	}
	for _, field := range []string{event.Campaign.Source, event.Campaign.Medium, event.Campaign.Name, event.Campaign.Content, event.Campaign.Term} {
		if len(field) > 64 {
			return false
		}
	}
	return strings.HasPrefix(event.Path, "/") && len(event.Path) <= 512 && !strings.ContainsAny(event.Path, "?#\r\n") && len(event.Source) <= 128 && len(event.Referrer) <= 256
}
