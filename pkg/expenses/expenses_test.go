package expenses

import "testing"

func TestCalculateMonthlyExpenseFromDiskOrActualInvoice(t *testing.T) {
	policy := DefaultServerPolicy("local", 500*DecimalGigabyte)
	if expense := CalculateMonthlyExpense(policy); expense != 7500 {
		t.Fatalf("500 GB expense = %d, want 7500", expense)
	}
	if capacity := BillingCapacityBytes(policy); capacity != 150*DecimalGigabyte {
		t.Fatalf("billing capacity = %d, want 150 GB", capacity)
	}
	if pricePerGB := BillingCostPerGBMinor(policy); pricePerGB != 50 {
		t.Fatalf("billing price per GB = %d, want 50", pricePerGB)
	}
	policy.Mode = ModeActual
	policy.ActualMonthlyExpenseMinor = 8123
	if expense := CalculateMonthlyExpense(policy); expense != 8123 {
		t.Fatalf("actual expense = %d, want 8123", expense)
	}
}

func TestAllocateMonthlyExpenseUsesThirtyPercentOfDisk(t *testing.T) {
	policy := DefaultServerPolicy("local", 500*DecimalGigabyte)
	allocation := AllocateMonthlyExpense(policy, []SiteUsage{
		{Key: "free", UsedBytes: 100 * DecimalMegabyte},
		{Key: "small", UsedBytes: 30 * DecimalGigabyte},
		{Key: "large", UsedBytes: 60 * DecimalGigabyte},
		{Key: "owner", UsedBytes: 90 * DecimalGigabyte, Excluded: true},
	})
	if allocation.BillingCapacityBytes != 150*DecimalGigabyte {
		t.Fatalf("billing capacity = %d, want 150 GB", allocation.BillingCapacityBytes)
	}
	if allocation.AllocatedMinor != 4500 || allocation.Sites["small"].ExpenseShareMinor != 1500 || allocation.Sites["large"].ExpenseShareMinor != 3000 {
		t.Fatalf("paid allocation = %+v", allocation)
	}
	if !allocation.Sites["free"].Free || allocation.Sites["free"].ExpenseShareMinor != 0 {
		t.Fatalf("free allocation = %+v", allocation.Sites["free"])
	}
	if allocation.Sites["owner"].ExpenseShareMinor != 0 || !allocation.Sites["owner"].Excluded {
		t.Fatalf("owner allocation = %+v", allocation.Sites["owner"])
	}
}

func TestAllocateMonthlyExpenseCapsTotalAfterThirtyPercent(t *testing.T) {
	policy := DefaultServerPolicy("local", 500*DecimalGigabyte)
	allocation := AllocateMonthlyExpense(policy, []SiteUsage{
		{Key: "first", UsedBytes: 100 * DecimalGigabyte},
		{Key: "second", UsedBytes: 100 * DecimalGigabyte},
	})
	if !allocation.CapacityExceeded {
		t.Fatal("capacity must be exceeded")
	}
	if allocation.AllocatedMinor != 7500 || allocation.Sites["first"].ExpenseShareMinor != 3750 || allocation.Sites["second"].ExpenseShareMinor != 3750 {
		t.Fatalf("capped allocation = %+v", allocation)
	}
}

func TestAllocateMonthlyExpenseRequiresKnownDiskCapacity(t *testing.T) {
	policy := DefaultServerPolicy("local", 0)
	policy.Mode = ModeActual
	policy.ActualMonthlyExpenseMinor = 7500
	allocation := AllocateMonthlyExpense(policy, []SiteUsage{{Key: "site", UsedBytes: 20 * DecimalGigabyte}})
	if allocation.AllocatedMinor != 0 || allocation.Sites["site"].ExpenseShareMinor != 0 {
		t.Fatalf("unknown disk allocation = %+v", allocation)
	}
}

func TestPaymentCommissionIsAddedOnTop(t *testing.T) {
	if commission := PaymentCommissionMinor(1000, 500); commission != 50 {
		t.Fatalf("commission = %d, want 50", commission)
	}
}

func TestServerPolicyValidationAndNormalization(t *testing.T) {
	valid := DefaultServerPolicy(" install-1 ", 1_000_000_000)
	if err := valid.Validate(); err != nil {
		t.Fatal(err)
	}
	testCases := []struct {
		name   string
		policy ServerPolicy
	}{
		{"missing installation", ServerPolicy{Mode: ModeAutomatic, DiskRatePer100GBMinor: 1, Currency: "USD"}},
		{"unsupported mode", ServerPolicy{InstallationID: "x", Mode: "other", DiskRatePer100GBMinor: 1, Currency: "USD"}},
		{"invalid rate", ServerPolicy{InstallationID: "x", Mode: ModeAutomatic, Currency: "USD"}},
		{"missing actual invoice", ServerPolicy{InstallationID: "x", Mode: ModeActual, DiskRatePer100GBMinor: 1, Currency: "USD"}},
		{"bad currency", ServerPolicy{InstallationID: "x", Mode: ModeAutomatic, DiskRatePer100GBMinor: 1, Currency: "US1"}},
		{"negative free threshold", ServerPolicy{InstallationID: "x", Mode: ModeAutomatic, DiskRatePer100GBMinor: 1, Currency: "USD", FreeSiteThresholdBytes: -1}},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if err := testCase.policy.Validate(); err == nil {
				t.Fatal("Validate unexpectedly succeeded")
			}
		})
	}
	if normalized := NormalizePolicy(ServerPolicy{InstallationID: " x ", Currency: " usd "}); normalized.Mode != ModeAutomatic || normalized.DiskRatePer100GBMinor != DefaultDiskRatePer100GBMinor || normalized.FreeSiteThresholdBytes != DefaultFreeSiteThresholdBytes || normalized.Currency != "USD" {
		t.Fatalf("normalized defaults=%+v", normalized)
	}
	if normalized := NormalizePolicy(ServerPolicy{Mode: ModeActual, DiskRatePer100GBMinor: -1, FreeSiteThresholdBytes: -1, Currency: "U$D"}); normalized.FreeSiteThresholdBytes != DefaultFreeSiteThresholdBytes || normalized.Currency != "EUR" {
		t.Fatalf("normalized invalid values=%+v", normalized)
	}
}
