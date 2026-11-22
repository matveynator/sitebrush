package hostingandsupport

import (
	"sort"
	"strings"
	"time"

	"github.com/matveynator/sitebrush/v2/pkg/expenses"
)

// InstallationCatalogView is a read-only, server-scoped snapshot for the
// central administrator. All device kinds share one catalog and one identity.
type InstallationCatalogView struct {
	ID                   string
	Name                 string
	OwnerEmail           string
	OSLabel              string
	SiteCount            int
	MonetizationEnabled  bool
	MonthlyForecastMinor int64
	MonthlyForecastLabel string
	PaidThisMonthMinor   int64
	PaidThisMonthLabel   string
	Currency             string
	LastSeenAt           string
	Archived             bool
	Qualified            bool
	ConnectionStatus     string
}

// InstallationCatalogViews unifies qualified servers, desktops and archive
// entries without merging by email, IP or domain. InstallationID is the key.
// The caller must check central-registry authorization before rendering these.
func InstallationCatalogViews(servers []ServerView, desktopGroups []DesktopHostingGroup, archived []ClientHosting) []InstallationCatalogView {
	return installationCatalogViewsAt(servers, desktopGroups, archived, time.Now().UTC())
}

func installationCatalogViewsAt(servers []ServerView, desktopGroups []DesktopHostingGroup, archived []ClientHosting, now time.Time) []InstallationCatalogView {
	views := make([]InstallationCatalogView, 0, len(servers))
	seen := make(map[string]struct{})
	add := func(view InstallationCatalogView) {
		view.ID = strings.TrimSpace(view.ID)
		if view.ID == "" {
			return
		}
		if _, exists := seen[view.ID]; exists {
			return
		}
		seen[view.ID] = struct{}{}
		views = append(views, view)
	}
	for _, server := range servers {
		currency := firstNonEmpty(server.BillingCurrency, "EUR")
		forecast := server.MonthlyForecastMinor
		if server.Archived {
			forecast = 0
		}
		add(InstallationCatalogView{
			ID: server.ID, Name: firstNonEmpty(server.Name, server.ID),
			OwnerEmail: server.OwnerEmail, OSLabel: server.OSLabel,
			SiteCount: server.SiteCount, MonetizationEnabled: server.MonetizationEnabled && !server.Archived,
			MonthlyForecastMinor: forecast,
			MonthlyForecastLabel: MoneyLabel(forecast, currency),
			PaidThisMonthMinor:   server.PaidThisMonthMinor,
			PaidThisMonthLabel:   MoneyLabel(server.PaidThisMonthMinor, currency),
			Currency:             currency, Qualified: server.Local || server.Qualified, LastSeenAt: server.LastSeenAt,
			Archived: server.Archived, ConnectionStatus: installationConnectionStatus(server.Local, server.Qualified, server.Archived, server.LastSeenAt, now),
		})
	}
	fromHosting := func(hosting ClientHosting, isArchived bool) InstallationCatalogView {
		policy := expenses.NormalizePolicy(hosting.ExpensePolicy)
		if policy.InstallationID == "" {
			policy = expenses.DefaultServerPolicy(hosting.InstallationID, hosting.DiskTotalBytes)
		}
		usages := make([]expenses.SiteUsage, 0, len(hosting.Sites))
		count := 0
		for _, site := range hosting.Sites {
			if site.IsDemo {
				continue
			}
			count++
			usages = append(usages, expenses.SiteUsage{Key: site.Domain, UsedBytes: site.UsedBytes})
		}
		forecast := int64(0)
		if policy.MonetizationEnabled() && !isArchived {
			forecast = expenses.AllocateMonthlyExpense(policy, usages).AllocatedMinor
		}
		currency := firstNonEmpty(policy.Currency, hosting.BillingCurrency, "EUR")
		return InstallationCatalogView{
			ID: hosting.InstallationID, Name: firstNonEmpty(hosting.ServerDomain, hosting.InstallationID),
			OwnerEmail: hosting.OwnerEmail, OSLabel: strings.TrimSpace(hosting.OSName + " " + hosting.OSVersion),
			SiteCount: count, MonetizationEnabled: policy.MonetizationEnabled() && !isArchived,
			MonthlyForecastMinor: forecast, MonthlyForecastLabel: MoneyLabel(forecast, currency),
			PaidThisMonthLabel: MoneyLabel(0, currency), Currency: currency,
			LastSeenAt: hosting.LastSeenAt, Archived: isArchived, Qualified: hosting.Qualified,
			ConnectionStatus: installationConnectionStatus(false, hosting.Qualified, isArchived, hosting.LastSeenAt, now),
		}
	}
	for _, group := range desktopGroups {
		for _, hosting := range group.Hostings {
			add(fromHosting(hosting, false))
		}
	}
	for _, hosting := range archived {
		add(fromHosting(hosting, true))
	}
	sort.Slice(views, func(i, j int) bool {
		if views[i].Archived != views[j].Archived {
			return !views[i].Archived
		}
		if views[i].SiteCount != views[j].SiteCount {
			return views[i].SiteCount > views[j].SiteCount
		}
		// Monetary amounts are comparable only within the same currency.
		// Keep the initial server order consistent with the client-side sorter.
		if views[i].Currency != views[j].Currency {
			return views[i].Currency < views[j].Currency
		}
		if views[i].MonthlyForecastMinor != views[j].MonthlyForecastMinor {
			return views[i].MonthlyForecastMinor > views[j].MonthlyForecastMinor
		}
		return views[i].ID < views[j].ID
	})
	return views
}

// Connectivity and qualification are independent of monetization. The same
// freshness interval is used by snapshot qualification and by this catalog.
func installationConnectionStatus(local, qualified, archived bool, lastSeenAt string, now time.Time) string {
	if archived {
		return "archived"
	}
	if local {
		return "connected"
	}
	seenAt, valid := parseHostingTime(lastSeenAt)
	if !valid || now.Sub(seenAt) > HostingSnapshotFreshness {
		return "disconnected"
	}
	if !qualified {
		return "unqualified"
	}
	return "connected"
}
