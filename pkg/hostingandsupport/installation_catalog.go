package hostingandsupport

import (
	"sort"
	"strings"

	"github.com/matveynator/sitebrush/v2/pkg/expenses"
)

// InstallationCatalogView is a read-only, server-scoped snapshot for the
// central administrator. All device kinds share one catalog and one identity.
type InstallationCatalogView struct {
	ID                    string
	Name                  string
	OwnerEmail            string
	OSLabel               string
	SiteCount             int
	MonetizationEnabled   bool
	MonthlyForecastMinor  int64
	MonthlyForecastLabel  string
	PaidThisMonthMinor    int64
	PaidThisMonthLabel    string
	Currency              string
	LastSeenAt            string
	Archived              bool
	Qualified             bool
}

// InstallationCatalogViews unifies qualified servers, desktops and archive
// entries without merging by email, IP or domain. InstallationID is the key.
// The caller must check central-registry authorization before rendering these.
func InstallationCatalogViews(servers []ServerView, desktopGroups []DesktopHostingGroup, archived []ClientHosting) []InstallationCatalogView {
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
		add(InstallationCatalogView{
			ID: server.ID, Name: firstNonEmpty(server.Name, server.ID),
			OwnerEmail: server.OwnerEmail, OSLabel: server.OSLabel,
			SiteCount: server.SiteCount, MonetizationEnabled: server.MonetizationEnabled,
			MonthlyForecastMinor: server.MonthlyForecastMinor,
			MonthlyForecastLabel: MoneyLabel(server.MonthlyForecastMinor, currency),
			PaidThisMonthMinor: server.PaidThisMonthMinor,
			PaidThisMonthLabel: MoneyLabel(server.PaidThisMonthMinor, currency),
			Currency: currency, Qualified: true, LastSeenAt: server.SyncStatusLabel,
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
		if views[i].MonthlyForecastMinor != views[j].MonthlyForecastMinor {
			return views[i].MonthlyForecastMinor > views[j].MonthlyForecastMinor
		}
		return views[i].ID < views[j].ID
	})
	return views
}
