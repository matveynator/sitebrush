package analytics

import (
	"sort"
	"time"
)

type AISecurityDay struct {
	Date             string `json:"date"`
	Requests         int    `json:"requests"`
	Errors           int    `json:"errors"`
	SuspiciousEvents int    `json:"suspicious_events"`
	MaximumSeverity  int    `json:"maximum_severity"`
}

type AISecurityType struct {
	Category string `json:"category"`
	Count    int    `json:"count"`
}

// This allowlist DTO never serializes incidents, request groups or addresses.
// SecurityReport retains longer windows for some dashboard widgets, so each
// aggregate is independently bounded to the requested period here.
type AISecuritySummary struct {
	Domain           string           `json:"domain"`
	Days             int              `json:"days"`
	PeriodStart      time.Time        `json:"period_start"`
	PeriodEnd        time.Time        `json:"period_end"`
	Available        bool             `json:"available"`
	Incomplete       bool             `json:"incomplete"`
	Requests         int              `json:"requests"`
	Errors           int              `json:"errors"`
	SuspiciousEvents int              `json:"suspicious_events"`
	BlockedRequests  *int             `json:"blocked_requests"`
	Daily            []AISecurityDay  `json:"daily"`
	Types            []AISecurityType `json:"types"`
	TotalDays        int              `json:"total_days"`
	Truncated        bool             `json:"truncated"`
	Coverage         string           `json:"coverage"`
}

func AISecurityView(report SecurityReport, domain string, now time.Time, days int, available bool, limit, offset int) AISecuritySummary {
	now = now.UTC()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, -days+1)
	summary := AISecuritySummary{Domain: domain, Days: days, PeriodStart: start, PeriodEnd: now, Available: available, Incomplete: report.Incomplete, Daily: []AISecurityDay{}, Types: []AISecurityType{}, Coverage: "Observed request counts and suspicious events. Severity is the maximum observed per day (1..4), not an exposure finding. Complete blocked-request totals are not measured."}
	byDay := map[string]AISecurityDay{}
	first, last := start.Format("2006-01-02"), now.Format("2006-01-02")
	inPeriod := func(date string) bool {
		parsed, err := time.Parse("2006-01-02", date)
		return err == nil && parsed.Format("2006-01-02") == date && date >= first && date <= last
	}
	for _, group := range report.Groups {
		if !inPeriod(group.Date) {
			continue
		}
		day := byDay[group.Date]
		day.Date = group.Date
		day.Requests += group.Count
		day.Errors += group.Errors
		byDay[group.Date] = day
		summary.Requests += group.Count
		summary.Errors += group.Errors
	}
	types := map[string]int{}
	for date, count := range report.ActivityDays {
		if !inPeriod(date) {
			continue
		}
		day := byDay[date]
		day.Date = date
		day.SuspiciousEvents = count
		day.MaximumSeverity = report.ActivitySeverity[date]
		byDay[date] = day
		summary.SuspiciousEvents += count
		for category, total := range report.ActivityTypes[date] {
			switch category {
			case "traversal", "sitebrush-exploit", "repository", "secret", "cms", "source-backup", "injection", "admin-discovery", "authentication-failures", "scanner-client":
			default:
				category = "other"
			}
			types[category] += total
		}
	}
	dates := make([]string, 0, len(byDay))
	for date := range byDay {
		dates = append(dates, date)
	}
	sort.Strings(dates)
	summary.TotalDays = len(dates)
	for index, date := range dates {
		if index >= offset && index < offset+limit {
			summary.Daily = append(summary.Daily, byDay[date])
		}
	}
	summary.Truncated = offset+len(summary.Daily) < len(dates)
	for category, count := range types {
		summary.Types = append(summary.Types, AISecurityType{Category: category, Count: count})
	}
	sort.Slice(summary.Types, func(left, right int) bool { return summary.Types[left].Category < summary.Types[right].Category })
	return summary
}
