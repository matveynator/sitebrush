package mailmonitor

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/matveynator/sitebrush/v2/pkg/mailout"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

type IPBlock struct {
	Scope                string
	OriginInstallationID string
	IP                   string
	Reason               string
	CreatedAt            string
}

// The cursor orders immutable events, so a lost response can safely replay the
// same prefix. Only the requesting installation's relay deliveries are exposed.
func (store Store) RelayEvents(ctx context.Context, origin, cursor string) ([]Event, string, error) {
	parts := strings.SplitN(cursor, "|", 2)
	created, id := "", ""
	if len(parts) == 2 {
		created, id = parts[0], parts[1]
	}
	rows, err := store.DB.QueryContext(ctx, `SELECT e.id,e.installation_id,e.flow_id,e.message_id,e.kind,e.stage,e.domain,e.recipient,e.route,e.accepted_by,e.attempt,e.smtp_code,e.detail,e.created_at FROM mail_monitor_events e INNER JOIN mail_journal_counters c ON c.identity=e.message_id AND c.stage='queued' INNER JOIN mail_journal j ON j.operation_id=c.operation_id WHERE e.installation_id='local' AND j.origin_id=? AND j.handled_local=1 AND (e.created_at>? OR (e.created_at=? AND e.id>?)) ORDER BY e.created_at,e.id LIMIT 20`, origin, created, created, id)
	if err != nil {
		return nil, cursor, err
	}
	events, err := scanEvents(rows)
	rows.Close()
	if err != nil {
		return nil, cursor, err
	}
	events, err = store.withMetadata(ctx, events)
	for index := range events {
		if events[index].Metadata == nil {
			events[index].Metadata = &mailout.Metadata{OriginInstallationID: origin}
		}
	}
	if len(events) > 0 {
		last := events[len(events)-1]
		cursor = last.CreatedAt + "|" + last.ID
	}
	return events, cursor, err
}

type CancelCommand struct {
	OriginInstallationID string
	MessageID            string
	Status               string
	CreatedAt            string
}

type ControlRequest struct {
	Cursor    string
	Completed []CancelCommand
	Cancel    []string
	Reports   []Abuse
}

type ControlResponse struct {
	Revision       int64
	Blocks         []IPBlock
	Commands       []CancelCommand
	Events         []Event
	Cursor         string
	Signature      string
	NetChanVersion string `json:"netchan_version"`
	CancelResults  []CancelCommand
}

func (store Store) RejectRate(ctx context.Context, dimension, identity string, now time.Time) error {
	if identity == "" {
		return nil
	}
	_, err := store.DB.ExecContext(ctx, `INSERT INTO mail_reject_rates(dimension,identity,bucket,attempts) VALUES(?,?,?,1) ON CONFLICT(dimension,identity,bucket) DO UPDATE SET attempts=mail_reject_rates.attempts+1`, dimension, identity, now.UTC().Format("2006-01-02T15:04:05Z"))
	return err
}

func (store Store) RejectedCount(ctx context.Context, dimension, identity string, since time.Time) (int, error) {
	rows, err := store.DB.QueryContext(ctx, `SELECT COALESCE(SUM(attempts),0) FROM mail_reject_rates WHERE dimension=? AND identity=? AND bucket>=?`, dimension, identity, since.UTC().Format("2006-01-02T15:04:05Z"))
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	count := 0
	if rows.Next() {
		err = rows.Scan(&count)
	}
	if err == nil {
		err = rows.Err()
	}
	return count, err
}

func PublicIP(raw string) bool {
	address, err := netip.ParseAddr(raw)
	address = address.Unmap()
	return err == nil && address.Zone() == "" && address.IsGlobalUnicast() && !address.IsPrivate() && !address.IsLoopback() && !address.IsLinkLocalUnicast()
}

func IsLoopbackIP(raw string) bool {
	address, err := netip.ParseAddr(strings.TrimSpace(raw))
	return err == nil && address.Unmap().IsLoopback()
}

func (store Store) Blocks(ctx context.Context) ([]IPBlock, error) {
	rows, err := store.DB.QueryContext(ctx, `SELECT scope,origin_id,ip,reason,created_at FROM mail_ip_blocks ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	blocks := []IPBlock{}
	for rows.Next() {
		var block IPBlock
		if err = rows.Scan(&block.Scope, &block.OriginInstallationID, &block.IP, &block.Reason, &block.CreatedAt); err != nil {
			return nil, err
		}
		blocks = append(blocks, block)
	}
	return blocks, rows.Err()
}

func (store Store) SetBlock(ctx context.Context, block IPBlock, blocked bool) error {
	if len(block.OriginInstallationID) > 128 || block.OriginInstallationID != boundedField(block.OriginInstallationID, 128) {
		return errors.New("invalid mail block installation")
	}
	address, err := netip.ParseAddr(block.IP)
	if err != nil {
		return errors.New("invalid initiator IP")
	}
	if blocked && address.Unmap().IsLoopback() {
		return errors.New("local access IP cannot be blocked")
	}
	block.IP = address.Unmap().String()
	if block.Scope != "global" && block.Scope != "installation" && block.Scope != "central_installation" {
		return errors.New("invalid mail block scope")
	}
	if block.Scope == "global" {
		if !PublicIP(block.IP) {
			return errors.New("private IP requires an installation scope")
		}
		block.OriginInstallationID = ""
	} else if block.OriginInstallationID == "" {
		return errors.New("mail block installation is required")
	}
	if blocked {
		_, err = store.DB.ExecContext(ctx, `INSERT INTO mail_ip_blocks(scope,origin_id,ip,reason,created_at) VALUES(?,?,?,?,?) ON CONFLICT(scope,origin_id,ip) DO UPDATE SET reason=excluded.reason`, block.Scope, block.OriginInstallationID, block.IP, boundedField(block.Reason, 500), time.Now().UTC().Format(timestampLayout))
	} else {
		_, err = store.DB.ExecContext(ctx, `DELETE FROM mail_ip_blocks WHERE scope=? AND origin_id=? AND ip=?`, block.Scope, block.OriginInstallationID, block.IP)
	}
	return err
}

func (store Store) Blocked(ctx context.Context, origin, ip string) (bool, error) {
	if ip == "" {
		return false, nil
	}
	address, err := netip.ParseAddr(ip)
	if err != nil {
		return false, err
	}
	if address.Unmap().IsLoopback() {
		return false, nil
	}
	ip = address.Unmap().String()
	rows, err := store.DB.QueryContext(ctx, `SELECT ip FROM mail_ip_blocks WHERE ip=? AND (scope='global' OR (scope IN ('installation','central_installation') AND origin_id=?))`, ip, origin)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	found := rows.Next()
	return found, rows.Err()
}

func (store Store) BumpRevision(ctx context.Context) (int64, error) {
	raw, err := store.State(ctx, "policy_revision")
	if err != nil {
		return 0, err
	}
	revision, _ := strconv.ParseInt(raw, 10, 64)
	revision++
	return revision, store.SetState(ctx, "policy_revision", strconv.FormatInt(revision, 10))
}

func (store Store) ApplyPolicy(ctx context.Context, response ControlResponse) error {
	previous, err := store.State(ctx, "received_policy_revision")
	if err != nil {
		return err
	}
	revision, _ := strconv.ParseInt(previous, 10, 64)
	if response.Revision < revision {
		return errors.New("mail policy rollback rejected")
	}
	if response.Revision == revision && previous != "" {
		return nil
	}
	// Installation rules are local decisions. A central snapshot only replaces
	// the globally scoped rules whose authority is the pinned central identity.
	for _, block := range response.Blocks {
		if block.Scope == "installation" && IsLoopbackIP(block.IP) {
			continue
		}
		if (block.Scope != "global" || !PublicIP(block.IP)) && (block.Scope != "installation" || block.OriginInstallationID == "") {
			return errors.New("invalid global mail policy")
		}
	}
	if _, err = store.DB.ExecContext(ctx, `DELETE FROM mail_ip_blocks WHERE scope IN ('global','central_installation')`); err != nil {
		return err
	}
	for _, block := range response.Blocks {
		if block.Scope == "installation" && IsLoopbackIP(block.IP) {
			continue
		}
		if block.Scope == "installation" {
			block.Scope = "central_installation"
		}
		if err = store.SetBlock(ctx, block, true); err != nil {
			return err
		}
	}
	return store.SetState(ctx, "received_policy_revision", strconv.FormatInt(response.Revision, 10))
}

// A remote report updates a monotonic bucket total, so transport retries never
// multiply incidents. It does not create a global block.
func (store Store) ImportAbuse(ctx context.Context, origin string, reports []Abuse) error {
	if len(reports) > 50 {
		return errors.New("too many mail abuse reports")
	}
	for _, report := range reports {
		if report.OriginInstallationID != origin || report.Attempts < 1 || len(report.Samples) > 20 || len(report.Kind) > 60 {
			return errors.New("invalid mail abuse report")
		}
		if _, err := netip.ParseAddr(report.IP); err != nil {
			return err
		}
		bucket, err := time.Parse(time.RFC3339Nano, report.Bucket)
		if err != nil {
			return err
		}
		if bucket.After(time.Now().Add(5*time.Minute)) || bucket.Before(time.Now().Add(-Retention)) {
			return errors.New("invalid mail abuse period")
		}
		report.Bucket = bucket.UTC().Truncate(10 * time.Minute).Format(timestampLayout)
		first, err := time.Parse(time.RFC3339Nano, report.FirstAt)
		if err != nil {
			return err
		}
		last, err := time.Parse(time.RFC3339Nano, report.LastAt)
		if err != nil {
			return err
		}
		if first.Before(bucket) || last.Before(first) || last.After(bucket.Add(10*time.Minute)) {
			return errors.New("invalid mail abuse timestamps")
		}
		report.FirstAt = first.UTC().Format(timestampLayout)
		report.LastAt = last.UTC().Format(timestampLayout)
		for index, sample := range report.Samples {
			report.Samples[index] = SafeError(errors.New(sample))
		}
		encoded, err := json.Marshal(report.Samples)
		if err != nil {
			return err
		}
		_, err = store.DB.ExecContext(ctx, `INSERT INTO mail_abuse(origin_id,ip,kind,bucket,attempts,first_at,last_at,samples) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(origin_id,ip,kind,bucket) DO UPDATE SET attempts=CASE WHEN mail_abuse.attempts<excluded.attempts THEN excluded.attempts ELSE mail_abuse.attempts END,last_at=CASE WHEN mail_abuse.last_at<excluded.last_at THEN excluded.last_at ELSE mail_abuse.last_at END,samples=excluded.samples`, origin, report.IP, report.Kind, report.Bucket, report.Attempts, report.FirstAt, report.LastAt, string(encoded))
		if err != nil {
			return err
		}
	}
	return nil
}

func (store Store) RequestCancel(ctx context.Context, origin, messageID string) error {
	if origin == "" || messageID == "" || len(origin) > 128 || len(messageID) > 128 {
		return errors.New("invalid cancellation identity")
	}
	_, err := store.DB.ExecContext(ctx, `INSERT INTO mail_cancel_commands(origin_id,message_id,status,created_at) VALUES(?,?,'requested',?) ON CONFLICT(origin_id,message_id) DO NOTHING`, origin, messageID, time.Now().UTC().Format(timestampLayout))
	return err
}

func (store Store) CancelStatus(ctx context.Context, origin, messageID string) (string, error) {
	rows, err := store.DB.QueryContext(ctx, `SELECT status FROM mail_cancel_commands WHERE origin_id=? AND message_id=?`, origin, messageID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var status string
	if rows.Next() {
		err = rows.Scan(&status)
	}
	if err == nil {
		err = rows.Err()
	}
	return status, err
}

func (store Store) Commands(ctx context.Context, origin string) ([]CancelCommand, error) {
	rows, err := store.DB.QueryContext(ctx, `SELECT origin_id,message_id,status,created_at FROM mail_cancel_commands WHERE origin_id=? AND status='requested' ORDER BY created_at LIMIT 80`, origin)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	commands := []CancelCommand{}
	for rows.Next() {
		var command CancelCommand
		if err = rows.Scan(&command.OriginInstallationID, &command.MessageID, &command.Status, &command.CreatedAt); err != nil {
			return nil, err
		}
		commands = append(commands, command)
	}
	return commands, rows.Err()
}

func (store Store) CompleteCommand(ctx context.Context, origin, messageID, status string) error {
	if status != "cancelled" && status != "too_late" && status != "not_found" {
		return errors.New("invalid cancellation result")
	}
	_, err := store.DB.ExecContext(ctx, `UPDATE mail_cancel_commands SET status=? WHERE origin_id=? AND message_id=? AND status='requested'`, status, origin, messageID)
	return err
}

type Abuse struct {
	OriginInstallationID string
	IP                   string
	Kind                 string
	Bucket               string
	Attempts             int
	FirstAt              string
	LastAt               string
	Samples              []string
}

// Recipient and error text are samples, never grouping keys controlled by a
// spammer. This bounds persistence for a flood of distinct rejected addresses.
func (store Store) Reject(ctx context.Context, origin, ip, kind, detail string, now time.Time) error {
	origin = boundedField(origin, 128)
	kind = boundedField(kind, 60)
	if address, err := netip.ParseAddr(ip); err == nil {
		ip = address.Unmap().String()
	} else {
		ip = ""
	}
	bucket := now.UTC().Truncate(10 * time.Minute).Format(timestampLayout)
	rows, err := store.DB.QueryContext(ctx, `SELECT attempts,first_at,samples FROM mail_abuse WHERE origin_id=? AND ip=? AND kind=? AND bucket=?`, origin, ip, kind, bucket)
	if err != nil {
		return err
	}
	attempts := 0
	first := now.UTC().Format(timestampLayout)
	encoded := "[]"
	found := rows.Next()
	if found {
		err = rows.Scan(&attempts, &first, &encoded)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	if !found {
		rows, err = store.DB.QueryContext(ctx, `SELECT COUNT(*) FROM mail_abuse WHERE bucket=?`, bucket)
		if err != nil {
			return err
		}
		count := 0
		if rows.Next() {
			err = rows.Scan(&count)
		}
		rows.Close()
		if err != nil {
			return err
		}
		if count >= 4096 && (origin != "overflow" || ip != "" || kind != "overflow") {
			return store.Reject(ctx, "overflow", "", "overflow", detail, now)
		}
	}
	samples := []string{}
	if err = json.Unmarshal([]byte(encoded), &samples); err != nil {
		return err
	}
	if len(samples) < 20 {
		samples = append(samples, SafeError(errors.New(detail)))
	}
	payload, err := json.Marshal(samples)
	if err != nil {
		return err
	}
	_, err = store.DB.ExecContext(ctx, `INSERT INTO mail_abuse(origin_id,ip,kind,bucket,attempts,first_at,last_at,samples) VALUES(?,?,?,?,?,?,?,?) ON CONFLICT(origin_id,ip,kind,bucket) DO UPDATE SET attempts=excluded.attempts,last_at=excluded.last_at,samples=excluded.samples`, origin, ip, kind, bucket, attempts+1, first, now.UTC().Format(timestampLayout), string(payload))
	return err
}

func (store Store) Abuse(ctx context.Context, since time.Time) ([]Abuse, error) {
	rows, err := store.DB.QueryContext(ctx, `SELECT origin_id,ip,kind,bucket,attempts,first_at,last_at,samples FROM mail_abuse WHERE last_at>=? ORDER BY last_at DESC LIMIT 50`, since.UTC().Format(timestampLayout))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	summaries := []Abuse{}
	for rows.Next() {
		var summary Abuse
		var encoded string
		if err = rows.Scan(&summary.OriginInstallationID, &summary.IP, &summary.Kind, &summary.Bucket, &summary.Attempts, &summary.FirstAt, &summary.LastAt, &encoded); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(encoded), &summary.Samples); err != nil {
			return nil, err
		}
		summaries = append(summaries, summary)
	}
	return summaries, rows.Err()
}
