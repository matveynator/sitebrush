package hostingandsupport

import (
	"context"
	"database/sql"
	"strings"
)

// PopulateSiteDeletionViews loads pending site and account deletion state in two
// database reads, not one pair of SQL queries per website in the server UI.
func PopulateSiteDeletionViews(ctx context.Context, database *sql.DB, sites []Site) error {
	positions := make(map[string][]int, len(sites))
	for index := range sites {
		domain := strings.ToLower(strings.TrimSpace(sites[index].Domain))
		positions[domain] = append(positions[domain], index)
	}
	if err := populatePendingSites(ctx, database, sites, positions); err != nil {
		return err
	}
	return populatePendingAccounts(ctx, database, sites, positions)
}

func populatePendingSites(ctx context.Context, database *sql.DB, sites []Site, positions map[string][]int) error {
	rows, err := database.QueryContext(ctx, `SELECT domain,reason,delete_after FROM site_deletion_queue WHERE status='pending'`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var domain, reason, deleteAfter string
		if err := rows.Scan(&domain, &reason, &deleteAfter); err != nil {
			return err
		}
		for _, index := range positions[strings.ToLower(strings.TrimSpace(domain))] {
			sites[index].DeletionReason = reason
			sites[index].DeletionDeadline = deleteAfter
		}
	}
	return rows.Err()
}

func populatePendingAccounts(ctx context.Context, database *sql.DB, sites []Site, positions map[string][]int) error {
	rows, err := database.QueryContext(ctx, `SELECT domain,email,delete_after FROM site_account_deletion_queue ORDER BY domain,email`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var domain, email, deleteAfter string
		if err := rows.Scan(&domain, &email, &deleteAfter); err != nil {
			return err
		}
		for _, index := range positions[strings.ToLower(strings.TrimSpace(domain))] {
			sites[index].AdminAccounts = append(sites[index].AdminAccounts, SiteAccountView{Email: email, DeleteAfter: deleteAfter})
		}
	}
	return rows.Err()
}
