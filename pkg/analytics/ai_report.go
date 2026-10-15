package analytics

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/url"
	"strings"
	"time"
)

// AIReport is an allowlist of aggregates, independent of the administrator
// dashboard's visitor maps, session replays and future diagnostic fields.
type AIReport struct {
	Version                   int            `json:"version"`
	Domain                    string         `json:"domain"`
	Available                 bool           `json:"available"`
	ConversionGoalsConfigured bool           `json:"conversion_goals_configured"`
	PeriodStart               time.Time      `json:"period_start"`
	PeriodEnd                 time.Time      `json:"period_end"`
	Generated                 time.Time      `json:"generated_at"`
	Days                      int            `json:"days"`
	Incomplete                bool           `json:"incomplete"`
	AudienceSince             *time.Time     `json:"audience_since"`
	AudienceIncomplete        bool           `json:"audience_incomplete"`
	Truncated                 bool           `json:"truncated"`
	Limit                     int            `json:"limit"`
	Offset                    int            `json:"offset"`
	Totals                    map[string]int `json:"totals"`
	Summary                   AISummary      `json:"summary"`
	Sections                  map[string]any `json:"sections"`
	Unavailable               []string       `json:"unavailable_signals"`
	Interpretation            []string       `json:"interpretation"`
}

type AISummary struct {
	Visitors       *int `json:"visitors"`
	Sessions       *int `json:"sessions"`
	PageViews      *int `json:"page_views"`
	ReturningUsers *int `json:"returning_users"`
}

type AIImpact struct {
	Page                string    `json:"page"`
	At                  time.Time `json:"changed_at"`
	BeforeStart         time.Time `json:"before_start"`
	BeforeEnd           time.Time `json:"before_end"`
	AfterStart          time.Time `json:"after_start"`
	AfterEnd            time.Time `json:"after_end"`
	BeforeUsers         int       `json:"before_users"`
	BeforeCompleted     int       `json:"before_completed"`
	AfterUsers          int       `json:"after_users"`
	AfterCompleted      int       `json:"after_completed"`
	Partial             bool      `json:"partial"`
	SmallSample         bool      `json:"small_sample"`
	OtherChanges        bool      `json:"other_changes"`
	ConversionAvailable bool      `json:"conversion_available"`
	OutcomeWindowDays   int       `json:"outcome_window_days"`
	ObservedThrough     time.Time `json:"outcomes_observed_through"`
}

// AIIdentifier keeps campaign and DOM identifiers useful for grouping without
// exposing arbitrary user-provided labels or form field contents.
func AIIdentifier(identifier string) string {
	if identifier == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(identifier))
	return hex.EncodeToString(digest[:])
}

func AIReportView(report Report, domain, operation, page string, available bool, limit, offset int) AIReport {
	if limit < 1 || limit > 50 {
		limit = 20
	}
	if offset < 0 {
		offset = 0
	}
	result := AIReport{Version: 1, Domain: domain, Available: available, PeriodStart: report.PeriodStart, PeriodEnd: report.PeriodEnd, Generated: report.Generated, Days: report.Days, Incomplete: report.Incomplete || report.Conversion.Incomplete || report.Experience.Incomplete, Limit: limit, Offset: offset, Totals: map[string]int{}, Sections: map[string]any{}, Unavailable: []string{"search_engine_queries", "internal_search_terms", "form_field_values", "causal_attribution"}, Interpretation: []string{
		"Counts describe observed browser activity; visitors are browser identities, not verified people.",
		"Missing or unavailable measurements are null, not zero. Incomplete collection may undercount.",
		"Sessions join browser activity with the same session identity across tabs until 30 minutes of inactivity; counts include sessions active in the period.",
		"Returning users had an earlier observed session. Page returns require distinct sessions visiting that page within the period; same-day means UTC day, not inferred purchase intent.",
		"Audience coverage starts at audience_since; earlier sessions cannot be reconstructed. Historical form attempts without a recorded device remain unknown.",
		"Funnel Pending is an open 90-day conversion window; Exited is observed inactivity, not final loss.",
		"Forms completed require configured observed success; submissions alone do not prove success.",
		"Page impacts are correlations over the explicit comparison windows, not causal evidence.",
		"DOM identifiers and campaigns are SHA-256 hashes; referrers contain origins only.",
	}}
	if !available {
		result.Unavailable = append(result.Unavailable, "browser_analytics")
		return result
	}
	conversion := report.Conversion
	result.ConversionGoalsConfigured = len(conversion.Funnels) > 0
	if conversion.Version == 2 {
		result.Summary.Visitors = &conversion.Visitors
		// Sampled replays cannot serve as session counts. Only page observations
		// contribute views; report snapshots from before this aggregate omit it.
		if conversion.PageAudience != nil {
			views := 0
			for _, audience := range conversion.PageAudience {
				views += audience.PageViews
			}
			result.Summary.PageViews = &views
		}
		if conversion.Audience != nil {
			result.Summary.Sessions = &conversion.Audience.Sessions
			result.Summary.ReturningUsers = &conversion.Audience.ReturningUsers
			result.AudienceSince = &conversion.Audience.Since
			result.AudienceIncomplete = conversion.Audience.Incomplete
			result.Incomplete = result.Incomplete || result.AudienceIncomplete
		} else {
			result.Unavailable = append(result.Unavailable, "total_sessions", "site_returning_users", "same_day_page_returns")
		}
		if conversion.PageAudience == nil {
			result.Unavailable = append(result.Unavailable, "page_audience", "page_views")
		}
		if !result.ConversionGoalsConfigured {
			result.Unavailable = append(result.Unavailable, "conversion_outcomes")
		}
	} else {
		result.Summary = AISummary{Visitors: &report.Visitors, Sessions: &report.Sessions, PageViews: &report.Views, ReturningUsers: &report.Returning}
		result.Unavailable = append(result.Unavailable, "conversion_funnels", "page_audience", "page_behaviour", "forms", "page_impacts", "conversion_insights")
	}
	if operation == "get_analytics_summary" {
		return result
	}
	include := func(section string) bool {
		return operation == "get_site_report" || operation == "get_"+section || section == "pages" && operation == "get_page_behaviour"
	}
	if include("pages") {
		pages := []PageAudience{}
		for _, audience := range conversion.PageAudience {
			if page == "" || audience.Page == page {
				audience.Page = aiSafePath(audience.Page)
				pages = append(pages, audience)
			}
		}
		result.Sections["pages"] = aiRows(&result, "pages", pages)
	}
	if include("funnels") {
		funnels := []map[string]any{}
		for _, funnel := range conversion.Funnels {
			steps := []map[string]any{}
			for _, step := range funnel.Steps {
				steps = append(steps, map[string]any{"name": CleanText(step.Name, 160), "users": step.Users, "pending": step.Pending, "lost": step.Lost, "exited": step.Exited, "transition": step.Transition, "drop": step.Drop})
			}
			funnels = append(funnels, map[string]any{"key": funnel.Key, "name": CleanText(funnel.Name, 160), "primary": funnel.Primary, "users": funnel.Users, "completed": funnel.Completed, "rate": funnel.Rate, "steps": steps})
		}
		result.Sections["funnels"] = aiRows(&result, "funnels", funnels)
	}
	if include("paths") {
		paths := []map[string]any{}
		var walk func([]*ConversionPath)
		walk = func(nodes []*ConversionPath) {
			for _, node := range nodes {
				if node == nil {
					continue
				}
				copy := *node
				copy.Children = nil
				copy.Root, copy.Label = aiSafePath(copy.Root), aiSafePath(copy.Label)
				steps := strings.Split(copy.Trail, " → ")
				for index := range steps {
					steps[index] = aiSafePath(steps[index])
				}
				copy.Trail = strings.Join(steps, " → ")
				paths = append(paths, map[string]any{"root": copy.Root, "trail": copy.Trail, "page": copy.Label, "users": copy.Users, "completed": copy.Completed, "entrances": copy.Entrances, "exits": copy.Exits, "share": copy.Share, "conversion": copy.Conversion, "exit_rate": copy.ExitRate})
				walk(node.Children)
			}
		}
		walk(conversion.Paths)
		result.Sections["paths"] = aiRows(&result, "paths", paths)
	}
	if include("page_behaviour") {
		behaviour := []map[string]any{}
		for _, observation := range conversion.Behaviour {
			if page != "" && observation.Page != page {
				continue
			}
			behaviour = append(behaviour, map[string]any{"page": aiSafePath(observation.Page), "layout": observation.Layout, "device": aiDeviceName(observation.Device), "visitors": observation.Visitors, "elements": aiBehaviourRows(&result, observation.Elements), "scroll": aiBehaviourRows(&result, observation.Scroll), "sections": aiBehaviourRows(&result, observation.Sections)})
		}
		result.Sections["page_behaviour"] = aiRows(&result, "page_behaviour", behaviour)
	}
	if include("forms") {
		forms := []map[string]any{}
		for _, form := range conversion.Forms {
			if page != "" && form.Page != page {
				continue
			}
			fields := []map[string]any{}
			for _, field := range form.Fields[:min(len(form.Fields), 50)] {
				fields = append(fields, map[string]any{"field_id": AIIdentifier(field.Label), "users": field.Count})
			}
			if len(form.Fields) > 50 {
				result.Truncated = true
			}
			devices := []map[string]any{}
			for _, device := range form.Devices {
				devices = append(devices, map[string]any{"device": aiDeviceName(device.Device), "viewed": device.Viewed, "started": device.Started, "submitted": device.Submitted, "completed": device.Completed, "failed": device.Failed, "abandoned": device.Abandoned})
			}
			forms = append(forms, map[string]any{"page": aiSafePath(form.Page), "form_id": AIIdentifier(form.Form), "viewed": form.Viewed, "started": form.Started, "submitted": form.Submitted, "completed": form.Completed, "failed": form.Failed, "abandoned": form.Abandoned, "last_fields": fields, "devices": devices, "devices_available": len(form.Devices) > 0})
		}
		result.Sections["forms"] = aiRows(&result, "forms", forms)
	}
	if include("sources") {
		sources := []map[string]any{}
		for _, source := range conversion.Referrers {
			if page != "" && source.Landing != page {
				continue
			}
			sources = append(sources, aiSourceRow(source))
		}
		result.Sections["sources"] = aiRows(&result, "sources", sources)
		devices := []map[string]any{}
		for _, device := range conversion.Devices {
			devices = append(devices, map[string]any{"name": aiDeviceName(device.Name), "visitors": device.Visitors, "completed": device.Completed})
		}
		result.Sections["devices"] = aiRows(&result, "devices", devices)
		cohorts := []map[string]any{}
		for _, cohort := range conversion.Cohorts {
			weeks := []map[string]any{}
			for _, week := range cohort.Weeks {
				weeks = append(weeks, map[string]any{"eligible": week.Eligible, "returned": week.Returned, "rate": week.Rate, "partial": week.Partial})
			}
			cohorts = append(cohorts, map[string]any{"week": cohort.Week, "visitors": cohort.Visitors, "weeks": weeks})
		}
		result.Sections["cohorts"] = aiRows(&result, "cohorts", cohorts)
	}
	if include("page_impacts") {
		impacts := []AIImpact{}
		for _, impact := range conversion.Impacts {
			if page != "" && impact.Page != page || impact.At.Before(report.PeriodStart) || impact.At.After(report.PeriodEnd) {
				continue
			}
			afterEnd := impact.At.Add(ImpactWindow)
			if afterEnd.After(report.PeriodEnd) {
				afterEnd = report.PeriodEnd
			}
			impacts = append(impacts, AIImpact{Page: aiSafePath(impact.Page), At: impact.At, BeforeStart: impact.At.Add(-ImpactWindow), BeforeEnd: impact.At, AfterStart: impact.At, AfterEnd: afterEnd, BeforeUsers: impact.BeforeVisitors, BeforeCompleted: impact.BeforeCompleted, AfterUsers: impact.AfterVisitors, AfterCompleted: impact.AfterCompleted, OutcomeWindowDays: 7, ObservedThrough: report.Generated, ConversionAvailable: impact.BeforeRate != "" && impact.BeforeRate != "—", Partial: impact.Partial, SmallSample: impact.SmallSample, OtherChanges: impact.OtherChanges})
		}
		result.Sections["page_impacts"] = aiRows(&result, "page_impacts", impacts)
	}
	if include("insights") {
		insights := []map[string]any{}
		for _, insight := range conversion.Insights {
			insights = append(insights, aiInsightRow(insight, conversion))
		}
		result.Sections["insights"] = aiRows(&result, "insights", insights)
	}

	return result
}

func aiRows[T any](report *AIReport, section string, rows []T) []T {
	report.Totals[section] = len(rows)
	start := min(report.Offset, len(rows))
	end := min(start+report.Limit, len(rows))
	if end < len(rows) || start > 0 {
		report.Truncated = true
	}
	return rows[start:end]
}

func aiBehaviourRows(report *AIReport, rows []BehaviourRow) []map[string]any {
	if len(rows) > 50 {
		rows = rows[:50]
		report.Truncated = true
	}
	result := []map[string]any{}
	for _, row := range rows {
		result = append(result, map[string]any{"element_id": AIIdentifier(row.Element), "kind": row.Kind, "users": row.Users, "clicks": row.Clicks, "rate": row.Rate, "depth": row.Depth})
	}
	return result
}

func aiDeviceName(device string) string {
	switch device {
	case "mobile", "desktop", "tablet":
		return device
	default:
		return "unknown"
	}
}

func aiSourceRow(source ConversionSource) map[string]any {
	referrerOrigin := ""
	if parsed, err := url.Parse(source.Referrer); err == nil && (parsed.Scheme == "https" || parsed.Scheme == "http") && net.ParseIP(parsed.Hostname()) == nil {
		referrerOrigin = parsed.Scheme + "://" + parsed.Host
	}
	kind := source.Kind
	switch kind {
	case "direct", "search", "social", "referral", "campaign", "app", "internal", "unknown":
	default:
		kind = "unknown"
	}
	label := source.Name
	if source.Campaign != "" {
		label += " · " + source.Campaign
	}
	return map[string]any{"source_id": AIIdentifier(label), "kind": kind, "referrer_origin": referrerOrigin, "campaign_id": AIIdentifier(source.Campaign), "landing": aiSafePath(source.Landing), "visitors": source.Visitors, "completed": source.Completed, "rate": source.Rate}
}

func aiInsightRow(insight ConversionInsight, report ConversionReport) map[string]any {
	result := map[string]any{"kind": insight.Kind, "subject_id": AIIdentifier(insight.Subject), "compared_id": AIIdentifier(insight.Compared), "users": insight.Users, "rate": insight.Rate, "factor": insight.Factor}
	evidence := []map[string]any{}
	switch insight.Kind {
	case "device":
		for _, device := range report.Devices {
			evidence = append(evidence, map[string]any{"device": aiDeviceName(device.Name), "users": device.Visitors, "completed": device.Completed})
		}
	case "funnel":
		for _, funnel := range report.Funnels {
			if !funnel.Primary {
				continue
			}
			for index := 0; index+1 < len(funnel.Steps); index++ {
				step := funnel.Steps[index]
				if funnel.Steps[index+1].Name == insight.Subject {
					evidence = append(evidence, map[string]any{"funnel": funnel.Key, "users": step.Users, "pending": step.Pending, "exited": step.Exited, "lost": step.Lost, "next_users": funnel.Steps[index+1].Users})
				}
			}
		}
	case "dead", "scroll":
		result["page"] = aiSafePath(insight.Subject)
		for _, page := range report.Behaviour {
			if page.Page != insight.Subject {
				continue
			}
			if insight.Kind == "scroll" && len(page.Scroll) > 0 {
				last := page.Scroll[len(page.Scroll)-1]
				evidence = append(evidence, map[string]any{"users": page.Visitors, "reached": last.Users, "depth": last.Depth})
			}
			if insight.Kind == "dead" {
				for _, element := range page.Elements {
					if element.Kind == "dead-click" {
						evidence = append(evidence, map[string]any{"users": page.Visitors, "clicked_users": element.Users, "element_id": AIIdentifier(element.Element)})
					}
				}
			}
		}
	case "source":
		for _, source := range report.Sources {
			label := source.Name
			if source.Campaign != "" {
				label += " · " + source.Campaign
			}
			if label == insight.Subject || label == insight.Compared {
				evidence = append(evidence, aiSourceRow(source))
			}
		}
	}
	if len(evidence) > 50 {
		evidence = evidence[:50]
	}
	result["evidence"] = evidence
	return result
}

// URLs can contain user-entered identifiers even when query strings were
// removed by collection. Keep ordinary content paths while hiding identity-like
// segments before exporting the aggregate to an external agent.
func aiSafePath(raw string) string {
	if raw == "" {
		return ""
	}
	segments := strings.Split(SafePath(raw), "/")
	for index, segment := range segments {
		decoded, err := url.PathUnescape(segment)
		if err != nil {
			decoded = segment
		}
		compact := strings.ReplaceAll(decoded, "-", "")
		_, hexErr := hex.DecodeString(compact)
		if strings.Contains(decoded, "@") || net.ParseIP(decoded) != nil || len(decoded) > 80 || len(compact) >= 24 && hexErr == nil {
			segments[index] = "redacted-" + AIIdentifier(decoded)[:12]
		}
	}
	return strings.Join(segments, "/")
}
