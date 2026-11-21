package hostingandsupport

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/matveynator/sitebrush/v2/pkg/expenses"
	_ "modernc.org/sqlite"
)

func TestMonetizationModePersistsAcrossTariffVersions(t *testing.T) {
	database, err:=sql.Open("sqlite",filepath.Join(t.TempDir(),"billing.db"))
	if err!=nil{t.Fatal(err)}
	defer database.Close()
	ctx:=context.Background()
	if err:=Migrate(ctx,database);err!=nil{t.Fatal(err)}
	store:=Store{DB:database}
	base:=expenses.ServerPolicy{
		InstallationID:"local",Mode:expenses.ModeActual,
		DiskRatePer100GBMinor:1500,ActualMonthlyExpenseMinor:8900,
		Currency:"EUR",BillingCapacityPercent:30,
	}
	base.MonetizationMode="disabled"
	if _,err:=store.SaveServerExpensePolicy(ctx,base);err!=nil{t.Fatal(err)}
	disabled,found:=store.ServerExpensePolicy(ctx,"local",100*expenses.DecimalGigabyte)
	if !found||disabled.MonetizationEnabled()||disabled.ActualMonthlyExpenseMinor!=8900 {
		t.Fatalf("disabled policy not persisted: %+v found=%t",disabled,found)
	}
	base.MonetizationMode="enabled"
	if _,err:=store.SaveServerExpensePolicy(ctx,base);err!=nil{t.Fatal(err)}
	enabled,found:=store.ServerExpensePolicy(ctx,"local",100*expenses.DecimalGigabyte)
	if !found||!enabled.MonetizationEnabled()||enabled.ActualMonthlyExpenseMinor!=8900 {
		t.Fatalf("enabled policy not persisted: %+v found=%t",enabled,found)
	}
	if disabled.MonetizationEnabled()||disabled.EffectiveAt==enabled.EffectiveAt {
		t.Fatalf("tariff history was mutated: disabled=%+v enabled=%+v",disabled,enabled)
	}
}

func TestMonetizationMigrationKeepsLegacyPaidPolicyEnabled(t *testing.T) {
	database,err:=sql.Open("sqlite",filepath.Join(t.TempDir(),"legacy.db"))
	if err!=nil{t.Fatal(err)}
	defer database.Close()
	ctx:=context.Background()
	if err:=Migrate(ctx,database);err!=nil{t.Fatal(err)}
	// Published schema versions did not contain monetization_mode. The new
	// NOT NULL default preserves the owners' existing paid configurations.
	_,err=database.ExecContext(ctx,`INSERT INTO server_expense_policies
		(installation_id,expense_mode,disk_rate_per_100_gb_minor,actual_monthly_expense_minor,currency,free_site_threshold_bytes,billing_capacity_percent,effective_at,created_at)
		VALUES('old','actual',1500,12000,'EUR',100000000,30,'2026-11-01','2026-11-01')`)
	if err!=nil{t.Fatal(err)}
	policy,found:=(Store{DB:database}).ServerExpensePolicy(ctx,"old",100*expenses.DecimalGigabyte)
	if !found||!policy.MonetizationEnabled()||policy.MonetizationMode!="enabled" {
		t.Fatalf("legacy paid server disabled during migration: %+v",policy)
	}
}
