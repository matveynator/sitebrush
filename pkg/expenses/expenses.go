package expenses

import (
	"fmt"
	"sort"
	"strings"
)

const (
	DecimalMegabyte int64 = 1_000_000
	DecimalGigabyte int64 = 1_000_000_000

	DefaultDiskRatePer100GBMinor  int64 = 1500
	DefaultFreeSiteThresholdBytes       = 100 * DecimalMegabyte
)

type Mode string

const (
	ModeAutomatic Mode = "automatic"
	ModeActual    Mode = "actual"
)

type ServerPolicy struct {
	InstallationID            string
	Mode                      Mode
	DiskRatePer100GBMinor     int64
	ActualMonthlyExpenseMinor int64
	Currency                  string
	FreeSiteThresholdBytes    int64
	DiskTotalBytes            int64
	EffectiveAt               string
	UpdatedAt                 string
}

func DefaultServerPolicy(installationID string, diskTotalBytes int64) ServerPolicy {
	return ServerPolicy{
		InstallationID:         strings.TrimSpace(installationID),
		Mode:                   ModeAutomatic,
		DiskRatePer100GBMinor:  DefaultDiskRatePer100GBMinor,
		Currency:               "EUR",
		FreeSiteThresholdBytes: DefaultFreeSiteThresholdBytes,
		DiskTotalBytes:         diskTotalBytes,
	}
}

func (policy ServerPolicy) Validate() error {
	if strings.TrimSpace(policy.InstallationID) == "" {
		return fmt.Errorf("installation id is required")
	}
	if policy.Mode != ModeAutomatic && policy.Mode != ModeActual {
		return fmt.Errorf("expense mode must be automatic or actual")
	}
	if policy.DiskRatePer100GBMinor <= 0 {
		return fmt.Errorf("disk rate per 100 GB must be greater than zero")
	}
	if policy.Mode == ModeActual && policy.ActualMonthlyExpenseMinor <= 0 {
		return fmt.Errorf("actual monthly expense must be greater than zero")
	}
	if normalizeCurrency(policy.Currency) == "" {
		return fmt.Errorf("expense currency must be a three-letter code")
	}
	if policy.FreeSiteThresholdBytes < 0 {
		return fmt.Errorf("free site threshold cannot be negative")
	}
	return nil
}

func CalculateMonthlyExpense(policy ServerPolicy) int64 {
	if policy.Mode == ModeActual {
		return max(policy.ActualMonthlyExpenseMinor, 0)
	}
	if policy.DiskTotalBytes <= 0 || policy.DiskRatePer100GBMinor <= 0 {
		return 0
	}
	const rateBytes = 100 * DecimalGigabyte
	wholeUnits := policy.DiskTotalBytes / rateBytes
	remainderBytes := policy.DiskTotalBytes % rateBytes
	return wholeUnits*policy.DiskRatePer100GBMinor +
		(remainderBytes*policy.DiskRatePer100GBMinor+rateBytes/2)/rateBytes
}

type SiteUsage struct {
	Key       string
	UsedBytes int64
	Excluded  bool
}

type SiteAllocation struct {
	UsedBytes         int64
	ExpenseShareMinor int64
	Free              bool
	Excluded          bool
}

func AllocateMonthlyExpense(policy ServerPolicy, sites []SiteUsage) map[string]SiteAllocation {
	allocations := make(map[string]SiteAllocation, len(sites))
	type weightedSite struct {
		key       string
		weight    int64
		share     int64
		remainder int64
	}
	weightedSites := make([]weightedSite, 0, len(sites))
	totalWeight := int64(0)
	for _, site := range sites {
		usedBytes := max(site.UsedBytes, 0)
		free := !site.Excluded && usedBytes <= policy.FreeSiteThresholdBytes
		allocations[site.Key] = SiteAllocation{
			UsedBytes: usedBytes,
			Free:      free,
			Excluded:  site.Excluded,
		}
		if site.Excluded || free {
			continue
		}
		totalWeight += usedBytes
		weightedSites = append(weightedSites, weightedSite{key: site.Key, weight: usedBytes})
	}

	monthlyExpenseMinor := CalculateMonthlyExpense(policy)
	if monthlyExpenseMinor <= 0 || totalWeight <= 0 {
		return allocations
	}
	allocatedMinor := int64(0)
	for weightedIndex := range weightedSites {
		numerator := monthlyExpenseMinor * weightedSites[weightedIndex].weight
		weightedSites[weightedIndex].share = numerator / totalWeight
		weightedSites[weightedIndex].remainder = numerator % totalWeight
		allocatedMinor += weightedSites[weightedIndex].share
	}
	sort.SliceStable(weightedSites, func(left int, right int) bool {
		if weightedSites[left].remainder != weightedSites[right].remainder {
			return weightedSites[left].remainder > weightedSites[right].remainder
		}
		return weightedSites[left].key < weightedSites[right].key
	})
	for remainderIndex := int64(0); remainderIndex < monthlyExpenseMinor-allocatedMinor; remainderIndex++ {
		weightedSites[remainderIndex%int64(len(weightedSites))].share++
	}
	for _, weighted := range weightedSites {
		allocation := allocations[weighted.key]
		allocation.ExpenseShareMinor = weighted.share
		allocations[weighted.key] = allocation
	}
	return allocations
}

func PaymentCommissionMinor(expenseShareMinor int64, commissionBPS int) int64 {
	if expenseShareMinor <= 0 || commissionBPS <= 0 {
		return 0
	}
	return (expenseShareMinor*int64(commissionBPS) + 9999) / 10000
}

func NormalizePolicy(policy ServerPolicy) ServerPolicy {
	unspecifiedPolicy := policy.Mode == ""
	if policy.Mode == "" {
		policy.Mode = ModeAutomatic
	}
	if policy.DiskRatePer100GBMinor <= 0 {
		policy.DiskRatePer100GBMinor = DefaultDiskRatePer100GBMinor
	}
	if policy.FreeSiteThresholdBytes < 0 || (unspecifiedPolicy && policy.FreeSiteThresholdBytes == 0) {
		policy.FreeSiteThresholdBytes = DefaultFreeSiteThresholdBytes
	}
	policy.Currency = normalizeCurrency(policy.Currency)
	if policy.Currency == "" {
		policy.Currency = "EUR"
	}
	return policy
}

func normalizeCurrency(currency string) string {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if len(currency) != 3 {
		return ""
	}
	for _, character := range currency {
		if character < 'A' || character > 'Z' {
			return ""
		}
	}
	return currency
}
