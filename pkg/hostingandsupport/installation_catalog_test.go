package hostingandsupport

import (
	"testing"

	"github.com/matveynator/sitebrush/v2/pkg/expenses"
)

func TestInstallationCatalogUnifiesServerDesktopAndArchiveByIdentity(t *testing.T) {
	servers := []ServerView{
		{ID:"local", Name:"sitebrush.com", SiteCount:4, MonetizationEnabled:true, MonthlyForecastMinor:2500, PaidThisMonthMinor:1000, BillingCurrency:"EUR", Local:true},
		{ID:"shared", Name:"qualified.example", SiteCount:2, MonetizationEnabled:true, MonthlyForecastMinor:900, BillingCurrency:"USD"},
	}
	desktop := ClientHosting{
		InstallationID:"desktop-a", ServerDomain:"desktop.example",
		InstallationKind:InstallationKindDesktop, OwnerEmail:"desktop@example.com",
		DiskTotalBytes:100*expenses.DecimalGigabyte,
		ExpensePolicy: expenses.ServerPolicy{InstallationID:"desktop-a", Mode:expenses.ModeActual, MonetizationMode:"enabled",
			DiskRatePer100GBMinor:1500, ActualMonthlyExpenseMinor:2000, Currency:"EUR",
			BillingCapacityPercent:30, DiskTotalBytes:100*expenses.DecimalGigabyte},
		Sites:[]ClientHostingSite{
			{Domain:"desktop.example",UsedBytes:10*expenses.DecimalGigabyte},
			{Domain:"demo.example",UsedBytes:2*expenses.DecimalGigabyte,IsDemo:true},
		},
	}
	archive := ClientHosting{InstallationID:"archive-a",ServerDomain:"old.example",Archived:true,
		Sites:[]ClientHostingSite{{Domain:"old.example",UsedBytes:10*expenses.DecimalGigabyte}}}
	groups := []DesktopHostingGroup{{Hostings:[]ClientHosting{
		{InstallationID:"shared",ServerDomain:"duplicate.example"},
		desktop,
	}}}
	catalog := InstallationCatalogViews(servers,groups,[]ClientHosting{archive})
	if len(catalog)!=4 {
		t.Fatalf("duplicate or missing installation: %+v",catalog)
	}
	if catalog[0].ID!="local" || catalog[0].SiteCount!=4 {
		t.Fatalf("wrong catalog order: %+v",catalog)
	}
	ids:=make(map[string]bool)
	for _, entry:=range catalog {
		if ids[entry.ID] {t.Fatalf("duplicated id %s",entry.ID)}
		ids[entry.ID]=true
		if entry.ID=="desktop-a" {
			if entry.SiteCount!=1||entry.MonthlyForecastMinor<=0||!entry.MonetizationEnabled {
				t.Fatalf("desktop isn't in unified catalog: %+v",entry)
			}
		}
		if entry.ID=="archive-a" && (!entry.Archived || entry.MonthlyForecastMinor!=0) {
			t.Fatalf("archived installation projected new earnings: %+v",entry)
		}
		if entry.ID=="shared" && entry.Name!="qualified.example" {
			t.Fatalf("unqualified desktop overwrote qualified server: %+v",entry)
		}
	}
}

func TestInstallationCatalogDoesNotChargeUnconfiguredDesktop(t *testing.T) {
	catalog:=InstallationCatalogViews(nil,[]DesktopHostingGroup{{Hostings:[]ClientHosting{{
		InstallationID:"new-desktop",ServerDomain:"new.example",DiskTotalBytes:100*expenses.DecimalGigabyte,
		Sites:[]ClientHostingSite{{Domain:"new.example",UsedBytes:5*expenses.DecimalGigabyte}},
	}}}},nil)
	if len(catalog)!=1 || catalog[0].MonetizationEnabled || catalog[0].MonthlyForecastMinor!=0 {
		t.Fatalf("new installation not disabled by default: %+v",catalog)
	}
}
