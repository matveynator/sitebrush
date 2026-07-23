package expenses

import "testing"

func TestCalculateMonthlyExpenseFromDiskOrActualInvoice(t *testing.T) {
	policy := DefaultServerPolicy("local", 500*DecimalGigabyte)
	if expense := CalculateMonthlyExpense(policy); expense != 7500 {
		t.Fatalf("500 GB expense = %d, want 7500", expense)
	}
	policy.Mode = ModeActual
	policy.ActualMonthlyExpenseMinor = 8123
	if expense := CalculateMonthlyExpense(policy); expense != 8123 {
		t.Fatalf("actual expense = %d, want 8123", expense)
	}
}

func TestAllocateMonthlyExpenseExcludesFreeAndDistributesExactTotal(t *testing.T) {
	policy := DefaultServerPolicy("local", 100*DecimalGigabyte)
	allocations := AllocateMonthlyExpense(policy, []SiteUsage{
		{Key: "free", UsedBytes: 100 * DecimalMegabyte},
		{Key: "small", UsedBytes: 200 * DecimalMegabyte},
		{Key: "large", UsedBytes: 400 * DecimalMegabyte},
		{Key: "owner", UsedBytes: 900 * DecimalMegabyte, Excluded: true},
	})
	if !allocations["free"].Free || allocations["free"].ExpenseShareMinor != 0 {
		t.Fatalf("free allocation = %+v", allocations["free"])
	}
	if allocations["small"].ExpenseShareMinor != 500 || allocations["large"].ExpenseShareMinor != 1000 {
		t.Fatalf("paid allocations = %+v", allocations)
	}
	if allocations["owner"].ExpenseShareMinor != 0 || !allocations["owner"].Excluded {
		t.Fatalf("owner allocation = %+v", allocations["owner"])
	}
}

func TestPaymentCommissionIsAddedOnTop(t *testing.T) {
	if commission := PaymentCommissionMinor(1000, 500); commission != 50 {
		t.Fatalf("commission = %d, want 50", commission)
	}
}
