package hostingandsupport

import (
	"fmt"
	"testing"
)

func TestInstallationCatalogPaginationKeepsArchiveOffInitialPage(t *testing.T) {
	rows := []InstallationCatalogView{{ID: "live", Name: "Live", SiteCount: 2}}
	for i := 0; i < 1153; i++ {
		rows = append(rows, InstallationCatalogView{ID: fmt.Sprintf("archive-%04d", i), Archived: true})
	}
	live := PaginateInstallations(rows, "active", "sites", "", 1, 40)
	if live.Total != 1154 || live.Matching != 1 || len(live.Rows) != 1 || live.Rows[0].ID != "live" {
		t.Fatalf("active page rendered archived installations: %+v", live)
	}
	archive := PaginateInstallations(rows, "archived", "name", "", 2, 40)
	if archive.Matching != 1153 || archive.Pages != 29 || archive.Page != 2 || len(archive.Rows) != 40 || archive.Rows[0].ID != "archive-0040" {
		t.Fatalf("archive page incorrect: %+v", archive)
	}
	filtered := PaginateInstallations(rows, "all", "name", "archive-1152", 1, 40)
	if filtered.Matching != 1 || filtered.Rows[0].ID != "archive-1152" {
		t.Fatalf("catalog search incomplete: %+v", filtered)
	}
}

func TestCatalogPaginationPreservesCurrencyGroupingAndBounds(t *testing.T) {
	rows := []InstallationCatalogView{
		{ID: "usd", Currency: "USD", MonthlyForecastMinor: 999999, MonetizationEnabled: true},
		{ID: "eur", Currency: "EUR", MonthlyForecastMinor: 1, MonetizationEnabled: true},
		{ID: "old", Currency: "EUR", Archived: true, MonetizationEnabled: true},
	}
	page := PaginateInstallations(rows, "monetized", "forecast", "", 1000000, 40)
	if page.Matching != 2 || page.Pages != 1 || page.Page != 1 || page.Rows[0].ID != "eur" || page.Rows[1].ID != "usd" {
		t.Fatalf("mixed currencies incorrectly compared: %+v", page)
	}
	empty := PaginateInstallations(rows, "archived", "sites", "nonexistent", -3, 40)
	if empty.Matching != 0 || empty.Page != 1 || empty.Pages != 1 || len(empty.Rows) != 0 {
		t.Fatalf("empty page should be safe: %+v", empty)
	}
}
