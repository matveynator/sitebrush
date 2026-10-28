package hostingandsupport

import (
	"context"
	"database/sql"
	"testing"
)

func TestCreationSizeLimitsDefaultsAndTariffCaps(t *testing.T) {
	for _, setting := range []string{"", "invalid", "0", "-1"} {
		if positiveSizeSetting(setting) != DefaultCreationLimitBytes {
			t.Fatalf("invalid setting %q changed default", setting)
		}
	}
	if positiveSizeSetting("1234") != 1234 {
		t.Fatal("positive size setting was ignored")
	}
	for _, scenario := range []struct{ maximum, tariff, expected int64 }{
		{0, 0, DefaultCreationLimitBytes}, {1024, 512, 512}, {1024, 2048, 1024}, {1024, 0, 1024},
	} {
		if actual := CreationQuota(scenario.maximum, scenario.tariff); actual != scenario.expected {
			t.Fatalf("quota=%d want=%d", actual, scenario.expected)
		}
	}
}

func TestCreationSizeLimitsPersistAndPolicyRollsBackTogether(t *testing.T) {
	store := newLifecycleStore(t)
	database := store.DB.(*sql.DB)
	ctx := context.Background()
	if err := store.SetOwner(ctx, "owner.example", "owner@example.com"); err != nil {
		t.Fatal(err)
	}
	defaults := SiteSizeLimits{Registration: DefaultCreationLimitBytes, TestDrive: DefaultTestDriveLimitBytes}
	if store.SiteSizeLimits(ctx) != defaults {
		t.Fatal("creation defaults are not 1 GB")
	}
	limits := SiteSizeLimits{Registration: 2 << 20, TestDrive: 4 << 20}
	if err := store.SaveRegistrationSettingsWithLimits(ctx, true, true, limits); err != nil {
		t.Fatal(err)
	}
	if store.SiteSizeLimits(ctx) != limits || !store.AutomaticRegistrationAllowed(ctx) || !store.PublicTrialAllowed(ctx) {
		t.Fatal("creation policy was not saved")
	}
	if err := store.SaveRegistrationSettingsWithLimits(ctx, false, false, SiteSizeLimits{}); err == nil || store.SiteSizeLimits(ctx) != limits {
		t.Fatal("invalid limit changed the saved policy")
	}
	if err := store.SaveRegistrationSettingsWithLimits(ctx, false, true, limits); err != nil || store.PublicTrialAllowed(ctx) {
		t.Fatalf("closed registration must close Test Drive too: %v", err)
	}
	for _, rejectedSetting := range []string{"auto_registration_enabled", "public_trial_enabled", "public_trial_max_site_bytes"} {
		if _, err := database.Exec(`CREATE TRIGGER reject_creation_limit BEFORE UPDATE ON server_settings WHEN NEW.name='` + rejectedSetting + `' BEGIN SELECT RAISE(ABORT,'limit rejected'); END`); err != nil {
			t.Fatal(err)
		}
		if err := store.SaveRegistrationSettingsWithLimits(ctx, true, true, SiteSizeLimits{Registration: 3 << 20, TestDrive: 5 << 20}); err == nil {
			t.Fatal("injected persistence error was ignored")
		}
		if store.SiteSizeLimits(ctx) != limits || store.AutomaticRegistrationAllowed(ctx) || store.PublicTrialAllowed(ctx) {
			t.Fatal("failed size update partially enabled registration")
		}
		if _, err := database.Exec(`DROP TRIGGER reject_creation_limit`); err != nil {
			t.Fatal(err)
		}
	}
	database.Close()
	if err := store.SaveRegistrationSettingsWithLimits(ctx, true, true, limits); err == nil {
		t.Fatal("closed database accepted a creation policy")
	}
}
