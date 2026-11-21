// Package billingworkspace builds deterministic, read-only forecasts for a server.
// It does not issue invoices or change the amount on an existing invoice.
package billingworkspace

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/matveynator/sitebrush/v2/pkg/expenses"
)

type Site struct {
	InstallationID string
	CustomerID     string
	Domain         string
	UsedBytes      int64
	Excluded       bool
}

type Extra struct {
	ID             string
	InstallationID string
	CustomerID     string
	Domain         string
	Description    string
	AmountMinor    int64
	Currency       string
	Quantity       int64
	Frequency      string // once, monthly, quarterly, yearly
	StartMonth     string // YYYY-MM
	EndMonth       string // YYYY-MM, inclusive, or empty
}

type Line struct {
	Key         string
	Domain      string
	Description string
	AmountMinor int64
}

type CustomerForecast struct {
	CustomerID     string
	HostingMinor   int64
	ServicesMinor  int64
	TotalMinor     int64
	Lines          []Line
}

type Forecast struct {
	InstallationID string
	PolicyVersion  string
	PeriodStart    string
	Currency       string
	TotalMinor     int64
	Customers      []CustomerForecast
}

// ProjectMonth derives a new projection each time from the supplied tariff version,
// measured full disk capacity, site usage and extra services. Only records belonging
// to the selected installation are read. No invoice or scheduled payment is mutated.
func ProjectMonth(policy expenses.ServerPolicy, month time.Time, sites []Site, extras []Extra) (Forecast, error) {
	policy = expenses.NormalizePolicy(policy)
	if err := policy.Validate(); err != nil {
		return Forecast{}, err
	}
	month = time.Date(month.UTC().Year(), month.UTC().Month(), 1, 0, 0, 0, 0, time.UTC)
	result := Forecast{
		InstallationID: policy.InstallationID,
		PolicyVersion: policy.EffectiveAt,
		PeriodStart: month.Format("2006-01"),
		Currency: policy.Currency,
	}
	// Disabled monetization suppresses all future charges, including optional
	// services; already issued invoice snapshots remain unchanged.
	if !policy.MonetizationEnabled() {
		return result, nil
	}
	usages := make([]expenses.SiteUsage, 0, len(sites))
	byCustomer := make(map[string]*CustomerForecast)
	customerForSite := make(map[string]string)
	seenSites := make(map[string]struct{})
	for _, site := range sites {
		if site.InstallationID != policy.InstallationID {
			continue
		}
		customerID, domain := strings.TrimSpace(site.CustomerID), strings.ToLower(strings.TrimSpace(site.Domain))
		if customerID == "" || domain == "" {
			return Forecast{}, fmt.Errorf("site must have a customer and domain")
		}
		if _, exists := seenSites[domain]; exists {
			return Forecast{}, fmt.Errorf("duplicate domain in installation: %s", domain)
		}
		if site.UsedBytes < 0 {
			return Forecast{}, fmt.Errorf("negative site usage: %s", domain)
		}
		seenSites[domain] = struct{}{}
		customerForSite[domain] = customerID
		usages = append(usages, expenses.SiteUsage{Key: domain, UsedBytes: site.UsedBytes, Excluded: site.Excluded})
		if byCustomer[customerID] == nil {
			byCustomer[customerID] = &CustomerForecast{CustomerID: customerID}
		}
	}
	allocation := expenses.AllocateMonthlyExpense(policy, usages)
	for _, site := range sites {
		domain := strings.ToLower(strings.TrimSpace(site.Domain))
		if site.InstallationID != policy.InstallationID || customerForSite[domain] != strings.TrimSpace(site.CustomerID) {
			continue
		}
		amount := allocation.Sites[domain].ExpenseShareMinor
		if amount <= 0 {
			continue
		}
		customer := byCustomer[strings.TrimSpace(site.CustomerID)]
		if err := addMoney(&customer.HostingMinor, amount); err != nil {
			return Forecast{}, err
		}
		customer.Lines = append(customer.Lines, Line{Key: "hosting:" + domain, Domain: domain, Description: "Hosting and storage", AmountMinor: amount})
	}
	seenExtras := make(map[string]struct{})
	for _, extra := range extras {
		if extra.InstallationID != policy.InstallationID {
			continue
		}
		if extra.ID == "" || strings.TrimSpace(extra.CustomerID) == "" || strings.TrimSpace(extra.Description) == "" {
			return Forecast{}, fmt.Errorf("extra service requires id, customer and description")
		}
		if _, exists := seenExtras[extra.ID]; exists {
			return Forecast{}, fmt.Errorf("duplicate extra service: %s", extra.ID)
		}
		seenExtras[extra.ID] = struct{}{}
		currency := strings.ToUpper(strings.TrimSpace(extra.Currency))
		if currency != result.Currency {
			return Forecast{}, fmt.Errorf("service %s currency %s does not match server %s", extra.ID, currency, result.Currency)
		}
		if extra.AmountMinor < 0 || extra.Quantity < 0 {
			return Forecast{}, fmt.Errorf("negative amount or quantity for service %s", extra.ID)
		}
		applies, err := appliesToMonth(extra, month)
		if err != nil {
			return Forecast{}, err
		}
		if !applies || extra.AmountMinor == 0 {
			continue
		}
		quantity := extra.Quantity
		if quantity == 0 {
			quantity = 1
		}
		if extra.AmountMinor > math.MaxInt64/quantity {
			return Forecast{}, fmt.Errorf("service amount overflows: %s", extra.ID)
		}
		amount := extra.AmountMinor * quantity
		customerID := strings.TrimSpace(extra.CustomerID)
		if byCustomer[customerID] == nil {
			byCustomer[customerID] = &CustomerForecast{CustomerID: customerID}
		}
		customer := byCustomer[customerID]
		if err := addMoney(&customer.ServicesMinor, amount); err != nil {
			return Forecast{}, err
		}
		customer.Lines = append(customer.Lines, Line{Key: "service:" + extra.ID, Domain: extra.Domain, Description: extra.Description, AmountMinor: amount})
	}
	for _, customer := range byCustomer {
		if err := addMoney(&customer.TotalMinor, customer.HostingMinor); err != nil {
			return Forecast{}, err
		}
		if err := addMoney(&customer.TotalMinor, customer.ServicesMinor); err != nil {
			return Forecast{}, err
		}
		if err := addMoney(&result.TotalMinor, customer.TotalMinor); err != nil {
			return Forecast{}, err
		}
		sort.Slice(customer.Lines, func(i, j int) bool { return customer.Lines[i].Key < customer.Lines[j].Key })
		result.Customers = append(result.Customers, *customer)
	}
	sort.Slice(result.Customers, func(i, j int) bool { return result.Customers[i].CustomerID < result.Customers[j].CustomerID })
	return result, nil
}

func appliesToMonth(extra Extra, month time.Time) (bool, error) {
	start, err := time.Parse("2006-01", extra.StartMonth)
	if err != nil || start.Format("2006-01") != extra.StartMonth {
		return false, fmt.Errorf("service %s has invalid start month", extra.ID)
	}
	if extra.EndMonth != "" {
		end, endErr := time.Parse("2006-01", extra.EndMonth)
		if endErr != nil || end.Format("2006-01") != extra.EndMonth {
			return false, fmt.Errorf("service %s has invalid end month", extra.ID)
		}
		if end.Before(start) {
			return false, fmt.Errorf("service %s ends before it starts", extra.ID)
		}
		if month.After(end) {
			return false, nil
		}
	}
	delta := (month.Year()-start.Year())*12 + int(month.Month()-start.Month())
	if delta < 0 {
		return false, nil
	}
	switch extra.Frequency {
	case "once":
		return delta == 0, nil
	case "monthly":
		return true, nil
	case "quarterly":
		return delta%3 == 0, nil
	case "yearly":
		return delta%12 == 0, nil
	default:
		return false, fmt.Errorf("service %s has invalid frequency", extra.ID)
	}
}

func addMoney(sum *int64, amount int64) error {
	if amount < 0 || *sum > math.MaxInt64-amount {
		return fmt.Errorf("billing amount overflows")
	}
	*sum += amount
	return nil
}
