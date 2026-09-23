package analytics

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
)

type RequestObservation struct {
	Time                                                    time.Time
	IP, Path, Query, Method, Agent, Language, Country, City string
	Status                                                  int
	Bytes                                                   int64
	Trusted                                                 bool
}
type Probe struct {
	Path, Category, Method string
	Status, Count          int
	First, Last            time.Time
	Bytes                  int64
}
type Incident struct {
	IP, Country, City, Agent, Class string
	First, Last                     time.Time
	Count                           int
	PossibleExposure                bool
	Categories                      map[string]int
	Examples                        []Probe
}
type RequestGroup struct {
	Date, Class, Agent, Path, Language string
	Count, Errors                      int
}
type SecurityState struct {
	Started, Updated time.Time
	Incidents        []Incident
	Groups           map[string]*RequestGroup
	Windows          map[string]*RequestWindow `json:"-"`
	Incomplete       bool
}
type RequestWindow struct {
	First, Last     time.Time
	Count, Failures int
	Paths           map[string]bool
}
type SecurityReport struct {
	Incidents                    []Incident
	Groups                       []RequestGroup
	Requests, Errors, Suspicious int
	Incomplete                   bool
}

// Names identify client claims. User-Agent alone cannot authenticate a crawler.
func ClientClass(agent string) string {
	lowered := strings.ToLower(agent)
	for _, token := range []string{"gptbot", "claudebot", "perplexitybot", "oai-searchbot", "chatgpt-user", "anthropic", "claude-user"} {
		if strings.Contains(lowered, token) {
			return "ai-crawler"
		}
	}
	for _, token := range []string{"googlebot", "bingbot", "yandexbot", "duckduckbot", "baiduspider"} {
		if strings.Contains(lowered, token) {
			return "search-crawler"
		}
	}
	for _, token := range []string{"sqlmap", "nikto", "nuclei", "masscan", "zgrab"} {
		if strings.Contains(lowered, token) {
			return "scanner"
		}
	}
	for _, token := range []string{"bot", "crawler", "spider", "curl/", "wget/", "python", "go-http-client", "monitor", "uptime"} {
		if strings.Contains(lowered, token) {
			return "automation"
		}
	}
	return "unknown"
}
func ProbeCategory(pathname, query string) string {
	decoded := strings.ToLower(pathname)
	for index := 0; index < 2; index++ {
		next, err := url.PathUnescape(decoded)
		if err != nil {
			break
		}
		decoded = next
	}
	if strings.Contains(decoded, "../") || strings.Contains(decoded, "..\\") {
		return "traversal"
	}
	for _, token := range []string{"/.git", "/.svn", "/.hg"} {
		if decoded == token || strings.HasPrefix(decoded, token+"/") {
			return "repository"
		}
	}
	for _, token := range []string{"/.env", "/id_rsa", "/credentials", "/private.key", "/database.sql", "/backup.sql"} {
		if strings.Contains(decoded, token) {
			return "secret"
		}
	}
	for _, token := range []string{"/wp-login.php", "/wp-admin", "/xmlrpc.php", "/phpmyadmin", "/vendor/phpunit", "/cgi-bin/"} {
		if strings.Contains(decoded, token) {
			return "cms"
		}
	}
	if strings.HasSuffix(decoded, ".bak") || strings.HasSuffix(decoded, ".sql") || strings.HasSuffix(decoded, ".old") || strings.Contains(decoded, "/backup.zip") {
		return "source-backup"
	}
	// Query samples are inspected transiently and never copied into the incident.
	parameters, _ := url.QueryUnescape(strings.ToLower(query))
	for _, token := range []string{"union select", "<script", "javascript:", ";cat ", "$(", "/etc/passwd", "sleep("} {
		if strings.Contains(parameters, token) {
			return "injection"
		}
	}
	if decoded == "/admin" || decoded == "/admin/" || decoded == "/login" {
		return "admin-discovery"
	}
	return ""
}
func (state *SecurityState) Record(request RequestObservation) string {
	now := request.Time
	if state.Started.IsZero() {
		state.Started = now
	}
	previous := state.Updated
	state.Updated = now
	if state.Groups == nil {
		state.Groups = map[string]*RequestGroup{}
	}
	if state.Windows == nil {
		state.Windows = map[string]*RequestWindow{}
	}
	class := ClientClass(request.Agent)
	category := ""
	if !request.Trusted {
		category = ProbeCategory(request.Path, request.Query)
	}
	window := state.Windows[request.IP]
	if window == nil || now.Sub(window.First) > time.Minute {
		if len(state.Windows) >= 64 {
			clear(state.Windows)
			state.Incomplete = true
		}
		window = &RequestWindow{First: now, Paths: map[string]bool{}}
		state.Windows[request.IP] = window
	}
	window.Last = now
	window.Count++
	if len(window.Paths) < 64 {
		window.Paths[SafePath(request.Path)] = true
	}
	if request.Status == 401 || request.Status == 403 {
		window.Failures++
	}
	if !request.Trusted {
		if category == "" && len(window.Paths) >= 60 {
			category = "enumeration"
		}
		if category == "" && window.Failures >= 10 {
			category = "authentication-failures"
		}
		if category == "" && window.Count >= 180 {
			category = "rapid-crawl"
		}
		if category == "" && class == "scanner" {
			category = "scanner-client"
		}
	}
	if category != "" {
		state.recordIncident(request, category, class)
	}
	agent := class
	if class != "unknown" {
		agent = CleanText(request.Agent, 80)
	}
	key := strings.Join([]string{dayKey(now), class, agent, SafePath(request.Path), CleanText(request.Language, 16)}, "\x1f")
	daily := 0
	if state.Groups[key] == nil {
		for _, group := range state.Groups {
			if group.Date == dayKey(now) {
				daily++
			}
		}
	}
	if state.Groups[key] == nil && daily >= 64 {
		key = dayKey(now) + "\x1f(other)"
		state.Incomplete = true
	}
	group := state.Groups[key]
	if group == nil {
		group = &RequestGroup{Date: dayKey(now), Class: class, Agent: agent, Path: SafePath(request.Path), Language: CleanText(request.Language, 16)}
		if strings.HasSuffix(key, "(other)") {
			group.Class = "unknown"
			group.Agent = "(other)"
			group.Path = "(other)"
			group.Language = "unknown"
		}
		state.Groups[key] = group
	}
	group.Count++
	if request.Status >= 400 {
		group.Errors++
	}
	if now.Sub(previous) > time.Minute || previous.UTC().Minute() != now.UTC().Minute() {
		state.Prune(now)
	}
	return category
}
func (state *SecurityState) recordIncident(request RequestObservation, category, class string) {
	index := -1
	for position := len(state.Incidents) - 1; position >= 0; position-- {
		candidate := state.Incidents[position]
		if candidate.IP == request.IP && request.Time.Sub(candidate.Last) <= 30*time.Minute {
			index = position
			break
		}
	}
	if index < 0 {
		state.Incidents = append(state.Incidents, Incident{IP: CleanText(request.IP, 64), Country: CleanText(request.Country, 64), City: CleanText(request.City, 64), Agent: CleanText(request.Agent, 128), Class: class, First: request.Time, Categories: map[string]int{}})
		index = len(state.Incidents) - 1
	}
	incident := &state.Incidents[index]
	incident.Last = request.Time
	incident.Count++
	incident.Categories[category]++
	if (category == "secret" || category == "repository" || category == "source-backup") && (request.Status == 200 || request.Status == 206) {
		incident.PossibleExposure = true
	}
	pathname := SafePath(request.Path)
	for position := range incident.Examples {
		example := &incident.Examples[position]
		if example.Path == pathname && example.Status == request.Status && example.Category == category {
			example.Count++
			example.Last = request.Time
			example.Bytes += request.Bytes
			return
		}
	}
	if len(incident.Examples) < 20 {
		incident.Examples = append(incident.Examples, Probe{Path: pathname, Category: category, Method: request.Method, Status: request.Status, Count: 1, First: request.Time, Last: request.Time, Bytes: request.Bytes})
	}
}
func (state *SecurityState) Prune(now time.Time) {
	retained := state.Incidents[:0]
	for _, incident := range state.Incidents {
		if now.Sub(incident.Last) <= 30*24*time.Hour {
			retained = append(retained, incident)
		}
	}
	state.Incidents = retained
	if len(state.Incidents) > 500 {
		state.Incidents = append([]Incident(nil), state.Incidents[len(state.Incidents)-500:]...)
		state.Incomplete = true
	}
	for key, group := range state.Groups {
		if group.Date < dayKey(now.AddDate(0, 0, -89)) {
			delete(state.Groups, key)
		}
	}
	for key, window := range state.Windows {
		if now.Sub(window.Last) > time.Minute {
			delete(state.Windows, key)
		}
	}
}
func (state *SecurityState) Report(now time.Time, days int) SecurityReport {
	result := SecurityReport{Incomplete: state.Incomplete}
	cutoff := dayKey(now.AddDate(0, 0, -days+1))
	for _, group := range state.Groups {
		if group.Date >= cutoff && group.Date <= dayKey(now) {
			result.Groups = append(result.Groups, *group)
			result.Requests += group.Count
			result.Errors += group.Errors
		}
	}
	for _, incident := range state.Incidents {
		if dayKey(incident.Last) >= cutoff {
			result.Incidents = append(result.Incidents, incident)
			result.Suspicious++
		}
	}
	sort.Slice(result.Groups, func(i, j int) bool { return result.Groups[i].Count > result.Groups[j].Count })
	sort.Slice(result.Incidents, func(i, j int) bool {
		left, right := result.Incidents[i], result.Incidents[j]
		if left.PossibleExposure != right.PossibleExposure {
			return left.PossibleExposure
		}
		return left.Last.After(right.Last)
	})
	return result
}
func (incident Incident) Reasons() string {
	labels := []string{}
	for category, count := range incident.Categories {
		labels = append(labels, fmt.Sprintf("%s × %d", category, count))
	}
	sort.Strings(labels)
	return strings.Join(labels, ", ")
}

// Limits apply before persistence as well as on disk. Detail is sacrificed first.
func (state *SecurityState) Limit(budget int) {
	estimate := func() int {
		total := len(state.Groups) * 512
		for _, incident := range state.Incidents {
			total += 1024 + len(incident.Examples)*512
		}
		return total
	}
	for estimate() > budget && len(state.Incidents) > 0 {
		state.Incidents = state.Incidents[1:]
		state.Incomplete = true
	}
	for estimate() > budget && len(state.Groups) > 0 {
		oldest := ""
		for key := range state.Groups {
			if oldest == "" || key < oldest {
				oldest = key
			}
		}
		delete(state.Groups, oldest)
		state.Incomplete = true
	}
}
