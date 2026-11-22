package hostingandsupport

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

func (store Store) LocalInstallationID(ctx context.Context) (string, error) {
	var installationID string
	err := store.DB.QueryRowContext(ctx, `SELECT value FROM server_settings WHERE name='service_mail_installation_id'`).Scan(&installationID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return strings.TrimSpace(installationID), err
}

// InvoiceSnapshot is collected by the existing database owner, before the
// immutable panel snapshot is published. Financial totals must never depend on
// a presentation limit. Two cursors avoid per-invoice queries on the owner's
// single SQL connection; neither cursor remains open when the next one starts.
func (store Store) InvoiceSnapshot(ctx context.Context) ([]Invoice, error) {
	rows, err := store.DB.QueryContext(ctx, invoiceSelectColumns+` FROM billing_invoices ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var invoices []Invoice
	positions := make(map[int]int)
	for rows.Next() {
		invoice, scanErr := scanInvoice(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		positions[invoice.ID] = len(invoices)
		invoices = append(invoices, invoice)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	lines, err := store.DB.QueryContext(ctx, `SELECT id,invoice_id,COALESCE(domain,''),COALESCE(description,''),COALESCE(used_bytes,0),COALESCE(billable_megabytes,0),COALESCE(list_amount_minor,0),COALESCE(discount_amount_minor,0),COALESCE(total_amount_minor,0),COALESCE(bonus,0),COALESCE(cost_share_minor,0),COALESCE(minimum_amount_minor,0) FROM billing_invoice_lines ORDER BY invoice_id,id`)
	if err != nil {
		return nil, err
	}
	defer lines.Close()
	for lines.Next() {
		var line InvoiceLine
		var bonus int
		if err := lines.Scan(&line.ID, &line.InvoiceID, &line.Domain, &line.Description, &line.UsedBytes, &line.BillableMegabytes, &line.ListAmountMinor, &line.DiscountAmountMinor, &line.TotalAmountMinor, &bonus, &line.CostShareMinor, &line.MinimumAmountMinor); err != nil {
			return nil, err
		}
		line.Bonus = bonus != 0
		if position, found := positions[line.InvoiceID]; found {
			invoices[position].Lines = append(invoices[position].Lines, line)
		}
	}
	if err := lines.Err(); err != nil {
		return nil, err
	}
	if err := lines.Close(); err != nil {
		return nil, err
	}
	return invoices, nil
}

// InvoiceBelongsToServer never infers historical ownership from a reusable
// domain. Local aliases are explicit identities of this installation only.
func InvoiceBelongsToServer(invoice Invoice, server ServerView) bool {
	if server.ID != "" && invoice.InstallationID == server.ID {
		return true
	}
	return server.Local && invoice.InstallationID == "local" ||
		server.Local && normalizeDomainName(server.Name) != "" && invoice.InstallationID == "local:"+normalizeDomainName(server.Name)
}

func InvoicesForServer(invoices []Invoice, server ServerView) []Invoice {
	var scoped []Invoice
	for _, invoice := range invoices {
		if InvoiceBelongsToServer(invoice, server) {
			scoped = append(scoped, invoice)
		}
	}
	return scoped
}
