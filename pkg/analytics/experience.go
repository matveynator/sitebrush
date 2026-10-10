package analytics

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
	"unicode"
)

// Attribution describes the observed evidence, not the authenticity of a sender.
type Attribution struct{ Name, Kind, Evidence, Detail string }
type Campaign struct {
	Source, Medium, Name, Content, Term string
	Google, Yandex, Facebook            bool
}
type Action struct {
	Name   string `json:"name"`
	Target string `json:"target"`
	Count  int    `json:"count"`
}
type Goal struct {
	Name  string
	Kind  string
	Match string
}
type BrowserContext struct {
	Limited                  bool     `json:"limited"`
	Tab                      string   `json:"tab"`
	Session                  string   `json:"session"`
	Language                 string   `json:"language"`
	PageLanguage             string   `json:"page_language"`
	Timezone                 string   `json:"timezone"`
	URI                      string   `json:"uri"`
	Offset                   *int     `json:"offset"`
	Campaign                 Campaign `json:"campaign"`
	Actions                  []Action `json:"actions"`
	Browser, OS, ClientClass string
	Country, City            string
	Latitude, Longitude      float64
	GeoKnown                 bool
	Address                  string `json:"-"`
}
type PageOutcome struct {
	Date, Path         string
	Views, Uncontinued int
	Acted, Progressed  bool
}
type SessionSummary struct {
	Referrer                                                                                             string
	GoalCounts                                                                                           map[string]int
	Campaign                                                                                             Campaign
	Outcomes                                                                                             map[string]*PageOutcome
	ID, Visitor, Landing, Language, PageLanguage, Browser, OS, Timezone, LocalTime, Country, City, Class string
	FirstSource                                                                                          string
	Source                                                                                               Attribution
	Started, Last                                                                                        time.Time
	ActiveMS, ReturnAfterMS                                                                              int64
	Scroll, Views, Actions                                                                               int
	Goal, Returning                                                                                      bool
	Complete, Limited                                                                                    bool
	Latitude, Longitude                                                                                  float64
	GeoKnown                                                                                             bool
	Steps                                                                                                []string
	Tabs                                                                                                 map[string][]string
	LastViews                                                                                            map[string]string
	Address                                                                                              string `json:"address,omitempty"`
}
type Measures struct {
	ActionSessions, NextSessions, CompletedSessions, EndSessions                                        int
	MismatchViews                                                                                       int
	ProgressViews                                                                                       int
	PageSessions, PageGoalSessions                                                                      int
	Sessions, Returning, GoalSessions, Views, ActionViews, NextViews, CompletedViews, EndViews, Actions int
	ActiveMS                                                                                            int64
	Scroll50, Scroll100                                                                                 int
}
type Segment struct {
	Referrer                                                string
	GoalCounts                                              map[string]int
	ActionCounts                                            map[string]int
	LocalHours                                              [24]int
	Date, Source, Evidence, Campaign, Page, Language, Class string
	Measures
}
type Experience struct {
	Version       int
	Started       time.Time
	Segments      map[string]*Segment
	Recent        map[string]*SessionSummary
	ActivityHours map[string]int
	ActivityDays  map[string]int
	LocalHours    [24]int
	Languages     map[string]int
	Incomplete    bool
}
type ExperienceReport struct {
	Version       int
	Segments      []Segment
	Recent        []SessionSummary
	ActivityHours map[string]int
	ActivityDays  map[string]int
	LocalHours    [24]int
	Incomplete    bool
}
type ExperienceFilter struct{ Source, Campaign, Page, Language, Traffic string }
type ExperienceRow struct {
	Referrer                   string
	GoalCounts                 map[string]int
	Label, Detail              string
	Source, Campaign, Evidence string
	Measures
}
type Insight struct {
	Kind, Label            string
	Numerator, Denominator int
}
type ExperienceView struct {
	GoalRows   []Row
	ActionRows []Row
	Measures
	Sources, Pages, Languages, Journeys []ExperienceRow
	Recent                              []SessionSummary
	Insights                            []Insight
	LocalHours                          [24]int
	ActivityHours                       map[string]int
	ActivityDays                        map[string]int
	Incomplete                          bool
	GoalsConfigured                     bool
}

func CleanText(raw string, maximum int) string {
	raw = strings.Map(func(character rune) rune {
		if unicode.IsControl(character) {
			return -1
		}
		return character
	}, raw)
	if len(raw) > maximum {
		raw = raw[:maximum]
	}
	return strings.TrimSpace(raw)
}
func SafePath(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "/"
	}
	pathname := parsed.EscapedPath()
	if pathname == "" {
		pathname = "/"
	}
	// Token-like path components are not useful dimensions.
	parts := strings.Split(pathname, "/")
	for index, part := range parts {
		if len(part) > 64 {
			parts[index] = "[redacted]"
		}
	}
	return CleanText(strings.Join(parts, "/"), 256)
}
func SafeURI(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "" || parsed.Host != "" || parsed.User != nil {
		return ""
	}
	pathname := SafePath(raw)
	query := parsed.Query()
	for key := range query {
		lower := strings.ToLower(key)
		sensitive := map[string]bool{"login_challenge": true, "profile_resume": true, "recovery_token": true, "email_confirm": true, "registration_form_token": true, "recovery_code": true, "login_code": true, "password_confirmation_code": true, "token": true, "access_token": true, "id_token": true, "secret": true, "password": true, "passwd": true, "auth": true, "authorization": true, "session": true, "sessionid": true, "session_id": true, "api_key": true, "apikey": true, "key": true, "code": true, "gclid": true, "yclid": true, "fbclid": true}
		if sensitive[lower] || strings.HasPrefix(lower, "utm_") {
			query.Del(key)
		}
	}
	result := pathname
	if encoded := query.Encode(); encoded != "" {
		result += "?" + encoded
	}
	if parsed.Fragment != "" {
		fragment := CleanText(parsed.Fragment, 64)
		if fragment == "" || strings.ContainsAny(fragment, "?#&=\\/\r\n\t ") {
			return ""
		}
		result += "#" + fragment
	}
	if len(result) > 256 {
		return ""
	}
	return result
}
func SafeTarget(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if parsed.Scheme != "" && parsed.Scheme != "http" && parsed.Scheme != "https" {
		return parsed.Scheme + ":"
	}
	if parsed.Hostname() != "" {
		return CleanText(strings.ToLower(parsed.Hostname()), 128) + SafePath(raw)
	}
	return SafePath(raw)
}
func SafeReferrer(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
		return ""
	}
	query := parsed.Query()
	for key := range query {
		lower := strings.ToLower(key)
		sensitive := map[string]bool{"login_challenge": true, "profile_resume": true, "recovery_token": true, "email_confirm": true, "registration_form_token": true, "recovery_code": true, "login_code": true, "password_confirmation_code": true, "token": true, "access_token": true, "id_token": true, "secret": true, "password": true, "passwd": true, "auth": true, "authorization": true, "session": true, "sessionid": true, "session_id": true, "api_key": true, "apikey": true, "key": true, "code": true, "gclid": true, "yclid": true, "fbclid": true}
		if sensitive[lower] {
			query.Del(key)
		}
	}
	result := strings.ToLower(parsed.Scheme) + "://" + parsed.Host + SafePath(raw)
	if encoded := query.Encode(); encoded != "" {
		result += "?" + encoded
	}
	if len(result) > 1024 {
		result = strings.ToLower(parsed.Scheme) + "://" + parsed.Host + SafePath(raw)
	}
	if len(result) > 1024 {
		return ""
	}
	return CleanText(result, 1024)
}
func SourceAttribution(campaign Campaign, referrer, siteHost string) Attribution {
	if campaign.Source != "" {
		return Attribution{CleanText(campaign.Source, 64), "campaign", "utm", CleanText(campaign.Name, 64)}
	}
	if campaign.Google {
		return Attribution{"Google Ads", "campaign", "click-parameter", ""}
	}
	if campaign.Yandex {
		return Attribution{"Yandex Ads", "campaign", "click-parameter", ""}
	}
	if campaign.Facebook {
		return Attribution{"Meta", "social", "click-parameter", ""}
	}
	safeReferrer := SafeReferrer(referrer)
	parsed, err := url.Parse(safeReferrer)
	host := strings.ToLower(parsedHost(parsed))
	if strings.TrimSpace(referrer) == "" {
		return Attribution{"direct", "direct", "absent", ""}
	}
	if err != nil || host == "" {
		original, parseErr := url.Parse(referrer)
		if parseErr == nil && original.Scheme != "" && original.Hostname() != "" {
			return Attribution{CleanText(original.Hostname(), 128), "app", "referrer", CleanText(original.Scheme+"://"+original.Host+SafePath(referrer), 256)}
		}
		return Attribution{"unknown-referrer", "referral", "referrer", "URL unavailable"}
	}
	if strings.EqualFold(host, strings.Split(siteHost, ":")[0]) {
		return Attribution{host, "internal", "referrer", safeReferrer}
	}
	for _, rule := range []struct{ host, name, kind string }{
		{"google.com", "Google", "search"}, {"google.ru", "Google", "search"}, {"yandex.ru", "Yandex", "search"}, {"yandex.com", "Yandex", "search"}, {"bing.com", "Bing", "search"}, {"duckduckgo.com", "DuckDuckGo", "search"},
		{"chatgpt.com", "ChatGPT", "ai"}, {"chat.openai.com", "ChatGPT", "ai"}, {"claude.ai", "Claude", "ai"}, {"perplexity.ai", "Perplexity", "ai"}, {"t.me", "Telegram", "messenger"}, {"telegram.org", "Telegram", "messenger"}, {"habr.com", "Habr", "referral"}, {"github.com", "GitHub", "referral"}, {"reddit.com", "Reddit", "social"},
	} {
		if host == rule.host || strings.HasSuffix(host, "."+rule.host) {
			return Attribution{rule.name, rule.kind, "referrer", safeReferrer}
		}
	}
	return Attribution{CleanText(host, 128), "referral", "referrer", safeReferrer}
}

// Application signatures are evidence only when no referrer was supplied.
func SourceWithAgent(campaign Campaign, referrer, siteHost, agent string) Attribution {
	source := SourceAttribution(campaign, referrer, siteHost)
	if source.Kind != "direct" {
		return source
	}
	for _, signature := range []struct{ match, name string }{{"telegram", "Telegram"}, {"fban", "Facebook"}, {"fbav", "Facebook"}, {"instagram", "Instagram"}, {"linkedinapp", "LinkedIn"}} {
		if strings.Contains(strings.ToLower(agent), signature.match) {
			return Attribution{signature.name, "app", "user-agent", ""}
		}
	}
	return source
}
func parsedHost(parsed *url.URL) string {
	if parsed == nil {
		return ""
	}
	return parsed.Hostname()
}
func ValidateGoals(goals []Goal) error {
	if len(goals) > 32 {
		return fmt.Errorf("maximum 32 goals")
	}
	for _, goal := range goals {
		if goal.Name == "" || len(goal.Name) > 64 || goal.Match == "" || len(goal.Match) > 256 || (goal.Kind != "action" && goal.Kind != "path" && goal.Kind != "uri") || ((goal.Kind == "path" || goal.Kind == "uri") && SafeURI(goal.Match) != goal.Match) {
			return fmt.Errorf("invalid goal: %s", goal.Name)
		}
	}
	return nil
}
func goalMatched(goals []Goal, path, uri string, actions []Action) bool {
	location := SafeURI(uri)
	if location == "" {
		location = path
	}
	for _, goal := range goals {
		if (goal.Kind == "path" || goal.Kind == "uri") && goal.Match == location {
			return true
		}
		for _, action := range actions {
			if action.Count > 0 && goal.Kind == "action" && action.Name == goal.Match {
				return true
			}
		}
	}
	return false
}
func (site *Site) experienceSegment(now time.Time, session *SessionSummary, pathname string) *Segment {
	if site.Experience.Segments == nil {
		site.Experience.Segments = map[string]*Segment{}
	}
	date := dayKey(now)
	key := strings.Join([]string{date, session.Source.Name, session.Source.Detail, session.Referrer, pathname, session.Language, session.Class}, "\x1f")
	if segment := site.Experience.Segments[key]; segment != nil {
		return segment
	}
	count := 0
	for _, segment := range site.Experience.Segments {
		if segment.Date == date {
			count++
		}
	}
	if count >= 64 {
		key = date + "\x1f(other)" + session.Class
		site.Experience.Incomplete = true
		if segment := site.Experience.Segments[key]; segment != nil {
			return segment
		}
		site.Experience.Segments[key] = &Segment{Date: date, Source: "(other)", Page: "(other)", Language: "unknown", Class: session.Class}
		return site.Experience.Segments[key]
	}
	segment := &Segment{Date: date, Source: session.Source.Name, Evidence: session.Source.Evidence, Campaign: session.Source.Detail, Page: pathname, Language: session.Language, Class: session.Class}
	segment.Referrer = session.Referrer
	site.Experience.Segments[key] = segment
	return segment
}

// Detail is bounded independently from daily totals; no raw event journal exists.
func (site *Site) recordExperience(event Event, now time.Time, visitor *Visitor, view *View, fresh bool, delta int64, previousScroll int) {
	if event.Limited {
		site.Experience.Incomplete = true
	}
	legacyClient := event.Tab == ""
	if legacyClient {
		// Clients that cached analytics.js before PR #84 do not send tab/session
		// context. Keep them visible in the new Experience dashboard instead of
		// silently dropping otherwise valid views and referrers.
		event.Tab = "legacy"
	}
	if site.Experience.Version == 0 {
		site.Experience.Version = 1
		site.Experience.Started = now
	}
	if site.Experience.Recent == nil {
		site.Experience.Recent = map[string]*SessionSummary{}
	}
	key := fmt.Sprintf("%s:%d", event.Visitor, visitor.Session)
	session := site.Experience.Recent[key]
	if session == nil {
		if !fresh || (visitor.SessionViews > 1 && !legacyClient) {
			site.Experience.Incomplete = true
			return
		}
		source := event.Attribution
		if source.Name == "" {
			source = Attribution{event.Source, "unknown", "legacy", ""}
		}
		session = &SessionSummary{ID: key, Visitor: event.Visitor, Landing: event.Path, Language: event.Language, PageLanguage: event.PageLanguage, Browser: event.Browser, OS: event.OS, Timezone: event.Timezone, Source: source, Campaign: event.Campaign, FirstSource: visitor.FirstSource, Started: now, Class: event.ClientClass, Tabs: map[string][]string{}, LastViews: map[string]string{}, Address: event.Address}
		session.Referrer = SafeReferrer(event.Referrer)
		if session.Class == "" {
			session.Class = "human-likely"
		}
		if session.Language == "" {
			session.Language = "unknown"
		}
		if visitor.Session > 1 {
			session.Returning = true
			if !visitor.Last.IsZero() && now.After(visitor.Last) {
				session.ReturnAfterMS = now.Sub(visitor.Last).Milliseconds()
			}
		}
		if event.Offset != nil && *event.Offset >= -840 && *event.Offset <= 840 {
			local := now.UTC().Add(-time.Duration(*event.Offset) * time.Minute)
			session.LocalTime = local.Format("15:04")
			site.experienceSegment(now, session, session.Landing).LocalHours[local.Hour()]++
		}
		site.Experience.Recent[key] = session
		activityDate := now.UTC().Format("2006-01-02")
		if site.Experience.ActivityDays == nil {
			site.Experience.ActivityDays = map[string]int{}
		}
		if site.Experience.ActivityHours == nil {
			site.Experience.ActivityHours = map[string]int{}
		}
		activityKey := activityDate + "\x1f" + session.Class
		site.Experience.ActivityDays[activityKey]++
		site.Experience.ActivityHours[activityDate+"T"+now.UTC().Format("15")+"\x1f"+session.Class]++
		first := site.experienceSegment(now, session, session.Landing)
		first.Sessions++
		if visitor.Session > 1 {
			first.Returning++
		}
	}
	session.Last = now
	session.ActiveMS += delta
	session.Scroll = max(session.Scroll, event.Scroll)
	if session.Tabs == nil {
		session.Tabs = map[string][]string{}
	}
	if session.LastViews == nil {
		session.LastViews = map[string]string{}
	}
	segment := site.experienceSegment(view.Started, session, event.Path)
	segment.ActiveMS += delta
	// Count deltas before action totals advance, so retries cannot repeat goals.
	for _, goal := range site.Goals {
		achievements := 0
		if (goal.Kind == "path" && fresh && goal.Match == event.Path) || (goal.Kind == "uri" && (fresh || event.URI != view.URI) && goal.Match == SafeURI(event.URI)) {
			achievements = 1
		}
		if goal.Kind == "action" {
			counts := map[string]int{}
			for _, action := range event.Actions {
				if action.Name == goal.Match {
					identity := action.Name + "\x1f" + action.Target
					counts[identity] = max(counts[identity], action.Count)
				}
			}
			for identity, count := range counts {
				achievements += max(0, count-view.Actions[identity])
			}
		}
		if achievements > 0 {
			if segment.GoalCounts == nil {
				segment.GoalCounts = map[string]int{}
			}
			if session.GoalCounts == nil {
				session.GoalCounts = map[string]int{}
			}
			if len(segment.GoalCounts) < 32 || segment.GoalCounts[goal.Name] > 0 {
				segment.GoalCounts[goal.Name] += achievements
				session.GoalCounts[goal.Name] += achievements
			} else {
				site.Experience.Incomplete = true
			}
		}
	}
	if fresh {
		segment.Views++
		if session.Outcomes == nil {
			session.Outcomes = map[string]*PageOutcome{}
		}
		outcomeKey := dayKey(now) + "\x1f" + event.Path
		firstPath := true
		for _, candidate := range session.Outcomes {
			if candidate.Path == event.Path {
				firstPath = false
				break
			}
		}
		if firstPath {
			segment.PageSessions++
			if session.Goal {
				segment.PageGoalSessions++
			}
		}
		outcome := session.Outcomes[outcomeKey]
		if outcome == nil && len(session.Outcomes) < 32 {
			outcome = &PageOutcome{Date: dayKey(now), Path: event.Path}
			session.Outcomes[outcomeKey] = outcome
		}
		if outcome != nil {
			outcome.Views++
			outcome.Uncontinued++
		} else {
			session.Limited = true
		}
		if session.Language != "unknown" && event.PageLanguage != "" && strings.Split(strings.ToLower(session.Language), "-")[0] != strings.Split(strings.ToLower(event.PageLanguage), "-")[0] {
			segment.MismatchViews++
		}
		session.Views++
		if previous := session.LastViews[event.Tab]; previous != "" {
			if previousView := visitor.Views[previous]; previousView != nil && !previousView.Next {
				if !previousView.Acted {
					site.experienceSegment(previousView.Started, session, previousView.Path).ProgressViews++
					decrementUncontinued(session, previousView)
				}
				previousView.Next = true
				site.experienceSegment(previousView.Started, session, previousView.Path).NextViews++
				site.markPageProgress(session, previousView, false)
			}
		}
		if len(session.LastViews) < 4 || session.LastViews[event.Tab] != "" {
			session.LastViews[event.Tab] = event.View
		}
		appendStep(session, event.Tab, event.Path)
	}
	if previousScroll < 50 && event.Scroll >= 50 {
		segment.Scroll50++
	}
	if previousScroll < 100 && event.Scroll >= 100 {
		segment.Scroll100++
	}
	if view.Actions == nil {
		view.Actions = map[string]int{}
	}
	for _, action := range event.Actions {
		identity := action.Name + "\x1f" + action.Target
		difference := action.Count - view.Actions[identity]
		if difference <= 0 {
			continue
		}
		if len(view.Actions) >= 16 && view.Actions[identity] == 0 {
			site.Experience.Incomplete = true
			continue
		}
		if !view.Acted {
			segment.ActionViews++
			if !view.Next {
				segment.ProgressViews++
				decrementUncontinued(session, view)
			}
			view.Acted = true
			site.markPageProgress(session, view, true)
		}
		if segment.ActionCounts == nil {
			segment.ActionCounts = map[string]int{}
		}
		actionKey := action.Name + " → " + action.Target
		if _, ok := segment.ActionCounts[actionKey]; !ok && len(segment.ActionCounts) >= 16 {
			actionKey = "(other)"
		}
		segment.ActionCounts[actionKey] += difference
		segment.Actions += difference
		session.Actions += difference
		view.Actions[identity] = action.Count
		appendStep(session, event.Tab, "→ "+action.Name+" "+action.Target)
	}
	if !session.Goal && len(session.GoalCounts) > 0 {
		session.Goal = true
		countedPages := map[string]bool{}
		for _, outcome := range session.Outcomes {
			if countedPages[outcome.Path] {
				continue
			}
			countedPages[outcome.Path] = true
			stamp, _ := time.Parse("2006-01-02", outcome.Date)
			site.experienceSegment(stamp, session, outcome.Path).PageGoalSessions++
		}
		site.experienceSegment(session.Started, session, session.Landing).GoalSessions++
	}
	site.pruneExperience(now)
}
func decrementUncontinued(session *SessionSummary, view *View) {
	key := dayKey(view.Started) + "\x1f" + view.Path
	if outcome := session.Outcomes[key]; outcome != nil && outcome.Uncontinued > 0 {
		outcome.Uncontinued--
	}
}

// A page's behavior is counted once per session, even when it is reloaded.
func (site *Site) markPageProgress(session *SessionSummary, view *View, action bool) {
	firstAction, firstProgress := true, true
	for _, outcome := range session.Outcomes {
		if outcome.Path == view.Path {
			firstAction = firstAction && !outcome.Acted
			firstProgress = firstProgress && !outcome.Progressed
		}
	}
	outcome := session.Outcomes[dayKey(view.Started)+"\x1f"+view.Path]
	if outcome == nil {
		return
	}
	segment := site.experienceSegment(view.Started, session, view.Path)
	if action && firstAction {
		segment.ActionSessions++
	}
	if firstProgress {
		segment.NextSessions++
	}
	outcome.Acted = outcome.Acted || action
	outcome.Progressed = true
}
func appendStep(session *SessionSummary, tab, step string) {
	if len(session.Tabs) >= 4 && session.Tabs[tab] == nil {
		session.Limited = true
		return
	}
	total := 0
	for _, steps := range session.Tabs {
		total += len(steps)
	}
	if total >= 12 {
		session.Limited = true
		return
	}
	session.Tabs[tab] = append(session.Tabs[tab], step)
}
func (site *Site) pruneExperience(now time.Time) {
	activityCutoff := dayKey(now.UTC().AddDate(0, 0, -364))
	for activityKey := range site.Experience.ActivityDays {
		if len(activityKey) < 10 || activityKey[:10] < activityCutoff {
			delete(site.Experience.ActivityDays, activityKey)
		}
	}
	for activityKey := range site.Experience.ActivityHours {
		if len(activityKey) < 10 || activityKey[:10] < activityCutoff {
			delete(site.Experience.ActivityHours, activityKey)
		}
	}
	for key, segment := range site.Experience.Segments {
		if segment.Date < dayKey(now.AddDate(0, 0, -89)) {
			delete(site.Experience.Segments, key)
		}
	}
	for key, session := range site.Experience.Recent {
		if !session.Complete && now.Sub(session.Last) > 30*time.Minute {
			session.Complete = true
			completed := map[string]bool{}
			for _, outcome := range session.Outcomes {
				stamp, err := time.Parse("2006-01-02", outcome.Date)
				if err != nil {
					continue
				}
				daily := site.experienceSegment(stamp, session, outcome.Path)
				daily.CompletedViews += outcome.Views
				daily.EndViews += outcome.Uncontinued
				if !completed[outcome.Path] {
					completed[outcome.Path] = true
					daily.CompletedSessions++
					progressed := false
					for _, candidate := range session.Outcomes {
						if candidate.Path == outcome.Path && candidate.Progressed {
							progressed = true
						}
					}
					if !progressed {
						daily.EndSessions++
					}
				}
			}
		}
		if now.Sub(session.Last) > 7*24*time.Hour {
			delete(site.Experience.Recent, key)
		}
	}
	for len(site.Experience.Recent) > 500 {
		site.evictExperience()
	}
}
func (site *Site) evictExperience() {
	oldest := ""
	var stamp time.Time
	for key, session := range site.Experience.Recent {
		if oldest == "" || session.Last.Before(stamp) {
			oldest = key
			stamp = session.Last
		}
	}
	if oldest != "" {
		delete(site.Experience.Recent, oldest)
		site.Experience.Incomplete = true
	}
}
func (site *Site) experienceBytes() int64 {
	total := int64(len(site.Experience.Recent)) * 12288
	total += int64(len(site.Experience.ActivityDays))*48 + int64(len(site.Experience.ActivityHours))*56
	for _, segment := range site.Experience.Segments {
		total += 1536
		for label := range segment.ActionCounts {
			total += int64(len(label) + 96)
		}
	}
	return total
}

func (site *Site) ExperienceReport(now time.Time, days int, detail bool) ExperienceReport {
	report := ExperienceReport{Version: site.Experience.Version, Incomplete: site.Experience.Incomplete, LocalHours: site.Experience.LocalHours, ActivityHours: make(map[string]int), ActivityDays: make(map[string]int)}
	for activityKey, count := range site.Experience.ActivityHours {
		report.ActivityHours[activityKey] = count
	}
	for activityKey, count := range site.Experience.ActivityDays {
		report.ActivityDays[activityKey] = count
	}
	first := dayKey(now.AddDate(0, 0, -days+1))
	for _, segment := range site.Experience.Segments {
		if segment.Date >= first && segment.Date <= dayKey(now) {
			report.Segments = append(report.Segments, *segment)
		}
	}
	sort.Slice(report.Segments, func(i, j int) bool {
		left, right := report.Segments[i], report.Segments[j]
		return left.Date+left.Source+left.Page+left.Language < right.Date+right.Source+right.Page+right.Language
	})
	if detail {
		for _, session := range site.Experience.Recent {
			if dayKey(session.Started) >= first && !session.Started.After(now) {
				copySession := *session
				copySession.LastViews = nil
				copySession.Outcomes = nil
				report.Recent = append(report.Recent, copySession)
			}
		}
		sort.Slice(report.Recent, func(i, j int) bool { return report.Recent[i].Started.After(report.Recent[j].Started) })
		if len(report.Recent) > 100 {
			report.Recent = report.Recent[:100]
		}
	}
	return report
}
func addMeasures(target *Measures, source Measures) {
	target.ActionSessions += source.ActionSessions
	target.NextSessions += source.NextSessions
	target.CompletedSessions += source.CompletedSessions
	target.EndSessions += source.EndSessions
	target.MismatchViews += source.MismatchViews
	target.ProgressViews += source.ProgressViews
	target.PageSessions += source.PageSessions
	target.PageGoalSessions += source.PageGoalSessions
	target.Sessions += source.Sessions
	target.GoalSessions += source.GoalSessions
	target.Returning += source.Returning
	target.Views += source.Views
	target.Actions += source.Actions
	target.ActionViews += source.ActionViews
	target.NextViews += source.NextViews
	target.CompletedViews += source.CompletedViews
	target.EndViews += source.EndViews
	target.ActiveMS += source.ActiveMS
	target.Scroll50 += source.Scroll50
	target.Scroll100 += source.Scroll100
}
func (report ExperienceReport) View(filter ExperienceFilter) ExperienceView {
	result := ExperienceView{Incomplete: report.Incomplete, LocalHours: [24]int{}, ActivityHours: make(map[string]int), ActivityDays: make(map[string]int)}
	acceptsTraffic := func(class string) bool {
		return filter.Traffic == "all" || (filter.Traffic == "bots" && class != "human-likely") || (filter.Traffic != "bots" && class == "human-likely")
	}
	for activityKey, count := range report.ActivityDays {
		date, class, found := strings.Cut(activityKey, "\x1f")
		if found && acceptsTraffic(class) {
			result.ActivityDays[date] += count
		}
	}
	for activityKey, count := range report.ActivityHours {
		hour, class, found := strings.Cut(activityKey, "\x1f")
		if found && acceptsTraffic(class) {
			result.ActivityHours[hour] += count
		}
	}
	actionCounts := map[string]int{}
	goalCounts := map[string]int{}
	sources, pages, languages := map[string]*ExperienceRow{}, map[string]*ExperienceRow{}, map[string]*ExperienceRow{}
	accepts := func(source, page, language, class string) bool {
		return (filter.Source == "" || filter.Source == source) && (filter.Page == "" || filter.Page == page) && (filter.Language == "" || filter.Language == language) && (filter.Traffic == "all" || (filter.Traffic == "bots" && class != "human-likely") || (filter.Traffic != "bots" && class == "human-likely"))
	}
	for _, segment := range report.Segments {
		if (filter.Campaign != "" && filter.Campaign != segment.Campaign) || !accepts(segment.Source, segment.Page, segment.Language, segment.Class) {
			continue
		}
		if filter.Page != "" {
			segment.Sessions = segment.PageSessions
			segment.GoalSessions = segment.PageGoalSessions
		}
		merge(actionCounts, segment.ActionCounts)
		merge(goalCounts, segment.GoalCounts)
		addMeasures(&result.Measures, segment.Measures)
		for hour, count := range segment.LocalHours {
			result.LocalHours[hour] += count
		}
		for _, group := range []struct {
			rows        map[string]*ExperienceRow
			key, detail string
		}{{sources, segment.Source + "\x1f" + segment.Evidence + "\x1f" + segment.Campaign + "\x1f" + segment.Referrer, segment.Evidence + " · " + segment.Campaign}, {pages, segment.Page, ""}, {languages, segment.Language, ""}} {
			if group.rows[group.key] == nil {
				label := group.key
				source, campaign, evidence := "", "", ""
				if group.detail != "" {
					label = segment.Source
					source = segment.Source
					campaign = segment.Campaign
					evidence = segment.Evidence
				}
				group.rows[group.key] = &ExperienceRow{Label: label, Detail: group.detail, Source: source, Campaign: campaign, Evidence: evidence}
				group.rows[group.key].Referrer = segment.Referrer
			}
			addMeasures(&group.rows[group.key].Measures, segment.Measures)
			if group.rows[group.key].GoalCounts == nil {
				group.rows[group.key].GoalCounts = map[string]int{}
			}
			merge(group.rows[group.key].GoalCounts, segment.GoalCounts)
		}
	}
	ordered := func(group map[string]*ExperienceRow) []ExperienceRow {
		rows := []ExperienceRow{}
		for _, row := range group {
			rows = append(rows, *row)
		}
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].Views == rows[j].Views {
				return rows[i].Label+rows[i].Detail < rows[j].Label+rows[j].Detail
			}
			return rows[i].Views > rows[j].Views
		})
		return rows
	}
	result.ActionRows = rows(actionCounts)
	result.GoalRows = rows(goalCounts)
	result.Sources = ordered(sources)
	result.Pages = ordered(pages)
	for index := range result.Pages {
		result.Pages[index].Sessions = result.Pages[index].PageSessions
		result.Pages[index].GoalSessions = result.Pages[index].PageGoalSessions
	}
	result.Languages = ordered(languages)
	journeys := map[string]*ExperienceRow{}
	loops, hiddenReturns := 0, 0
	localHoursKnown := false
	for _, count := range result.LocalHours {
		if count > 0 {
			localHoursKnown = true
			break
		}
	}
	for _, session := range report.Recent {
		sessionPage := session.Landing
		if filter.Page != "" {
			for _, steps := range session.Tabs {
				for _, step := range steps {
					if step == filter.Page {
						sessionPage = filter.Page
					}
				}
			}
		}
		if (filter.Campaign != "" && filter.Campaign != session.Source.Detail) || !accepts(session.Source.Name, sessionPage, session.Language, session.Class) {
			continue
		}
		result.Recent = append(result.Recent, session)
		if !localHoursKnown && len(session.LocalTime) >= 2 {
			hour := int(session.LocalTime[0]-'0')*10 + int(session.LocalTime[1]-'0')
			if hour >= 0 && hour < len(result.LocalHours) {
				result.LocalHours[hour]++
			}
		}
		if (session.Source.Name == "direct" || session.Source.Name == "direct-hidden") && session.FirstSource != "direct-hidden" && session.FirstSource != "direct" && session.FirstSource != "" {
			hiddenReturns++
		}
		loopFound := false
		for _, steps := range session.Tabs {
			paths := []string{}
			for _, step := range steps {
				if strings.HasPrefix(step, "/") {
					paths = append(paths, step)
				}
			}
			for index := 3; index < len(paths); index++ {
				if paths[index] == paths[index-2] && paths[index-1] == paths[index-3] && paths[index] != paths[index-1] {
					loopFound = true
				}
			}
		}
		if loopFound {
			loops++
		}
		seenJourneys := map[string]bool{}
		for _, steps := range session.Tabs {
			label := session.Source.Name + " → " + strings.Join(steps, " → ")
			if seenJourneys[label] {
				continue
			}
			seenJourneys[label] = true
			if journeys[label] == nil {
				journeys[label] = &ExperienceRow{Label: label}
			}
			journeys[label].Sessions++
		}
	}
	result.Journeys = ordered(journeys)
	sort.Slice(result.Journeys, func(i, j int) bool {
		if result.Journeys[i].Sessions == result.Journeys[j].Sessions {
			return result.Journeys[i].Label < result.Journeys[j].Label
		}
		return result.Journeys[i].Sessions > result.Journeys[j].Sessions
	})
	if len(result.Journeys) > 12 {
		result.Journeys = result.Journeys[:12]
	}
	if loops > 0 {
		result.Insights = append(result.Insights, Insight{"navigation-loop", "", loops, len(result.Recent)})
	}
	if hiddenReturns > 0 {
		result.Insights = append(result.Insights, Insight{"hidden-return", "", hiddenReturns, len(result.Recent)})
	}
	// Insights are observations with explicit denominators, not causal claims.
	for _, source := range result.Sources {
		if source.Sessions > 0 {
			result.Insights = append(result.Insights, Insight{"source-goal", source.Label, source.GoalSessions, source.Sessions})
		}
	}
	for _, page := range result.Pages {
		if page.Views > 0 && page.MismatchViews > 0 {
			result.Insights = append(result.Insights, Insight{"language-mismatch", page.Label, page.MismatchViews, page.Views})
		}
		if page.PageSessions > 0 {
			result.Insights = append(result.Insights, Insight{"continued", page.Label, page.NextSessions, page.PageSessions})
		}
		if page.CompletedSessions > 0 {
			result.Insights = append(result.Insights, Insight{"end", page.Label, page.EndSessions, page.CompletedSessions})
		}
	}
	for _, language := range result.Languages {
		if language.Sessions > 0 {
			result.Insights = append(result.Insights, Insight{"language-goal", language.Label, language.GoalSessions, language.Sessions})
		}
	}
	if len(result.Insights) > 12 {
		result.Insights = result.Insights[:12]
	}

	return result
}

func ratio(numerator, denominator int) string {
	if denominator == 0 {
		return "No data"
	}
	return fmt.Sprintf("%.1f%% (%d/%d)", 100*float64(numerator)/float64(denominator), numerator, denominator)
}
func (metrics Measures) GoalRate() string { return ratio(metrics.GoalSessions, metrics.Sessions) }
func (metrics Measures) ActionRate() string {
	return ratio(metrics.ActionSessions, metrics.PageSessions)
}
func (metrics Measures) NextRate() string { return ratio(metrics.NextSessions, metrics.PageSessions) }
func (metrics Measures) EndRate() string {
	return ratio(metrics.EndSessions, metrics.CompletedSessions)
}
func (metrics Measures) ScrollRate() string { return ratio(metrics.Scroll50, metrics.Views) }
func (metrics Measures) AverageActive() string {
	if metrics.Views == 0 {
		return "No data"
	}
	return fmt.Sprintf("%.1f s", float64(metrics.ActiveMS)/float64(metrics.Views)/1000)
}
func (session SessionSummary) Active() string {
	return fmt.Sprintf("%.1f s", float64(session.ActiveMS)/1000)
}
func (session SessionSummary) ReturnAfter() string {
	if session.ReturnAfterMS <= 0 {
		return "No data"
	}
	duration := time.Duration(session.ReturnAfterMS) * time.Millisecond
	if duration >= 24*time.Hour {
		return fmt.Sprintf("%dd %dh", int(duration/(24*time.Hour)), int(duration%(24*time.Hour)/time.Hour))
	}
	if duration >= time.Hour {
		return fmt.Sprintf("%dh %dm", int(duration/time.Hour), int(duration%time.Hour/time.Minute))
	}
	return fmt.Sprintf("%dm", max(1, int(duration/time.Minute)))
}
func (session SessionSummary) When() string {
	return session.Started.UTC().Format("2006-01-02 15:04 UTC")
}
