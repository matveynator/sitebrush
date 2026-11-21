package billingworkspace

import (
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/matveynator/sitebrush/v2/pkg/expenses"
)

func policy(cost int64) expenses.ServerPolicy {
	return expenses.ServerPolicy{
		InstallationID: "server-a", Mode: expenses.ModeActual,
		DiskRatePer100GBMinor: 1500, ActualMonthlyExpenseMinor: cost,
		Currency: "EUR", BillingCapacityPercent: 30,
		DiskTotalBytes: 1000 * expenses.DecimalGigabyte,
		FreeSiteThresholdBytes: 100 * expenses.DecimalMegabyte,
		EffectiveAt: "2026-11-21T00:00:00Z",
	}
}

func TestForecastRepricesOnlyFutureQuotes(t *testing.T) {
	month := time.Date(2026, time.November, 1, 0, 0, 0, 0, time.UTC)
	sites := []Site{
		{InstallationID: "server-a", CustomerID: "customer-a", Domain: "a.example", UsedBytes: 100 * expenses.DecimalGigabyte},
		{InstallationID: "server-a", CustomerID: "customer-b", Domain: "free.example", UsedBytes: 1},
		{InstallationID: "server-b", CustomerID: "customer-a", Domain: "other.example", UsedBytes: 90 * expenses.DecimalGigabyte},
	}
	extras := []Extra{
		{ID: "support", InstallationID: "server-a", CustomerID: "customer-a", Description: "Monthly webmaster support", AmountMinor: 900, Currency: "EUR", Frequency: "monthly", StartMonth: "2026-11"},
		{ID: "design", InstallationID: "server-a", CustomerID: "customer-a", Description: "One-time design", AmountMinor: 2500, Currency: "EUR", Frequency: "once", StartMonth: "2026-11"},
		{ID: "outside", InstallationID: "server-b", CustomerID: "customer-a", Description: "Other server", AmountMinor: 99999, Currency: "EUR", Frequency: "monthly", StartMonth: "2026-11"},
	}
	original, err := ProjectMonth(policy(20000), month, sites, extras)
	if err != nil { t.Fatal(err) }
	if original.TotalMinor != 10067 { t.Fatalf("initial forecast = %d, want 10067", original.TotalMinor) }
	if original.Customers[0].HostingMinor != 6667 || original.Customers[0].ServicesMinor != 3400 {
		t.Fatalf("unexpected allocation: %+v", original.Customers[0])
	}
	originalCopy := original
	updatedPolicy := policy(30000)
	updatedPolicy.EffectiveAt = "2026-11-22T00:00:00Z"
	future, err := ProjectMonth(updatedPolicy, month.AddDate(0, 1, 0), sites, extras)
	if err != nil { t.Fatal(err) }
	if future.TotalMinor != 10900 { t.Fatalf("updated forecast = %d, want 10900", future.TotalMinor) }
	if original.TotalMinor != 10067 || !reflect.DeepEqual(original, originalCopy) {
		t.Fatalf("repricing changed the original billing snapshot: %+v", original)
	}
	repeated, err := ProjectMonth(updatedPolicy, month.AddDate(0, 1, 0), sites, extras)
	if err != nil { t.Fatal(err) }
	if !reflect.DeepEqual(future, repeated) { t.Fatal("projection is not deterministic") }
	if future.PolicyVersion != updatedPolicy.EffectiveAt { t.Fatal("forecast did not retain tariff version") }
}

func TestForecastRecurringIntervalsAndCurrency(t *testing.T) {
	p := policy(20000)
	p.DiskTotalBytes = 0
	services := []Extra{
		{ID: "quarter", InstallationID: "server-a", CustomerID: "customer-a", Description: "Quarterly service", AmountMinor: 1100, Currency: "EUR", Frequency: "quarterly", StartMonth: "2026-01"},
		{ID: "year", InstallationID: "server-a", CustomerID: "customer-a", Description: "Annual service", AmountMinor: 3300, Currency: "EUR", Frequency: "yearly", StartMonth: "2026-01"},
	}
	for _, testcase := range []struct { month time.Month; want int64 }{
		{time.January, 4400}, {time.February, 0}, {time.April, 1100},
	} {
		forecast, err := ProjectMonth(p, time.Date(2026, testcase.month, 1, 0, 0, 0, 0, time.UTC), nil, services)
		if err != nil { t.Fatal(err) }
		if forecast.TotalMinor != testcase.want { t.Fatalf("%s got %d, want %d", testcase.month, forecast.TotalMinor, testcase.want) }
	}
	services[0].Currency = "USD"
	if _, err := ProjectMonth(p, time.Now(), nil, services); err == nil { t.Fatal("mixed-currency billing must fail explicitly") }
}

func TestForecastRejectsBadData(t *testing.T) {
	now := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	p := policy(20000)
	sites := []Site{{InstallationID: "server-a", CustomerID: "customer-a", Domain: "a.example", UsedBytes: 1000}}
	if _, err := ProjectMonth(p, now, append(sites, sites[0]), nil); err == nil { t.Fatal("duplicate sites must fail") }
	sites[0].UsedBytes = -1
	if _, err := ProjectMonth(p, now, sites, nil); err == nil { t.Fatal("negative usage must fail") }
	services := []Extra{{ID: "big", InstallationID: "server-a", CustomerID: "customer-a", Description: "Huge service", AmountMinor: math.MaxInt64, Quantity: 2, Currency: "EUR", Frequency: "monthly", StartMonth: "2026-11"}}
	if _, err := ProjectMonth(p, now, nil, services); err == nil { t.Fatal("minor unit overflow must fail") }
	services[0].AmountMinor = 100
	services[0].Quantity = 1
	services[0].StartMonth = "2026-13"
	if _, err := ProjectMonth(p, now, nil, services); err == nil { t.Fatal("invalid schedule must fail") }
}
