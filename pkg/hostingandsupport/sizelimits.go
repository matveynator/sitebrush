package hostingandsupport

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"time"
)

const DefaultCreationLimitBytes int64 = 1024 * 1024 * 1024

type SiteSizeLimits struct {
	Registration int64
	TestDrive    int64
}

func (store Store) SiteSizeLimits(ctx context.Context) SiteSizeLimits {
	return SiteSizeLimits{
		Registration: positiveSizeSetting(SettingText(ctx, store.DB, "registration_max_site_bytes")),
		TestDrive:    positiveSizeSetting(SettingText(ctx, store.DB, "public_trial_max_site_bytes")),
	}
}

func positiveSizeSetting(setting string) int64 {
	bytes, err := strconv.ParseInt(setting, 10, 64)
	if err != nil || bytes <= 0 {
		return DefaultCreationLimitBytes
	}
	return bytes
}

func CreationQuota(limitBytes, planBytes int64) int64 {
	if limitBytes <= 0 {
		limitBytes = DefaultCreationLimitBytes
	}
	if planBytes > 0 && planBytes < limitBytes {
		return planBytes
	}
	return limitBytes
}

// Permission and quota changes commit together so a new site sees one policy.
func (store Store) SaveRegistrationSettingsWithLimits(ctx context.Context, automatic, testDrive bool, limits SiteSizeLimits) error {
	if limits.Registration <= 0 || limits.TestDrive <= 0 {
		return fmt.Errorf("site size limits must be positive")
	}
	if !automatic {
		testDrive = false
	}
	transaction, err := store.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer transaction.Rollback()
	if err = saveBooleanSetting(ctx, transaction, "auto_registration_enabled", automatic); err != nil {
		return err
	}
	if err = saveBooleanSetting(ctx, transaction, "public_trial_enabled", testDrive); err != nil {
		return err
	}
	for _, setting := range []struct {
		name  string
		bytes int64
	}{
		{"registration_max_site_bytes", limits.Registration}, {"public_trial_max_site_bytes", limits.TestDrive},
	} {
		if err = saveSiteSizeSetting(ctx, transaction, setting.name, setting.bytes); err != nil {
			return err
		}
	}
	return transaction.Commit()
}

func saveSiteSizeSetting(ctx context.Context, transaction *sql.Tx, name string, bytes int64) error {
	_, err := transaction.ExecContext(ctx, `INSERT INTO server_settings(name,value,updated_at) VALUES(?,?,?) ON CONFLICT(name) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, name, strconv.FormatInt(bytes, 10), time.Now().UTC().Format(time.RFC3339))
	return err
}
