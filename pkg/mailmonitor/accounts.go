package mailmonitor

import (
	"context"
	"encoding/json"
	"time"

	"github.com/matveynator/sitebrush/v2/pkg/mailout"
)

func AccountSchemaQuery() string {
	return `CREATE TABLE IF NOT EXISTS mail_account_events(id TEXT PRIMARY KEY,payload TEXT NOT NULL);`
}

// Account outcomes are committed by the site's SQL owner in the same
// transaction as the account change. Export acknowledgments may safely replay.
func SaveAccountEvent(ctx context.Context, database Database, event Event) error {
	if event.CreatedAt == "" {
		event.CreatedAt = time.Now().UTC().Format(timestampLayout)
	}
	if event.ID == "" {
		event.ID = digestID(event.FlowID + "/" + event.Stage + "/0")
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		return err
	}
	_, err = database.ExecContext(ctx, `INSERT INTO mail_account_events(id,payload) VALUES(?,?) ON CONFLICT(id) DO NOTHING`, event.ID, string(encoded))
	return err
}

func PendingAccountEvents(ctx context.Context, database Database) ([]Event, error) {
	rows, err := database.QueryContext(ctx, `SELECT payload FROM mail_account_events ORDER BY id LIMIT 80`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []Event{}
	for rows.Next() {
		var encoded string
		if err = rows.Scan(&encoded); err != nil {
			return nil, err
		}
		var event Event
		if err = json.Unmarshal([]byte(encoded), &event); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

// Historical success requires an explicit persisted account outcome. A sent
// email alone never supplies evidence that an account was recovered.
func (store Store) BackfillRecovery(ctx context.Context) error {
	rows, err := store.DB.QueryContext(ctx, `SELECT id,domain,email,created_at FROM support_events WHERE kind='password_changed' AND status='success' AND message='password recovered' AND created_at>=?`, time.Now().UTC().Add(-Retention).Format(time.RFC3339))
	if err != nil {
		return err
	}
	events := []Event{}
	for rows.Next() {
		var sourceID, domain, email, created string
		if err = rows.Scan(&sourceID, &domain, &email, &created); err != nil {
			break
		}
		events = append(events, Event{ID: digestID("legacy recovery/" + sourceID), FlowID: digestID("legacy recovery/" + sourceID), Kind: "recovery", Stage: "recovery_complete", Domain: domain, Recipient: email, CreatedAt: created, Detail: "restored from a saved password recovery outcome"})
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	for _, event := range events {
		completed, err := time.Parse(time.RFC3339Nano, event.CreatedAt)
		if err != nil {
			continue
		}
		matches, err := store.DB.QueryContext(ctx, `SELECT id FROM mail_monitor_events WHERE stage='recovery_complete' AND domain=? AND recipient=? AND created_at>=? AND created_at<=? LIMIT 1`, event.Domain, event.Recipient, completed.Add(-5*time.Second).UTC().Format(timestampLayout), completed.Add(5*time.Second).UTC().Format(timestampLayout))
		if err != nil {
			return err
		}
		found := matches.Next()
		err = matches.Err()
		matches.Close()
		if err != nil {
			return err
		}
		if !found {
			// A single surviving flow is sufficient evidence to attach the explicit
			// success to its request, including an origin that used the central relay.
			candidates, err := store.DB.QueryContext(ctx, `SELECT flow_id FROM mail_monitor_events WHERE domain=? AND recipient=? AND flow_id<>'' AND created_at>=? AND created_at<=? AND (stage='recovery_requested' OR (stage='queued' AND kind='login_code')) ORDER BY created_at DESC LIMIT 16`, event.Domain, event.Recipient, completed.Add(-time.Hour).UTC().Format(timestampLayout), completed.Add(5*time.Second).UTC().Format(timestampLayout))
			if err != nil {
				return err
			}
			flows := map[string]bool{}
			for candidates.Next() {
				var flow string
				if err = candidates.Scan(&flow); err != nil {
					break
				}
				flows[flow] = true
			}
			if err == nil {
				err = candidates.Err()
			}
			candidates.Close()
			if err != nil {
				return err
			}
			if len(flows) == 1 {
				for flow := range flows {
					event.FlowID = flow
				}
			}
			localOrigin, err := store.State(ctx, "local_origin")
			if err != nil {
				return err
			}
			if localOrigin != "" {
				event.Metadata = &mailout.Metadata{OriginInstallationID: localOrigin}
			}
			if err = store.Append(ctx, event); err != nil {
				return err
			}
		}
	}
	return nil
}
