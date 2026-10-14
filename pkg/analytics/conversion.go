package analytics

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Conversion state belongs to one site worker. Contact identifiers connect
// bounded observations; sampled replay owns its separate short-lived budget.
const MaximumSignals = 20
const MaximumBehaviourElements = 200
const FunnelWindow = 90 * 24 * time.Hour
const configurationBudget int64 = 16384
const progressBudget int64 = 4096
const pathBudget int64 = 2048
const behaviourBudget int64 = 1024
const formBudget int64 = 2048
const deliveryBudget int64 = 768

type Signal struct {
	Kind    string `json:"kind"`
	Name    string `json:"name,omitempty"`
	Element string `json:"element,omitempty"`
	Form    string `json:"form,omitempty"`
	Field   string `json:"field,omitempty"`
	Scroll  int    `json:"scroll,omitempty"`
	Filled  bool   `json:"filled,omitempty"`
	At      int64  `json:"at"`
}
type FunnelProgress struct {
	Device   string
	Landing  string
	Entered  time.Time
	Reached  []time.Time
	Source   Attribution
	Referrer string
	Campaign Campaign
}
type ConversionVisitor struct {
	Device                      string
	Location                    VisitorLocation
	Deliveries                  map[string]*View
	First, Last                 time.Time
	Persistent                  bool
	ReturnedWeeks               uint16
	Acquisition                 Attribution
	Landing, Referrer, Campaign string
	Days                        map[string]bool
	Funnels                     map[string]*FunnelProgress
	Tabs                        map[string]*PathContact
	Paths                       map[string]*ObservedPath
	Behaviour                   map[string]*ElementObservation
	Forms                       map[string]*FormObservation
}
type PathContact struct {
	Session string
	Last    time.Time
	Steps   []string
}
type ObservedPath struct {
	Date, Root, Trail string
	At                time.Time
	Exit, Entry       bool
}
type ElementObservation struct {
	FirstAt, LastAt                           time.Time
	Date, Page, Layout, Device, Element, Kind string
	Count                                     int
	Depth                                     int
}
type FormObservation struct {
	StartedAt, SubmittedAt, CompletedAt, FailedAt time.Time
	Date, Page, Form, Session, LastField          string
	Last                                          time.Time
	Viewed, Started, Submitted, Completed, Failed bool
}
type ConversionState struct {
	Replay            *ReplayState
	Changes           []PageChange
	Version           int
	Visitors          map[string]*ConversionVisitor
	Configurations    map[string]Goal
	Daily             map[string]ConversionDaily
	HourlyConversions map[string]int
	Elements          map[string]map[string]bool
	Cohorts           map[string]*CohortAggregate
	Used              int64
	Incomplete        bool
}
type ConversionDaily struct{ Visitors, Conversions int }
type CohortAggregate struct {
	Visitors int
	Returned [12]int
}
type ConversionStep struct {
	Name                 string
	Users, Pending, Lost int
	Transition, Drop     string
}
type ConversionFunnel struct {
	Key, Name                           string
	Primary                             bool
	Users, Completed                    int
	Rate                                string
	Steps                               []ConversionStep
	BiggestFrom, BiggestTo, BiggestDrop string
	BiggestPending                      int
}
type ConversionDevice struct {
	Name                string
	Visitors, Completed int
}
type ConversionSource struct {
	Name, Kind, Referrer, Landing, Campaign string
	Visitors, Completed                     int
	Rate                                    string
}
type ConversionPath struct {
	Root, Trail, Label                 string
	Users, Completed, Exits, Entrances int
	Share, Conversion, ExitRate        string
	Children                           []*ConversionPath
}
type BehaviourRow struct {
	Element, Kind string
	Users, Clicks int
	Rate          string
	Depth         int
}
type BehaviourPage struct {
	Page, Layout, Device       string
	Visitors                   int
	Elements, Scroll, Sections []BehaviourRow
}
type ConversionForm struct {
	Page, Form                                               string
	Viewed, Started, Submitted, Completed, Failed, Abandoned int
	Fields                                                   []Row
}
type CohortWeek struct {
	Eligible, Returned int
	Rate               string
	Partial            bool
}
type ConversionCohort struct {
	Week     string
	Visitors int
	Weeks    []CohortWeek
}
type ConversionInsight struct {
	Kind, Subject, Compared string
	Users                   int
	Rate, Factor            string
}
type VisitorLocation struct {
	Address, Country, City, Source, Landing, Device string
	Latitude, Longitude                             float64
	Known                                           bool
	CheckedAt, Last                                 time.Time
	Days                                            []string
}
type ConversionDay struct {
	Date     string
	Visitors int
}
type ConversionReport struct {
	Sessions            []ReplaySummary
	Impacts             []PageImpact
	Devices             []ConversionDevice
	Locations           []VisitorLocation
	Daily               []ConversionDay
	UnknownLocations    int
	Version             int
	Visitors, Temporary int
	Primary             string
	Funnels             []ConversionFunnel
	Sources             []ConversionSource
	Referrers           []ConversionSource
	Paths               []*ConversionPath
	Behaviour           []BehaviourPage
	Forms               []ConversionForm
	Cohorts             []ConversionCohort
	Insights            []ConversionInsight
	Incomplete          bool
}

func goalVersion(goal Goal) string {
	normalized := Goal{Name: goal.Name, Steps: append([]Goal(nil), goalSteps(goal)...)}
	for index := range normalized.Steps {
		normalized.Steps[index].Primary = false
		if normalized.Steps[index].Kind != "scroll" {
			normalized.Steps[index].Threshold = 0
		}
	}
	encoded, _ := json.Marshal(normalized)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:8])
}
func GoalKey(goal Goal) string { return goalVersion(goal) }
func goalSteps(goal Goal) []Goal {
	steps := goal.Steps
	if len(steps) == 0 {
		steps = []Goal{{Name: goal.Name, Kind: goal.Kind, Match: goal.Match, Page: goal.Page, Element: goal.Element, Threshold: goal.Threshold, SuccessPage: goal.SuccessPage}}
	}
	// Every configured outcome has an acquisition denominator, including custom
	// business events that would otherwise misleadingly show 100% conversion.
	if steps[0].Kind != "page" || steps[0].Page != "" || steps[0].Element != "" {
		steps = append([]Goal{{Name: "Visitors", Kind: "page"}}, steps...)
	}
	return steps
}
func safeStructuralKey(key string) bool {
	if len(key) == 0 || len(key) > 180 {
		return false
	}
	if strings.HasPrefix(key, "key:") {
		return safeEventName(strings.TrimPrefix(key, "key:"))
	}
	for _, part := range strings.Split(key, ">") {
		halves := strings.Split(part, ":")
		if len(halves) != 2 || len(halves[0]) > 20 {
			return false
		}
		for _, letter := range halves[0] {
			if letter < 'a' || letter > 'z' {
				return false
			}
		}
		ordinal, err := strconv.Atoi(halves[1])
		if err != nil || ordinal < 1 || ordinal > 10000 {
			return false
		}
	}
	return true
}
func safeEventName(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	for _, letter := range name {
		if !(letter >= 'a' && letter <= 'z' || letter >= 'A' && letter <= 'Z' || letter >= '0' && letter <= '9' || letter == '_' || letter == '-') {
			return false
		}
	}
	return true
}
func ValidSignals(event Event) bool {
	if len(event.Signals) > MaximumSignals || len(event.Layout) > 32 || (event.Device != "" && event.Device != "mobile" && event.Device != "tablet" && event.Device != "desktop") {
		return false
	}
	for _, letter := range event.Layout {
		if !(letter >= '0' && letter <= '9' || letter >= 'a' && letter <= 'f') {
			return false
		}
	}
	previous := int64(0)
	for _, signal := range event.Signals {
		switch signal.Kind {
		case "page", "click", "scroll", "download", "form-view", "form-start", "form-submit", "form-success", "form-failed", "field", "external-link", "goal", "rage-click", "dead-click", "section":
		default:
			return false
		}
		if signal.Name != "" && !safeEventName(signal.Name) || signal.Element != "" && !safeStructuralKey(signal.Element) || signal.Form != "" && !safeStructuralKey(signal.Form) || signal.Field != "" && !safeStructuralKey(signal.Field) || signal.Scroll < 0 || signal.Scroll > 100 || signal.At < previous || signal.At > 24*60*60*1000 {
			return false
		}
		previous = signal.At
	}
	return true
}
func stepMatches(step Goal, event Event, signal Signal) bool {
	if step.Page != "" && step.Page != event.Path && !(step.Kind == "form-success" && step.SuccessPage == event.Path) {
		return false
	}
	if step.Element != "" && step.Element != signal.Element && step.Element != signal.Form {
		return false
	}
	switch step.Kind {
	case "path", "uri":
		return signal.Kind == "goal" && signal.Name == URIEventName(step.Match) || signal.Kind == "page" && (step.Match == event.Path || step.Match == SafeURI(event.URI))
	case "action":
		return (signal.Kind == "goal" || signal.Kind == "download" || signal.Kind == "external-link") && step.Match == signal.Name
	case "scroll":
		return signal.Kind == "scroll" && signal.Scroll >= step.Threshold
	case "event":
		return signal.Kind == "goal" && signal.Name == step.Match
	default:
		return signal.Kind == step.Kind && (step.Match == "" || step.Match == signal.Name)
	}
}
func (site *Site) recordConversion(event Event, now time.Time, limit int64) bool {
	if site.Conversion == nil {
		site.Experience = Experience{}
		site.Visitors = map[string]*Visitor{}
		site.Days = map[string]*Day{}
		site.Conversion = &ConversionState{Version: 2, Visitors: map[string]*ConversionVisitor{}, Configurations: map[string]Goal{}, Daily: map[string]ConversionDaily{}, HourlyConversions: map[string]int{}}
	}
	state := site.Conversion
	if event.Limited {
		state.Incomplete = true
		site.Incomplete = true
	}
	if dayKey(site.LastEvent) != dayKey(now) {
		site.pruneConversion(now)
	}
	// Admission never discards accepted identities to make room for later traffic.
	reserve := int64(32768 + len(event.Signals)*16384)
	for _, goal := range site.Goals {
		key := goalVersion(goal)
		if _, known := state.Configurations[key]; !known {
			reserve += configurationBudget
		}
		if candidate := state.Visitors[event.Visitor]; candidate == nil || candidate.Funnels[key] == nil {
			reserve += progressBudget
		}
	}
	if state.Used+reserve > limit {
		state.Incomplete = true
		site.Incomplete = true
		return false
	}
	if state.Elements == nil {
		state.Elements = map[string]map[string]bool{}
	}
	if state.Cohorts == nil {
		state.Cohorts = map[string]*CohortAggregate{}
	}
	visitor := state.Visitors[event.Visitor]
	if visitor == nil {
		visitor = &ConversionVisitor{First: now, Persistent: event.Persistent, Acquisition: event.Attribution, Landing: event.Path, Referrer: event.Referrer, Campaign: event.Campaign.Name, Days: map[string]bool{}, Funnels: map[string]*FunnelProgress{}, Tabs: map[string]*PathContact{}, Paths: map[string]*ObservedPath{}, Behaviour: map[string]*ElementObservation{}, Forms: map[string]*FormObservation{}}
		state.Visitors[event.Visitor] = visitor
		if visitor.Persistent {
			week := dayKey(weekStart(now))
			cohort := state.Cohorts[week]
			if cohort == nil {
				cohort = &CohortAggregate{}
				state.Cohorts[week] = cohort
			}
			cohort.Visitors++
		}
		state.Used += 2048
	}
	// Batch sequences are checked before any aggregates change, including beacon retries.
	if visitorSequences(site, event, now) == false {
		return false
	}
	if event.Address != "" {
		if event.Location.Address == event.Address {
			visitor.Location = event.Location
		} else if visitor.Location.Address != event.Address {
			visitor.Location = VisitorLocation{Address: event.Address}
		}
		visitor.Location.Last = now
	}
	if event.Device != "" {
		visitor.Device = event.Device
	}
	visitor.Last = now
	if visitor.Persistent {
		weekIndex := int(weekStart(now).Sub(weekStart(visitor.First)).Hours()/168) - 1
		if weekIndex >= 0 && weekIndex < 12 && visitor.ReturnedWeeks&(1<<weekIndex) == 0 {
			visitor.ReturnedWeeks |= 1 << weekIndex
			if cohort := state.Cohorts[dayKey(weekStart(visitor.First))]; cohort != nil {
				cohort.Returned[weekIndex]++
			}
		}
	}
	date := dayKey(now)
	if !visitor.Days[date] {
		visitor.Days[date] = true
		daily := state.Daily[date]
		daily.Visitors++
		state.Daily[date] = daily
		state.Used += 64
	}
	for _, goal := range site.Goals {
		key := goalVersion(goal)
		if _, exists := state.Configurations[key]; !exists {
			if len(state.Configurations) >= 128 {
				state.Incomplete = true
				continue
			}
			configuration := goal
			configuration.Steps = append([]Goal(nil), goal.Steps...)
			state.Configurations[key] = configuration
			state.Used += configurationBudget
		}
	}
	contactKey := event.Tab
	if contactKey == "" {
		contactKey = "default"
	}
	contact := visitor.Tabs[contactKey]
	freshPage := false
	for _, signal := range event.Signals {
		if signal.Kind == "page" {
			freshPage = true
			break
		}
	}
	if freshPage {
		if contact == nil || contact.Session != event.Session || now.Sub(contact.Last) > 30*time.Minute {
			if contact != nil {
				closePathContact(visitor, contact)
			}
			if contact == nil && len(visitor.Tabs) >= 4 {
				state.Incomplete = true
			} else {
				contact = &PathContact{Session: event.Session}
				visitor.Tabs[contactKey] = contact
				state.Used += 512
			}
		}
		if contact != nil {
			contact.Last = now
			contact.Steps = append(contact.Steps, event.Path)
			if len(contact.Steps) > 4 {
				contact.Steps = contact.Steps[1:]
			}
			for start := 0; start < len(contact.Steps); start++ {
				trail := strings.Join(contact.Steps[start:], " → ")
				pathKey := date + "\x1f" + trail
				if visitor.Paths[pathKey] == nil {
					if len(visitor.Paths) >= 128 {
						state.Incomplete = true
						break
					}
					visitor.Paths[pathKey] = &ObservedPath{Date: date, Root: contact.Steps[start], Trail: trail, At: now}
					state.Used += pathBudget
				}
				if start == 0 && len(contact.Steps) == 1 {
					visitor.Paths[pathKey].Entry = true
				}
			}
		}
	} else if contact != nil {
		contact.Last = now
	}
	signals := append([]Signal(nil), event.Signals...)
	if freshPage {
		for _, goal := range site.Goals {
			for _, step := range goalSteps(goal) {
				if step.Kind == "form-success" && step.SuccessPage == event.Path && step.Element != "" {
					for _, attempt := range visitor.Forms {
						if attempt.Form == step.Element && attempt.Session == event.Session && (attempt.Started || attempt.Submitted) && now.Sub(attempt.Last) <= 30*time.Minute {
							signals = append(signals, Signal{Kind: "form-success", Form: step.Element, At: event.Signals[len(event.Signals)-1].At})
							break
						}
					}
				}
			}
		}
	}
	for _, signal := range signals {
		occurred := now
		// Relative event times preserve ordering inside a batch without trusting a wall clock.
		if len(event.Signals) > 0 {
			occurred = now.Add(time.Duration(signal.At-event.Signals[len(event.Signals)-1].At) * time.Millisecond)
		}
		for _, goal := range site.Goals {
			key := goalVersion(goal)
			if _, known := state.Configurations[key]; !known {
				continue
			}
			steps := goalSteps(goal)
			progress := visitor.Funnels[key]
			initialized := progress == nil
			if progress == nil {
				if !stepMatches(steps[0], event, signal) {
					continue
				}
				progress = &FunnelProgress{Device: event.Device, Entered: occurred, Reached: []time.Time{occurred}, Source: event.Attribution, Referrer: event.Referrer, Campaign: event.Campaign, Landing: event.Path}
				visitor.Funnels[key] = progress
				state.Used += progressBudget
				if len(steps) == 1 {
					daily := state.Daily[date]
					daily.Conversions++
					state.Daily[date] = daily
					if state.HourlyConversions == nil {
						state.HourlyConversions = map[string]int{}
					}
					state.HourlyConversions[now.UTC().Format("2006-01-02T15")]++
				}
			}
			if len(progress.Reached) < len(steps) && (!initialized || (steps[0].Kind == "page" && steps[0].Page == "" && steps[0].Element == "")) && occurred.Sub(progress.Entered) <= FunnelWindow && stepMatches(steps[len(progress.Reached)], event, signal) {
				progress.Reached = append(progress.Reached, occurred)
				if len(progress.Reached) == len(steps) {
					daily := state.Daily[date]
					daily.Conversions++
					state.Daily[date] = daily
					if state.HourlyConversions == nil {
						state.HourlyConversions = map[string]int{}
					}
					state.HourlyConversions[now.UTC().Format("2006-01-02T15")]++
				}
			}
		}
		switch signal.Kind {
		case "page", "click", "rage-click", "dead-click", "scroll", "section":
			kind := signal.Kind
			element := signal.Element
			if kind == "page" {
				element = ""
			}
			if kind == "scroll" {
				element = ""
			}
			key := strings.Join([]string{date, event.Path, event.Layout, event.Device, kind, element}, "\x1f")
			observation := visitor.Behaviour[key]
			if observation == nil {
				pageKey := event.Path + "\x1f" + event.Layout + "\x1f" + event.Device
				elements := state.Elements[pageKey]
				if elements == nil {
					if len(state.Elements) >= 128 {
						state.Incomplete = true
						continue
					}
					elements = map[string]bool{}
					state.Elements[pageKey] = elements
					state.Used += 1024
				}
				if element != "" && !elements[element] {
					if len(elements) >= MaximumBehaviourElements {
						state.Incomplete = true
						continue
					}
					elements[element] = true
					state.Used += 512
				}
				observation = &ElementObservation{Date: date, Page: event.Path, Layout: event.Layout, Device: event.Device, Kind: kind, Element: element}
				visitor.Behaviour[key] = observation
				state.Used += behaviourBudget
			}
			if observation.FirstAt.IsZero() {
				observation.FirstAt = occurred
			}
			observation.LastAt = occurred
			observation.Count++
			observation.Depth = max(observation.Depth, signal.Scroll)
		case "form-view", "form-start", "form-submit", "form-success", "form-failed", "field":
			if signal.Form == "" {
				continue
			}
			key := event.Session + "\x1f" + event.Path + "\x1f" + signal.Form
			observation := visitor.Forms[key]
			if signal.Kind == "form-success" || signal.Kind == "form-failed" {
				for _, attempt := range visitor.Forms {
					if attempt.Form == signal.Form && attempt.Session == event.Session && (observation == nil || attempt.Last.After(observation.Last)) {
						observation = attempt
					}
				}
				if observation == nil {
					continue
				}
			}
			if observation == nil {
				if len(visitor.Forms) >= 32 {
					state.Incomplete = true
					continue
				}
				observation = &FormObservation{Date: date, Page: event.Path, Form: signal.Form, Session: event.Session}
				visitor.Forms[key] = observation
				state.Used += formBudget
			}
			observation.Last = now
			switch signal.Kind {
			case "form-view":
				observation.Viewed = true
			case "form-start":
				observation.Started = true
				if observation.StartedAt.IsZero() {
					observation.StartedAt = occurred
				}
			case "form-submit":
				observation.Submitted = true
				if observation.SubmittedAt.IsZero() {
					observation.SubmittedAt = occurred
				}
			case "form-success":
				observation.Completed = true
				if observation.CompletedAt.IsZero() {
					observation.CompletedAt = occurred
				}
			case "form-failed":
				observation.Failed = true
				if observation.FailedAt.IsZero() {
					observation.FailedAt = occurred
				}
			case "field":
				observation.LastField = signal.Field
			}
		}
	}
	site.recordReplay(event, now, limit)
	site.Used = state.Used
	site.LastEvent = now
	return true
}

// Reuse the bounded view sequence gate without retaining a second raw stream.
func visitorSequences(site *Site, event Event, now time.Time) bool {
	visitor := site.Conversion.Visitors[event.Visitor]
	if visitor.Deliveries == nil {
		visitor.Deliveries = map[string]*View{}
	}
	view := visitor.Deliveries[event.View]
	if view != nil && event.Sequence <= view.Sequence {
		return false
	}
	if view == nil {
		if len(visitor.Deliveries) >= 32 {
			site.Conversion.Incomplete = true
			return false
		}
		if event.Sequence != 1 {
			site.Conversion.Incomplete = true
		}
		view = &View{Path: event.Path}
		visitor.Deliveries[event.View] = view
		site.Conversion.Used += deliveryBudget
	}
	if view.Path != event.Path {
		return false
	}
	view.Sequence = event.Sequence
	view.Seen = now
	return true
}
func closePathContact(visitor *ConversionVisitor, contact *PathContact) {
	for _, path := range visitor.Paths {
		for start := 0; start < len(contact.Steps); start++ {
			if path.Trail == strings.Join(contact.Steps[start:], " → ") {
				path.Exit = true
			}
		}
	}
}
func (site *Site) pruneConversion(now time.Time) {
	state := site.Conversion
	if state == nil {
		return
	}
	for index := range state.Changes {
		if state.Changes[index].Result == nil && !now.Before(state.Changes[index].At.Add(2*ImpactWindow)) {
			impact := pageImpact(state, state.Changes[index], now)
			state.Changes[index].Result = &impact
		}
	}
	state.Used = 1024 + int64(len(state.Changes))*1024
	for identity, visitor := range state.Visitors {
		if now.Sub(visitor.Last) > FunnelWindow && !hasOpenFunnel(visitor, now) {
			delete(state.Visitors, identity)
			delete(site.Visitors, identity)
			continue
		}
		state.Used += 2048
		for date := range visitor.Days {
			if date < dayKey(now.AddDate(0, 0, -89)) {
				delete(visitor.Days, date)
			} else {
				state.Used += 64
			}
		}
		for key, contact := range visitor.Tabs {
			if now.Sub(contact.Last) > 30*time.Minute {
				closePathContact(visitor, contact)
				delete(visitor.Tabs, key)
			} else {
				state.Used += 512
			}
		}
		for key, path := range visitor.Paths {
			if path.Date < dayKey(now.AddDate(0, 0, -89)) {
				delete(visitor.Paths, key)
			} else {
				state.Used += pathBudget
			}
		}
		for key, observation := range visitor.Behaviour {
			if observation.Date < dayKey(now.AddDate(0, 0, -89)) {
				delete(visitor.Behaviour, key)
			} else {
				state.Used += behaviourBudget
			}
		}
		for key, form := range visitor.Forms {
			if now.Sub(form.Last) > 7*24*time.Hour {
				form.Session = ""
			}
			if form.Date < dayKey(now.AddDate(0, 0, -89)) {
				delete(visitor.Forms, key)
			} else {
				state.Used += formBudget
			}
		}
		state.Used += int64(len(visitor.Funnels)) * progressBudget
		for key, view := range visitor.Deliveries {
			if now.Sub(view.Seen) > 24*time.Hour {
				delete(visitor.Deliveries, key)
			} else {
				state.Used += deliveryBudget
			}
		}

	}
	for _, elements := range state.Elements {
		state.Used += 1024 + int64(len(elements))*512
	}
	state.Used += int64(len(state.Configurations)) * configurationBudget
	for week := range state.Cohorts {
		if week < dayKey(now.AddDate(0, -13, 0)) {
			delete(state.Cohorts, week)
		} else {
			state.Used += 256
		}
	}
	for date := range state.Daily {
		if date < dayKey(now.AddDate(0, -13, 0)) {
			delete(state.Daily, date)
		} else {
			state.Used += 64
		}
	}
	for hour := range state.HourlyConversions {
		if len(hour) < 10 || hour[:10] < dayKey(now.AddDate(0, 0, -29)) {
			delete(state.HourlyConversions, hour)
		} else {
			state.Used += 64
		}
	}
	site.PruneReplay(now)
	site.Used = state.Used
}
func percentage(numerator, denominator int) string {
	if denominator <= 0 {
		return "—"
	}
	return fmt.Sprintf("%.1f%%", 100*float64(numerator)/float64(denominator))
}
func weekStart(now time.Time) time.Time {
	date := dayStart(now.UTC())
	return date.AddDate(0, 0, -(int(date.Weekday())+6)%7)
}

// Reports read owned state. Visitor metadata serves the authenticated map;
// long-lived daily archives remove visitor metadata.
func (site *Site) conversionReport(now time.Time, days int) ConversionReport {
	result := ConversionReport{Version: 2, Incomplete: site.Incomplete}
	state := site.Conversion
	if state == nil {
		return result
	}
	result.Incomplete = result.Incomplete || state.Incomplete
	start := dayStart(now.UTC()).AddDate(0, 0, -days+1)
	goals := append([]Goal(nil), site.Goals...)
	primary := ""
	for _, goal := range goals {
		if goal.Primary {
			primary = goalVersion(goal)
			break
		}
	}
	if primary == "" && len(goals) > 0 {
		primary = goalVersion(goals[0])
	}
	result.Primary = primary
	configurations := map[string]Goal{}
	for key, goal := range state.Configurations {
		configurations[key] = goal
	}
	for _, goal := range goals {
		configurations[goalVersion(goal)] = goal
	}
	keys := make([]string, 0, len(configurations))
	for key := range configurations {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	sources := map[string]*ConversionSource{}
	devices := map[string]*ConversionDevice{}
	for _, key := range keys {
		goal := configurations[key]
		steps := goalSteps(goal)
		funnel := ConversionFunnel{Key: key, Name: goal.Name, Primary: key == primary}
		for _, step := range steps {
			funnel.Steps = append(funnel.Steps, ConversionStep{Name: step.Name})
		}
		for _, visitor := range state.Visitors {
			progress := visitor.Funnels[key]
			if progress == nil || progress.Entered.Before(start) || progress.Entered.After(now) {
				continue
			}
			funnel.Users++
			reached := 0
			for _, stamp := range progress.Reached {
				if !stamp.After(now) {
					reached++
				}
			}
			completed := reached == len(steps)
			if completed {
				funnel.Completed++
			}
			for index := range funnel.Steps {
				if index >= reached {
					continue
				}
				funnel.Steps[index].Users++
				if index < len(steps)-1 && index == reached-1 {
					if now.Sub(progress.Entered) < FunnelWindow {
						funnel.Steps[index].Pending++
					} else {
						funnel.Steps[index].Lost++
					}
				}
			}
			if key == primary {
				if devices[progress.Device] == nil {
					devices[progress.Device] = &ConversionDevice{Name: progress.Device}
				}
				devices[progress.Device].Visitors++
				if completed {
					devices[progress.Device].Completed++
				}
				sourceKey := strings.Join([]string{progress.Source.Name, progress.Source.Kind, progress.Referrer, progress.Campaign.Source, progress.Campaign.Name, progress.Landing}, "\x1f")
				source := sources[sourceKey]
				if source == nil {
					source = &ConversionSource{Name: progress.Source.Name, Kind: progress.Source.Kind, Referrer: progress.Referrer, Campaign: progress.Campaign.Name, Landing: progress.Landing}
					sources[sourceKey] = source
				}
				if source.Name == "" {
					source.Name = "direct"
				}
				source.Visitors++
				if completed {
					source.Completed++
				}
			}
		}
		biggest := -1
		for index := range funnel.Steps {
			step := &funnel.Steps[index]
			if index == 0 {
				step.Transition = percentage(step.Users, step.Users)
			} else {
				step.Transition = percentage(step.Users, funnel.Steps[index-1].Users)
			}
			if index+1 < len(funnel.Steps) {
				missing := step.Users - funnel.Steps[index+1].Users
				step.Drop = percentage(missing, step.Users)
				if missing > 0 && step.Users > 0 && (biggest < 0 || float64(missing)/float64(step.Users) > float64(funnel.Steps[biggest].Users-funnel.Steps[biggest+1].Users)/float64(funnel.Steps[biggest].Users)) {
					biggest = index
				}
			}
		}
		if biggest >= 0 {
			funnel.BiggestFrom = funnel.Steps[biggest].Name
			funnel.BiggestTo = funnel.Steps[biggest+1].Name
			funnel.BiggestDrop = funnel.Steps[biggest].Drop
			funnel.BiggestPending = funnel.Steps[biggest].Pending
		}
		funnel.Rate = percentage(funnel.Completed, funnel.Users)
		result.Funnels = append(result.Funnels, funnel)
	}
	sort.SliceStable(result.Funnels, func(i, j int) bool {
		if result.Funnels[i].Primary != result.Funnels[j].Primary {
			return result.Funnels[i].Primary
		}
		return result.Funnels[i].Name+result.Funnels[i].Key < result.Funnels[j].Name+result.Funnels[j].Key
	})
	if primary == "" {
		for _, visitor := range state.Visitors {
			active := false
			for date := range visitor.Days {
				if date >= dayKey(start) && date <= dayKey(now) {
					active = true
					break
				}
			}
			if !active {
				continue
			}
			key := visitor.Acquisition.Name + "\x1f" + visitor.Acquisition.Kind + "\x1f" + visitor.Referrer + "\x1f" + visitor.Campaign + "\x1f" + visitor.Landing
			source := sources[key]
			if source == nil {
				source = &ConversionSource{Name: visitor.Acquisition.Name, Kind: visitor.Acquisition.Kind, Campaign: visitor.Campaign, Referrer: visitor.Referrer, Landing: visitor.Landing}
				sources[key] = source
			}
			source.Visitors++
		}
	}
	qualitySources := map[string]*ConversionSource{}
	for _, source := range sources {
		source.Rate = percentage(source.Completed, source.Visitors)
		if primary == "" {
			source.Rate = "—"
		}
		result.Referrers = append(result.Referrers, *source)
		key := source.Name + "\x1f" + source.Kind + "\x1f" + source.Campaign
		if qualitySources[key] == nil {
			qualitySources[key] = &ConversionSource{Name: source.Name, Kind: source.Kind, Campaign: source.Campaign}
		}
		qualitySources[key].Visitors += source.Visitors
		qualitySources[key].Completed += source.Completed

	}
	for _, source := range qualitySources {
		source.Rate = percentage(source.Completed, source.Visitors)
		if primary == "" {
			source.Rate = "—"
		}
		result.Sources = append(result.Sources, *source)
	}
	sort.Slice(result.Referrers, func(left, right int) bool {
		if result.Referrers[left].Visitors != result.Referrers[right].Visitors {
			return result.Referrers[left].Visitors > result.Referrers[right].Visitors
		}
		return result.Referrers[left].Referrer+result.Referrers[left].Landing < result.Referrers[right].Referrer+result.Referrers[right].Landing
	})
	sort.Slice(result.Sources, func(i, j int) bool {
		if result.Sources[i].Visitors != result.Sources[j].Visitors {
			return result.Sources[i].Visitors > result.Sources[j].Visitors
		}
		return result.Sources[i].Name < result.Sources[j].Name
	})
	paths := map[string]*ConversionPath{}
	behaviour := map[string]*BehaviourPage{}
	elementRows := map[string]map[string]*BehaviourRow{}
	forms := map[string]*ConversionForm{}
	formFields := map[string]map[string]int{}
	for _, visitor := range state.Visitors {
		active := false
		for date := range visitor.Days {
			if date >= dayKey(start) && date <= dayKey(now) {
				active = true
				break
			}
		}
		if active {
			location := visitor.Location
			location.Source = visitor.Acquisition.Name
			location.Landing = visitor.Landing
			location.Device = visitor.Device
			for date := range visitor.Days {
				if date >= dayKey(start) && date <= dayKey(now) {
					location.Days = append(location.Days, date)
				}
			}
			sort.Strings(location.Days)
			if !location.Known {
				result.UnknownLocations++
			}
			result.Locations = append(result.Locations, location)
			result.Visitors++
			if !visitor.Persistent {
				result.Temporary++
			}
		}
		progress := visitor.Funnels[primary]
		convertedAt := time.Time{}
		if progress != nil && len(progress.Reached) == len(goalSteps(configurations[primary])) {
			convertedAt = progress.Reached[len(progress.Reached)-1]
		}
		seenPaths := map[string]bool{}
		entryPaths := map[string]bool{}
		exitedPaths := map[string]bool{}
		for _, path := range visitor.Paths {
			if path.Date < dayKey(start) || path.At.After(now) {
				continue
			}
			node := paths[path.Trail]
			if node == nil {
				parts := strings.Split(path.Trail, " → ")
				node = &ConversionPath{Root: path.Root, Trail: path.Trail, Label: parts[len(parts)-1]}
				paths[path.Trail] = node
			}
			if path.Entry && !entryPaths[path.Trail] {
				node.Entrances++
				entryPaths[path.Trail] = true
			}
			if !seenPaths[path.Trail] {
				node.Users++
				if !convertedAt.IsZero() && !convertedAt.Before(path.At) {
					node.Completed++
				}
				seenPaths[path.Trail] = true
			}
			exited := path.Exit
			for _, contact := range visitor.Tabs {
				if now.Sub(contact.Last) > 30*time.Minute {
					for index := range contact.Steps {
						if path.Trail == strings.Join(contact.Steps[index:], " → ") {
							exited = true
						}
					}
				}
			}
			if exited && !exitedPaths[path.Trail] {
				node.Exits++
				exitedPaths[path.Trail] = true
			}
		}
		seenRows := map[string]bool{}
		pageDepth := map[string]int{}
		seenPages := map[string]bool{}
		for _, observation := range visitor.Behaviour {
			if observation.Date < dayKey(start) || observation.Date > dayKey(now) {
				continue
			}
			pageKey := strings.Join([]string{observation.Page, observation.Layout, observation.Device}, "\x1f")
			page := behaviour[pageKey]
			if page == nil {
				page = &BehaviourPage{Page: observation.Page, Layout: observation.Layout, Device: observation.Device}
				behaviour[pageKey] = page
				elementRows[pageKey] = map[string]*BehaviourRow{}
			}
			if observation.Kind == "page" {
				if !seenPages[pageKey] {
					page.Visitors++
					seenPages[pageKey] = true
				}
				continue
			}
			if observation.Kind == "scroll" {
				pageDepth[pageKey] = max(pageDepth[pageKey], observation.Depth)
				continue
			}
			rowKey := observation.Kind + "\x1f" + observation.Element
			row := elementRows[pageKey][rowKey]
			if row == nil {
				row = &BehaviourRow{Element: observation.Element, Kind: observation.Kind}
				elementRows[pageKey][rowKey] = row
			}
			row.Clicks += observation.Count
			if !seenRows[pageKey+rowKey] {
				row.Users++
				seenRows[pageKey+rowKey] = true
			}
		}
		for pageKey, depth := range pageDepth {
			page := behaviour[pageKey]
			if page.Scroll == nil {
				for threshold := 10; threshold <= 100; threshold += 10 {
					page.Scroll = append(page.Scroll, BehaviourRow{Depth: threshold})
				}
			}
			for index := range page.Scroll {
				if depth >= page.Scroll[index].Depth {
					page.Scroll[index].Users++
				}
			}
		}
		seenForms := map[string]FormObservation{}
		for _, observation := range visitor.Forms {
			if observation.Date < dayKey(start) || observation.Date > dayKey(now) {
				continue
			}
			formKey := observation.Page + "\x1f" + observation.Form
			merged := seenForms[formKey]
			merged.Viewed = merged.Viewed || observation.Viewed
			merged.Started = merged.Started || observation.Started && !observation.StartedAt.After(now)
			merged.Submitted = merged.Submitted || observation.Submitted && !observation.SubmittedAt.After(now)
			merged.Completed = merged.Completed || observation.Completed && !observation.CompletedAt.After(now)
			merged.Failed = merged.Failed || observation.Failed && !observation.FailedAt.After(now)
			if observation.Last.After(merged.Last) {
				merged.Last = observation.Last
				merged.LastField = observation.LastField
			}
			seenForms[formKey] = merged
		}
		for formKey, observation := range seenForms {
			form := forms[formKey]
			if form == nil {
				parts := strings.Split(formKey, "\x1f")
				form = &ConversionForm{Page: parts[0], Form: parts[1]}
				forms[formKey] = form
				formFields[formKey] = map[string]int{}
			}
			if observation.Viewed {
				form.Viewed++
			}
			if observation.Started {
				form.Started++
			}
			if observation.Submitted {
				form.Submitted++
			}
			if observation.Completed {
				form.Completed++
			}
			if observation.Failed {
				form.Failed++
			}
			if observation.Started && !observation.Completed && now.Sub(observation.Last) > 30*time.Minute {
				form.Abandoned++
				if observation.LastField != "" {
					formFields[formKey][observation.LastField]++
				}
			}
		}

	}
	for _, node := range paths {
		node.Conversion = percentage(node.Completed, node.Users)
		if primary == "" {
			node.Conversion = "—"
		}
		node.ExitRate = percentage(node.Exits, node.Users)
		parts := strings.Split(node.Trail, " → ")
		if len(parts) == 1 {
			node.Share = "100.0%"
			result.Paths = append(result.Paths, node)
		} else if parent := paths[strings.Join(parts[:len(parts)-1], " → ")]; parent != nil {
			node.Share = percentage(node.Users, parent.Users)
			parent.Children = append(parent.Children, node)
		}
	}
	sortPaths(result.Paths)
	pageKeys := make([]string, 0, len(behaviour))
	for key := range behaviour {
		pageKeys = append(pageKeys, key)
	}
	sort.Strings(pageKeys)
	for _, key := range pageKeys {
		page := behaviour[key]
		for _, row := range elementRows[key] {
			row.Rate = percentage(row.Users, page.Visitors)
			if row.Kind == "section" {
				page.Sections = append(page.Sections, *row)
			} else {
				page.Elements = append(page.Elements, *row)
			}
		}
		sort.Slice(page.Elements, func(i, j int) bool {
			if page.Elements[i].Clicks != page.Elements[j].Clicks {
				return page.Elements[i].Clicks > page.Elements[j].Clicks
			}
			return page.Elements[i].Element+page.Elements[i].Kind < page.Elements[j].Element+page.Elements[j].Kind
		})
		sort.Slice(page.Sections, func(i, j int) bool { return page.Sections[i].Element < page.Sections[j].Element })
		for index := range page.Scroll {
			page.Scroll[index].Rate = percentage(page.Scroll[index].Users, page.Visitors)
		}
		result.Behaviour = append(result.Behaviour, *page)
	}
	for key, form := range forms {
		form.Fields = rows(formFields[key])
		result.Forms = append(result.Forms, *form)
	}
	sort.Slice(result.Forms, func(i, j int) bool {
		return result.Forms[i].Page+result.Forms[i].Form < result.Forms[j].Page+result.Forms[j].Form
	})
	for cohortKey, aggregate := range state.Cohorts {
		cohortDate, err := time.Parse("2006-01-02", cohortKey)
		if err != nil {
			continue
		}
		cohort := ConversionCohort{Week: cohortKey, Visitors: aggregate.Visitors}
		for index := 0; index < 12; index++ {
			week := cohortDate.AddDate(0, 0, (index+1)*7)
			cell := CohortWeek{Partial: now.Before(week.AddDate(0, 0, 7)), Rate: "—"}
			if !now.Before(week) {
				cell.Eligible = aggregate.Visitors
				cell.Returned = aggregate.Returned[index]
				cell.Rate = percentage(cell.Returned, cell.Eligible)
			}
			cohort.Weeks = append(cohort.Weeks, cell)
		}
		result.Cohorts = append(result.Cohorts, cohort)
	}

	for date, aggregate := range state.Daily {
		if date <= dayKey(now) {
			result.Daily = append(result.Daily, ConversionDay{Date: date, Visitors: aggregate.Visitors})
		}
	}
	sort.Slice(result.Daily, func(left, right int) bool { return result.Daily[left].Date < result.Daily[right].Date })
	sort.Slice(result.Locations, func(left, right int) bool { return result.Locations[left].Last.After(result.Locations[right].Last) })
	sort.Slice(result.Cohorts, func(i, j int) bool { return result.Cohorts[i].Week > result.Cohorts[j].Week })
	result.Sessions = replaySummaries(state.Replay, start, now)
	result.Impacts = pageImpacts(state, now)
	for _, device := range devices {
		result.Devices = append(result.Devices, *device)
	}
	sort.Slice(result.Devices, func(left, right int) bool { return result.Devices[left].Name < result.Devices[right].Name })
	if !result.Incomplete {
		result.Insights = conversionInsights(result)
	}
	return result
}
func sortPaths(paths []*ConversionPath) {
	sort.Slice(paths, func(i, j int) bool {
		if paths[i].Users != paths[j].Users {
			return paths[i].Users > paths[j].Users
		}
		return paths[i].Trail < paths[j].Trail
	})
	for _, path := range paths {
		sortPaths(path.Children)
	}
}
func conversionInsights(report ConversionReport) []ConversionInsight {
	insights := []ConversionInsight{}
	var mobile, desktop ConversionDevice
	for _, device := range report.Devices {
		if device.Name == "mobile" {
			mobile = device
		}
		if device.Name == "desktop" {
			desktop = device
		}
	}
	if mobile.Visitors >= 100 && desktop.Visitors >= 100 && mobile.Completed+desktop.Completed >= 10 && desktop.Completed > 0 {
		mobileRate := float64(mobile.Completed) / float64(mobile.Visitors)
		desktopRate := float64(desktop.Completed) / float64(desktop.Visitors)
		if mobileRate < desktopRate*0.8 {
			insights = append(insights, ConversionInsight{Kind: "device", Rate: fmt.Sprintf("%.1f%%", 100*(1-mobileRate/desktopRate))})
		}
	}
	for _, funnel := range report.Funnels {
		if !funnel.Primary || funnel.Users < 100 {
			continue
		}
		for index := 0; index < len(funnel.Steps)-1; index++ {
			step := funnel.Steps[index]
			if step.Lost*100 >= step.Users*30 && step.Lost >= 30 {
				insights = append(insights, ConversionInsight{Kind: "funnel", Subject: funnel.Steps[index+1].Name, Rate: percentage(step.Lost, step.Users)})
				break
			}
		}
	}
	for _, page := range report.Behaviour {
		for _, element := range page.Elements {
			if element.Kind == "dead-click" && element.Users >= 3 {
				insights = append(insights, ConversionInsight{Kind: "dead", Subject: page.Page, Users: element.Users})
			}
		}
		if page.Visitors >= 100 && len(page.Scroll) > 0 {
			last := page.Scroll[len(page.Scroll)-1]
			if last.Users*100 < page.Visitors*40 {
				insights = append(insights, ConversionInsight{Kind: "scroll", Subject: page.Page, Rate: last.Rate})
			}
		}
	}
	for index, source := range report.Sources {
		for _, other := range report.Sources[index+1:] {
			if source.Visitors < 100 || other.Visitors < 100 || source.Completed+other.Completed < 10 {
				continue
			}
			better, worse := source, other
			first := float64(source.Completed) / float64(source.Visitors)
			second := float64(other.Completed) / float64(other.Visitors)
			if first < second {
				first, second = second, first
				better, worse = other, source
			}
			if second > 0 && first >= second*1.2 {
				betterName, worseName := better.Name, worse.Name
				if better.Campaign != "" {
					betterName += " · " + better.Campaign
				}
				if worse.Campaign != "" {
					worseName += " · " + worse.Campaign
				}
				insights = append(insights, ConversionInsight{Kind: "source", Subject: betterName, Compared: worseName, Factor: fmt.Sprintf("%.1f", first/second)})
			}
		}
	}
	if len(insights) > 3 {
		insights = insights[:3]
	}
	return insights
}

func hasOpenFunnel(visitor *ConversionVisitor, now time.Time) bool {
	for _, progress := range visitor.Funnels {
		if now.Sub(progress.Entered) <= FunnelWindow {
			return true
		}
	}
	return false
}

func EmptyConversionReport(goals []Goal) ConversionReport {
	site := New(time.Now().UTC())
	site.Goals = goals
	site.Conversion = &ConversionState{Version: 2}
	return site.conversionReport(time.Now().UTC(), 30)
}

func URIEventName(match string) string {
	sum := sha256.Sum256([]byte(match))
	return "uri_" + hex.EncodeToString(sum[:8])
}
