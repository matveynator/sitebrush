package hostingandsupport

import (
	"sort"
	"strings"
)

// InstallationPage is a detached, read-only page of one authorized central
// catalog. Filtering and sorting precede pagination; browser HTML stays bounded.
type InstallationPage struct {
	Rows                         []InstallationCatalogView
	Total, Matching, Page, Pages int
	Filter, Sort, Query          string
}

func PaginateInstallations(catalog []InstallationCatalogView, filter, order, query string, page, pageSize int) InstallationPage {
	switch filter {
	case "active", "connected", "disconnected", "unqualified", "monetized", "not-monetized", "archived", "all":
	default:
		filter = "active"
	}
	switch order {
	case "sites", "forecast", "paid", "name":
	default:
		order = "sites"
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 40
	}
	query = strings.TrimSpace(query)
	if len(query) > 160 {
		query = query[:160]
	}
	paged := InstallationPage{Total: len(catalog), Filter: filter, Sort: order, Query: query}
	search := strings.ToLower(query)
	rows := make([]InstallationCatalogView, 0, len(catalog))
	for _, item := range catalog {
		accepted := false
		switch filter {
		case "all":
			accepted = true
		case "archived":
			accepted = item.Archived
		case "active":
			accepted = !item.Archived
		case "connected", "disconnected", "unqualified":
			accepted = !item.Archived && item.ConnectionStatus == filter
		case "monetized":
			accepted = !item.Archived && item.MonetizationEnabled
		case "not-monetized":
			accepted = !item.Archived && !item.MonetizationEnabled
		}
		if accepted && (search == "" || strings.Contains(strings.ToLower(item.Name+" "+item.ID+" "+item.OwnerEmail+" "+item.OSLabel), search)) {
			rows = append(rows, item)
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		left, right := rows[i], rows[j]
		if order == "name" {
			if strings.ToLower(left.Name) != strings.ToLower(right.Name) {
				return strings.ToLower(left.Name) < strings.ToLower(right.Name)
			}
		} else {
			if order == "forecast" || order == "paid" {
				if left.Currency != right.Currency {
					return left.Currency < right.Currency
				}
				if order == "forecast" && left.MonthlyForecastMinor != right.MonthlyForecastMinor {
					return left.MonthlyForecastMinor > right.MonthlyForecastMinor
				}
				if order == "paid" && left.PaidThisMonthMinor != right.PaidThisMonthMinor {
					return left.PaidThisMonthMinor > right.PaidThisMonthMinor
				}
			} else if left.SiteCount != right.SiteCount {
				return left.SiteCount > right.SiteCount
			}
		}
		return left.ID < right.ID
	})
	paged.Matching = len(rows)
	paged.Pages = (len(rows) + pageSize - 1) / pageSize
	if paged.Pages < 1 {
		paged.Pages = 1
	}
	if page < 1 {
		page = 1
	}
	if page > paged.Pages {
		page = paged.Pages
	}
	paged.Page = page
	start := (page - 1) * pageSize
	paged.Rows = rows[start:min(start+pageSize, len(rows))]
	return paged
}
