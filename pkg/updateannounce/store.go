package updateannounce

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/matveynator/sitebrush/v2/pkg/mailmonitor"
	"github.com/matveynator/sitebrush/v2/pkg/mailout"
)

const Kind = "release_announcement:"
const Retention = 7 * 24 * time.Hour

type Campaign struct {
	Version   string
	Reason    string
	CreatedAt string
	ExpiresAt string
	Canceled  bool
	Waiting   int
	Sent      int
	Skipped   int
	Failed    int
	LastError string
}
type Installation struct {
	ID     string
	Key    string
	Domain string
}

func SchemaQueries() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS release_campaigns(version TEXT PRIMARY KEY,reason TEXT NOT NULL,created_at TEXT NOT NULL,expires_at TEXT NOT NULL,canceled INTEGER NOT NULL DEFAULT 0)`,
		`CREATE TABLE IF NOT EXISTS release_campaign_deliveries(version TEXT NOT NULL,email TEXT NOT NULL,installations TEXT NOT NULL,message_id TEXT NOT NULL,status TEXT NOT NULL,PRIMARY KEY(version,email))`,
	}
}

func Start(database *sql.DB, version, reason string, now time.Time) error {
	if reason != "security" && reason != "feature" && reason != "both" {
		return errors.New("choose an important security fix or significant feature")
	}
	sequence, versionErr := strconv.ParseUint(version, 10, 64)
	if versionErr != nil || sequence == 0 {
		return errors.New("missing verified stable version")
	}
	var count int
	if err := database.QueryRow(`SELECT COUNT(1) FROM release_campaigns WHERE version=?`, version).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	_, err := database.Exec(`INSERT INTO release_campaigns(version,reason,created_at,expires_at,canceled) VALUES(?,?,?,?,0)`, version, reason, now.UTC().Format(time.RFC3339Nano), now.Add(Retention).UTC().Format(time.RFC3339Nano))
	return err
}

func Cancel(database *sql.DB, version string) error {
	_, err := database.Exec(`UPDATE release_campaigns SET canceled=1 WHERE version=?`, version)
	return err
}

func Campaigns(database *sql.DB) ([]Campaign, error) {
	rows, err := database.Query(`SELECT version,reason,created_at,expires_at,canceled FROM release_campaigns ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	var campaigns []Campaign
	for rows.Next() {
		var campaign Campaign
		var canceled int
		if err := rows.Scan(&campaign.Version, &campaign.Reason, &campaign.CreatedAt, &campaign.ExpiresAt, &canceled); err != nil {
			rows.Close()
			return nil, err
		}
		campaign.Canceled = canceled != 0
		campaigns = append(campaigns, campaign)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	for index := range campaigns {
		counts, err := database.Query(`SELECT status,COUNT(1) FROM release_campaign_deliveries WHERE version=? GROUP BY status`, campaigns[index].Version)
		if err != nil {
			return nil, err
		}
		for counts.Next() {
			var status string
			var count int
			if err := counts.Scan(&status, &count); err != nil {
				counts.Close()
				return nil, err
			}
			switch status {
			case "waiting":
				campaigns[index].Waiting = count
			case "sent":
				campaigns[index].Sent = count
			case "skipped":
				campaigns[index].Skipped = count
			case "failed":
				campaigns[index].Failed = count
			}
		}
		err = counts.Err()
		counts.Close()
		if err != nil {
			return nil, err
		}
		err = database.QueryRow(`SELECT last_error FROM mail_outbox WHERE kind=? AND last_error<>'' ORDER BY created_at DESC LIMIT 1`, Kind+campaigns[index].Version).Scan(&campaigns[index].LastError)
		if err != nil && err != sql.ErrNoRows {
			return nil, err
		}
	}
	return campaigns, nil
}

// Queue is called by the central database owner, never by an installation.
// Reserving the recipient and its outbox record together survives both restarts
// and outbox retention without repeating an announcement for the same release.
func Queue(database *sql.DB, now time.Time) error {
	campaigns, err := Campaigns(database)
	if err != nil {
		return err
	}
	for _, campaign := range campaigns {
		expires, err := time.Parse(time.RFC3339Nano, campaign.ExpiresAt)
		if err != nil {
			return err
		}
		if campaign.Canceled || !now.Before(expires) {
			status := "failed"
			if campaign.Canceled {
				status = "skipped"
			}
			if _, err := database.Exec(`UPDATE release_campaign_deliveries SET status=? WHERE version=? AND status='waiting'`, status, campaign.Version); err != nil {
				return err
			}
			continue
		}
		rows, err := database.Query(`SELECT DISTINCT roles.email,hostings.installation_id FROM registry_installation_roles roles JOIN client_hostings hostings ON hostings.installation_id=roles.installation_id JOIN service_mail_installations installations ON installations.installation_id=hostings.installation_id WHERE roles.role='superadmin' AND roles.scope='installation' AND hostings.installation_kind='server' AND installations.blocked=0`)
		if err != nil {
			return err
		}
		owners := make(map[string][]string)
		for rows.Next() {
			var email, id string
			if err := rows.Scan(&email, &id); err != nil {
				rows.Close()
				return err
			}
			email = strings.ToLower(strings.TrimSpace(email))
			owners[email] = append(owners[email], id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		queued := 0
		for email, installations := range owners {
			if queued >= 100 {
				break
			}
			var verifiedInstallations []string
			recipientDigest := sha256.Sum256([]byte("sitebrush service mail recipient\n" + strings.ToLower(strings.TrimSpace(email))))
			for _, id := range installations {
				var verified int
				if err := database.QueryRow(`SELECT COUNT(1) FROM service_mail_recipients WHERE installation_id=? AND recipient_hash=? AND status='verified'`, id, hex.EncodeToString(recipientDigest[:])).Scan(&verified); err != nil {
					return err
				}
				if verified > 0 {
					verifiedInstallations = append(verifiedInstallations, id)
				}
			}
			if len(verifiedInstallations) == 0 {
				continue
			}
			installations = verifiedInstallations
			encoded, err := json.Marshal(installations)
			if err != nil {
				return err
			}
			transaction, err := database.Begin()
			if err != nil {
				return err
			}
			var count int
			err = transaction.QueryRow(`SELECT COUNT(1) FROM release_campaign_deliveries WHERE version=? AND email=?`, campaign.Version, email).Scan(&count)
			if err != nil {
				transaction.Rollback()
				return err
			}
			if count > 0 {
				transaction.Rollback()
				continue
			}
			digest := sha256.Sum256([]byte(campaign.Version + "|" + email))
			id := "release-announcement-" + hex.EncodeToString(digest[:])
			_, err = transaction.Exec(`INSERT INTO release_campaign_deliveries(version,email,installations,message_id,status) VALUES(?,?,?,?,'waiting')`, campaign.Version, email, string(encoded), id)
			if err == nil {
				task := mailout.NewTask(Kind+campaign.Version, mailout.RouteLocal, mailout.Message{MonitorDomain: "sitebrush.com", From: "SiteBrush <sitebrush@sitebrush.com>", To: email}, expires, nil)
				task.ID = id
				_, err = mailout.Insert(context.Background(), transaction, task)
				if err == nil {
					// Campaigns bypass the application enqueue path. Commit their
					// journal metadata with the outbox so attempts can inherit it.
					err = (mailmonitor.Store{DB: transaction}).Append(context.Background(), mailmonitor.Event{
						MessageID: task.ID, Kind: task.Kind, Stage: "queued",
						Domain: task.Message.MonitorDomain, Recipient: task.Message.To, Route: task.Route,
					})
				}
			}
			if err != nil {
				transaction.Rollback()
				return err
			}
			if err := transaction.Commit(); err != nil {
				return err
			}
			queued++
		}
	}
	return nil
}

func Delivery(database *sql.DB, version, email string, now time.Time) (Campaign, []Installation, error) {
	var campaign Campaign
	campaign.Version = version
	var canceled int
	if err := database.QueryRow(`SELECT reason,created_at,expires_at,canceled FROM release_campaigns WHERE version=?`, version).Scan(&campaign.Reason, &campaign.CreatedAt, &campaign.ExpiresAt, &canceled); err != nil {
		return campaign, nil, err
	}
	expires, err := time.Parse(time.RFC3339Nano, campaign.ExpiresAt)
	if err != nil {
		return campaign, nil, err
	}
	if canceled != 0 || !now.Before(expires) {
		return campaign, nil, mailout.PermanentError{Err: errors.New("announcement canceled or expired")}
	}
	var encoded string
	if err := database.QueryRow(`SELECT installations FROM release_campaign_deliveries WHERE version=? AND email=?`, version, email).Scan(&encoded); err != nil {
		return campaign, nil, err
	}
	var ids []string
	if err := json.Unmarshal([]byte(encoded), &ids); err != nil {
		return campaign, nil, err
	}
	var installations []Installation
	for _, id := range ids {
		var installation Installation
		installation.ID = id
		err := database.QueryRow(`SELECT installations.public_key,hostings.server_domain FROM service_mail_installations installations JOIN client_hostings hostings ON hostings.installation_id=installations.installation_id WHERE installations.installation_id=? AND installations.blocked=0`, id).Scan(&installation.Key, &installation.Domain)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return campaign, nil, err
		}
		var count int
		if err := database.QueryRow(`SELECT COUNT(1) FROM registry_installation_roles WHERE installation_id=? AND email=? AND role='superadmin' AND scope='installation'`, id, email).Scan(&count); err != nil {
			return campaign, nil, err
		}
		if count > 0 {
			recipientDigest := sha256.Sum256([]byte("sitebrush service mail recipient\n" + strings.ToLower(strings.TrimSpace(email))))
			var verified int
			if err := database.QueryRow(`SELECT COUNT(1) FROM service_mail_recipients WHERE installation_id=? AND recipient_hash=? AND status='verified'`, id, hex.EncodeToString(recipientDigest[:])).Scan(&verified); err != nil {
				return campaign, nil, err
			}
			if verified > 0 {
				installations = append(installations, installation)
			}
		}
	}
	return campaign, installations, nil
}

func Mark(database *sql.DB, version, email, status string) error {
	if status != "sent" && status != "skipped" && status != "failed" {
		return fmt.Errorf("invalid delivery status %q", status)
	}
	_, err := database.Exec(`UPDATE release_campaign_deliveries SET status=? WHERE version=? AND email=?`, status, version, email)
	return err
}
