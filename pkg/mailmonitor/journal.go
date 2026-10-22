package mailmonitor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/netip"
	"strings"
	"time"

	"github.com/matveynator/sitebrush/v2/pkg/mailout"
)

// The journal is a materialized view owned by the control database writer.
// Immutable event IDs remain the synchronization and deduplication boundary.
type Operation struct {
	ID                      string
	OriginInstallationID    string
	FlowID                  string
	MessageID               string
	Kind                    string
	Domain                  string
	Recipient               string
	InitiatorIP             string
	InstallationIP          string
	CreatedAt               string
	UpdatedAt               string
	Stage                   string
	Steps                   []Event
	HandledLocally          bool
	QueuedAt                string
	AcceptedAt              string
	FailedAt                string
	RelayAcceptedAt         string
	RegistrationRequestedAt string
	RegistrationCompleteAt  string
	RecoveryRequestedAt     string
	RecoveryCompleteAt      string
	CancelledAt             string
	CancelRequestedAt       string
	CancelTooLateAt         string
}

func JournalSchemaQueries() []string {
	return []string{
		mailout.SchemaQueries()[0],
		mailout.MetadataSchemaQuery(),
		`CREATE TABLE IF NOT EXISTS mail_monitor_metadata(installation_id TEXT NOT NULL,event_id TEXT NOT NULL,payload TEXT NOT NULL,PRIMARY KEY(installation_id,event_id));`,
		`CREATE TABLE IF NOT EXISTS mail_journal(operation_id TEXT PRIMARY KEY,origin_id TEXT NOT NULL,handled_local INTEGER NOT NULL,created_at TEXT NOT NULL,updated_at TEXT NOT NULL,stage TEXT NOT NULL,kind TEXT NOT NULL,recipient TEXT NOT NULL,payload TEXT NOT NULL);`,
		`CREATE INDEX IF NOT EXISTS idx_mail_journal_origin ON mail_journal(origin_id,updated_at);`,
		`CREATE INDEX IF NOT EXISTS idx_mail_journal_local ON mail_journal(handled_local,updated_at);`,
		`CREATE TABLE IF NOT EXISTS mail_journal_counters(operation_id TEXT NOT NULL,identity TEXT NOT NULL,stage TEXT NOT NULL,occurred_at TEXT NOT NULL,PRIMARY KEY(operation_id,identity,stage));`,
		`CREATE INDEX IF NOT EXISTS idx_mail_journal_counters_time ON mail_journal_counters(occurred_at,operation_id);`,
		`CREATE TABLE IF NOT EXISTS mail_ip_blocks(scope TEXT NOT NULL,origin_id TEXT NOT NULL,ip TEXT NOT NULL,reason TEXT NOT NULL,created_at TEXT NOT NULL,PRIMARY KEY(scope,origin_id,ip));`,
		`CREATE TABLE IF NOT EXISTS mail_control_state(name TEXT PRIMARY KEY,payload TEXT NOT NULL);`,
		`CREATE TABLE IF NOT EXISTS mail_cancel_commands(origin_id TEXT NOT NULL,message_id TEXT NOT NULL,status TEXT NOT NULL,created_at TEXT NOT NULL,PRIMARY KEY(origin_id,message_id));`,
		`CREATE TABLE IF NOT EXISTS mail_abuse(origin_id TEXT NOT NULL,ip TEXT NOT NULL,kind TEXT NOT NULL,bucket TEXT NOT NULL,attempts INTEGER NOT NULL,first_at TEXT NOT NULL,last_at TEXT NOT NULL,samples TEXT NOT NULL,PRIMARY KEY(origin_id,ip,kind,bucket));`,
		`CREATE TABLE IF NOT EXISTS mail_account_events(id TEXT PRIMARY KEY,payload TEXT NOT NULL);`,
		`CREATE TABLE IF NOT EXISTS mail_reject_rates(dimension TEXT NOT NULL,identity TEXT NOT NULL,bucket TEXT NOT NULL,attempts INTEGER NOT NULL,PRIMARY KEY(dimension,identity,bucket));`,
		`CREATE TABLE IF NOT EXISTS mail_journal_stages(operation_id TEXT NOT NULL,stage TEXT NOT NULL,kind TEXT NOT NULL,last_at TEXT NOT NULL,PRIMARY KEY(operation_id,stage,kind));`,
		`CREATE INDEX IF NOT EXISTS idx_mail_journal_stages_filter ON mail_journal_stages(stage,kind,last_at,operation_id);`,
	}
}

func ValidateMetadata(metadata mailout.Metadata) error {
	if len(metadata.Domain) > 253 || len(metadata.FlowID) > 128 || len(metadata.OriginInstallationID) > 128 || len(metadata.RequestedAt) > 40 || len(metadata.RelayTransport) > 500 {
		return errors.New("mail metadata is invalid")
	}
	for _, address := range []string{metadata.InitiatorIP, metadata.InstallationIP} {
		if address != "" {
			if _, err := netip.ParseAddr(address); err != nil {
				return errors.New("mail IP is invalid")
			}
		}
	}
	for _, field := range []string{metadata.Domain, metadata.FlowID, metadata.OriginInstallationID} {
		if field != boundedField(field, len(field)) {
			return errors.New("mail metadata contains controls")
		}
	}
	return nil
}

func (store Store) withMetadata(ctx context.Context, events []Event) ([]Event, error) {
	for index := range events {
		rows, err := store.DB.QueryContext(ctx, `SELECT payload FROM mail_monitor_metadata WHERE installation_id=? AND event_id=?`, events[index].InstallationID, events[index].ID)
		if err != nil {
			return nil, err
		}
		var encoded string
		if rows.Next() {
			err = rows.Scan(&encoded)
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return nil, err
		}
		if encoded != "" {
			var extra Event
			if err = json.Unmarshal([]byte(encoded), &extra); err != nil {
				return nil, err
			}
			events[index].Metadata, events[index].Transport = extra.Metadata, extra.Transport
		}
	}
	return events, nil
}

func digestID(identity string) string {
	digest := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(digest[:])
}

func (store Store) project(ctx context.Context, event Event) error {
	// Reuse authenticated diagnostics already persisted by earlier releases.
	diagnostic := event.Diagnostic()
	if event.Metadata == nil && diagnostic.ClientIP != "" {
		if address, err := netip.ParseAddr(diagnostic.ClientIP); err == nil {
			event.Metadata = &mailout.Metadata{InitiatorIP: address.Unmap().String()}
		}
	}
	if event.Transport == "" && diagnostic.Transport != "" {
		event.Transport = diagnostic.Identifiers
		if event.Transport == "" {
			event.Transport = "transport=" + diagnostic.Transport
		}
	}

	origin := event.InstallationID
	if event.Metadata != nil && event.Metadata.OriginInstallationID != "" {
		origin = event.Metadata.OriginInstallationID
	} else if origin == "local" && event.MessageID != "" {
		// The legacy relay already stored the authenticated owner on its outbox.
		// Reuse that evidence when projecting history from before metadata existed.
		rows, err := store.DB.QueryContext(ctx, `SELECT installation_id FROM mail_outbox WHERE message_id=?`, event.MessageID)
		if err != nil {
			return err
		}
		var owner string
		if rows.Next() {
			err = rows.Scan(&owner)
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return err
		}
		if owner != "" {
			origin = owner
		}
	}
	if origin == "local" && (event.Metadata == nil || event.Metadata.OriginInstallationID == "") {
		localOrigin, err := store.State(ctx, "local_origin")
		if err != nil {
			return err
		}
		if localOrigin != "" {
			origin = localOrigin
		}
	}
	if event.FlowID == "" && event.MessageID != "" {
		rows, err := store.DB.QueryContext(ctx, `SELECT flow_id FROM mail_monitor_events WHERE installation_id=? AND message_id=? AND flow_id<>'' ORDER BY created_at LIMIT 1`, origin, event.MessageID)
		if err != nil {
			return err
		}
		if rows.Next() {
			err = rows.Scan(&event.FlowID)
		}
		if err == nil {
			err = rows.Err()
		}
		rows.Close()
		if err != nil {
			return err
		}
		localOrigin, err := store.State(ctx, "local_origin")
		if err != nil {
			return err
		}
		if event.FlowID == "" && origin == localOrigin {
			rows, err = store.DB.QueryContext(ctx, `SELECT flow_id FROM mail_monitor_events WHERE installation_id='local' AND message_id=? AND flow_id<>'' ORDER BY created_at LIMIT 1`, event.MessageID)
			if err != nil {
				return err
			}
			if rows.Next() {
				err = rows.Scan(&event.FlowID)
			}
			if err == nil {
				err = rows.Err()
			}
			rows.Close()
			if err != nil {
				return err
			}
		}
	}
	identity := event.FlowID
	if identity == "" {
		identity = event.MessageID
	}
	if identity == "" {
		identity = event.ID
	}
	operationID := digestID(origin + "\n" + identity)
	operation, found, err := store.Operation(ctx, operationID)
	if err != nil {
		return err
	}
	if !found {
		operation = Operation{ID: operationID, OriginInstallationID: origin, FlowID: event.FlowID, MessageID: event.MessageID, Kind: event.Kind, Domain: event.Domain, Recipient: event.Recipient, CreatedAt: event.CreatedAt}
	}
	if event.FlowID != "" && event.MessageID != "" && event.FlowID != event.MessageID {
		if err := store.mergeLegacy(ctx, &operation, digestID(origin+"\n"+event.MessageID)); err != nil {
			return err
		}
	}
	if event.CreatedAt < operation.CreatedAt {
		operation.CreatedAt = event.CreatedAt
	}
	if event.CreatedAt >= operation.UpdatedAt {
		operation.UpdatedAt = event.CreatedAt
	}
	if event.InstallationID == "local" {
		operation.HandledLocally = true
	}
	if operation.FlowID == "" {
		operation.FlowID = event.FlowID
	}
	if event.MessageID != "" {
		operation.MessageID = event.MessageID
	}
	if event.Metadata != nil {
		if event.Metadata.InitiatorIP != "" {
			operation.InitiatorIP = event.Metadata.InitiatorIP
		}
		if event.Metadata.InstallationIP != "" {
			operation.InstallationIP = event.Metadata.InstallationIP
		}
	}
	switch event.Stage {
	case "queued":
		operation.QueuedAt = firstTime(operation.QueuedAt, event.CreatedAt)
	case "relay_accepted":
		operation.RelayAcceptedAt = firstTime(operation.RelayAcceptedAt, event.CreatedAt)
	case "accepted":
		if event.AcceptedBy == "central_relay" {
			operation.RelayAcceptedAt = firstTime(operation.RelayAcceptedAt, event.CreatedAt)
		} else {
			operation.AcceptedAt = firstTime(operation.AcceptedAt, event.CreatedAt)
		}
	case "failed":
		operation.FailedAt = firstTime(operation.FailedAt, event.CreatedAt)
	case "cancelled":
		operation.CancelledAt = firstTime(operation.CancelledAt, event.CreatedAt)
	case "cancel_requested":
		operation.CancelRequestedAt = event.CreatedAt
	case "cancel_too_late":
		operation.CancelTooLateAt = event.CreatedAt
	case "registration_requested":
		operation.RegistrationRequestedAt = firstTime(operation.RegistrationRequestedAt, event.CreatedAt)
		operation.Kind = "registration"
	case "registration_complete":
		operation.RegistrationCompleteAt = firstTime(operation.RegistrationCompleteAt, event.CreatedAt)
		operation.Kind = "registration"
	case "recovery_requested":
		operation.RecoveryRequestedAt = firstTime(operation.RecoveryRequestedAt, event.CreatedAt)
		operation.Kind = "recovery"
	case "recovery_complete":
		operation.RecoveryCompleteAt = firstTime(operation.RecoveryCompleteAt, event.CreatedAt)
		operation.Kind = "recovery"
	}
	// One visible step per phase and reporting installation; retries update that
	// phase rather than multiplying the visible or serialized history.
	replaced := false
	for index := range operation.Steps {
		previous := operation.Steps[index]
		if previous.Stage == event.Stage && previous.InstallationID == event.InstallationID && previous.MessageID == event.MessageID {
			if event.CreatedAt >= previous.CreatedAt {
				operation.Steps[index] = event
			}
			replaced = true
			break
		}
	}
	if !replaced && len(operation.Steps) < 32 {
		operation.Steps = append(operation.Steps, event)
	}
	phase := event
	smtpPhaseFound := false
	for _, step := range operation.Steps {
		if step.Route == "local" && step.MessageID != "" {
			if !smtpPhaseFound || step.CreatedAt > phase.CreatedAt {
				phase = step
				smtpPhaseFound = true
			}
		}
	}
	operation.Stage = phase.Stage
	if phase.Stage == "accepted" && phase.AcceptedBy == "central_relay" {
		operation.Stage = "relay_accepted"
	}
	switch {
	case operation.RecoveryCompleteAt != "":
		operation.Stage = "recovery_complete"
	case operation.RegistrationCompleteAt != "":
		operation.Stage = "registration_complete"
	case operation.AcceptedAt != "":
		operation.Stage = "accepted"
	case operation.CancelledAt != "":
		operation.Stage = "cancelled"
	case operation.CancelRequestedAt != "" && operation.CancelRequestedAt > operation.CancelTooLateAt:
		operation.Stage = "cancel_requested"
	case operation.CancelTooLateAt != "" && operation.CancelTooLateAt >= phase.CreatedAt:
		operation.Stage = "cancel_too_late"
	case operation.FailedAt != "":
		operation.Stage = "failed"
	}
	encoded, err := json.Marshal(operation)
	if err != nil {
		return err
	}
	_, err = store.DB.ExecContext(ctx, `INSERT INTO mail_journal(operation_id,origin_id,handled_local,created_at,updated_at,stage,kind,recipient,payload) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(operation_id) DO UPDATE SET handled_local=excluded.handled_local,created_at=excluded.created_at,updated_at=excluded.updated_at,stage=excluded.stage,kind=excluded.kind,recipient=excluded.recipient,payload=excluded.payload`, operation.ID, origin, operation.HandledLocally, operation.CreatedAt, operation.UpdatedAt, operation.Stage, operation.Kind, operation.Recipient, string(encoded))
	if err != nil {
		return err
	}
	stage := event.Stage
	if stage == "accepted" && event.AcceptedBy == "central_relay" {
		stage = "relay_accepted"
	}
	if err := store.recordStage(ctx, operation.ID, stage, event.Kind, event.CreatedAt); err != nil {
		return err
	}
	counterIdentity := event.MessageID
	switch stage {
	case "registration_requested", "registration_complete", "recovery_requested", "recovery_complete":
		counterIdentity = operation.ID
	case "queued", "accepted", "failed", "relay_accepted", "cancelled":
	default:
		return nil
	}
	if counterIdentity == "" {
		counterIdentity = event.ID
	}
	_, err = store.DB.ExecContext(ctx, `INSERT INTO mail_journal_counters(operation_id,identity,stage,occurred_at) VALUES(?,?,?,?) ON CONFLICT(operation_id,identity,stage) DO NOTHING`, operation.ID, counterIdentity, stage, event.CreatedAt)
	return err
}

// One row per observed phase and mail kind keeps history filtering independent
// of the final status and the bounded list of steps displayed in the browser.
func (store Store) recordStage(ctx context.Context, operationID, stage, kind, at string) error {
	_, err := store.DB.ExecContext(ctx, `INSERT INTO mail_journal_stages(operation_id,stage,kind,last_at) VALUES(?,?,?,?) ON CONFLICT(operation_id,stage,kind) DO UPDATE SET last_at=CASE WHEN mail_journal_stages.last_at<excluded.last_at THEN excluded.last_at ELSE mail_journal_stages.last_at END`, operationID, stage, journalKind(kind), at)
	return err
}

func journalKind(kind string) string {
	switch kind = strings.ToLower(strings.TrimSpace(kind)); kind {
	case "login_code", "recover":
		return "recovery"
	case "email_confirm", "register":
		return "registration"
	default:
		return kind
	}
}

// A relay may learn the old origin's flow only after an offline installation
// reconnects. Merge its message-only projection without duplicating counters.
func (store Store) mergeLegacy(ctx context.Context, operation *Operation, legacyID string) error {
	if legacyID == operation.ID {
		return nil
	}
	legacy, found, err := store.Operation(ctx, legacyID)
	if err != nil || !found {
		return err
	}
	operation.HandledLocally = operation.HandledLocally || legacy.HandledLocally
	operation.CreatedAt = firstTime(operation.CreatedAt, legacy.CreatedAt)
	if legacy.UpdatedAt > operation.UpdatedAt {
		operation.UpdatedAt = legacy.UpdatedAt
	}
	if operation.Recipient == "" {
		operation.Recipient = legacy.Recipient
	}
	if operation.Domain == "" {
		operation.Domain = legacy.Domain
	}
	if operation.InitiatorIP == "" {
		operation.InitiatorIP = legacy.InitiatorIP
	}
	for _, fields := range []struct {
		destination *string
		source      string
	}{
		{&operation.QueuedAt, legacy.QueuedAt}, {&operation.AcceptedAt, legacy.AcceptedAt}, {&operation.FailedAt, legacy.FailedAt}, {&operation.RelayAcceptedAt, legacy.RelayAcceptedAt}, {&operation.CancelledAt, legacy.CancelledAt},
	} {
		if fields.source != "" {
			*fields.destination = firstTime(*fields.destination, fields.source)
		}
	}
	for _, step := range legacy.Steps {
		alreadyPresent := false
		for _, existing := range operation.Steps {
			if existing.ID == step.ID && existing.InstallationID == step.InstallationID {
				alreadyPresent = true
				break
			}
		}
		if !alreadyPresent && len(operation.Steps) < 32 {
			operation.Steps = append(operation.Steps, step)
		}
	}
	rows, err := store.DB.QueryContext(ctx, `SELECT identity,stage,occurred_at FROM mail_journal_counters WHERE operation_id=?`, legacyID)
	if err != nil {
		return err
	}
	type counter struct{ identity, stage, at string }
	counters := []counter{}
	for rows.Next() {
		var current counter
		if err = rows.Scan(&current.identity, &current.stage, &current.at); err != nil {
			break
		}
		counters = append(counters, current)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	for _, current := range counters {
		if _, err = store.DB.ExecContext(ctx, `INSERT INTO mail_journal_counters(operation_id,identity,stage,occurred_at) VALUES(?,?,?,?) ON CONFLICT(operation_id,identity,stage) DO NOTHING`, operation.ID, current.identity, current.stage, current.at); err != nil {
			return err
		}
	}
	if _, err = store.DB.ExecContext(ctx, `DELETE FROM mail_journal_counters WHERE operation_id=?`, legacyID); err != nil {
		return err
	}
	rows, err = store.DB.QueryContext(ctx, `SELECT stage,kind,last_at FROM mail_journal_stages WHERE operation_id=?`, legacyID)
	if err != nil {
		return err
	}
	type phase struct{ stage, kind, at string }
	phases := []phase{}
	for rows.Next() {
		var current phase
		if err = rows.Scan(&current.stage, &current.kind, &current.at); err != nil {
			break
		}
		phases = append(phases, current)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	for _, current := range phases {
		if err := store.recordStage(ctx, operation.ID, current.stage, current.kind, current.at); err != nil {
			return err
		}
	}
	if _, err = store.DB.ExecContext(ctx, `DELETE FROM mail_journal_stages WHERE operation_id=?`, legacyID); err != nil {
		return err
	}
	_, err = store.DB.ExecContext(ctx, `DELETE FROM mail_journal WHERE operation_id=?`, legacyID)
	return err
}

func firstTime(previous, current string) string {
	if previous == "" || current < previous {
		return current
	}
	return previous
}

func (store Store) Operation(ctx context.Context, id string) (Operation, bool, error) {
	rows, err := store.DB.QueryContext(ctx, `SELECT payload FROM mail_journal WHERE operation_id=?`, id)
	if err != nil {
		return Operation{}, false, err
	}
	defer rows.Close()
	if !rows.Next() {
		return Operation{}, false, rows.Err()
	}
	var encoded string
	if err = rows.Scan(&encoded); err != nil {
		return Operation{}, false, err
	}
	var operation Operation
	err = json.Unmarshal([]byte(encoded), &operation)
	return operation, err == nil, err
}

func journalFilter(filter Filter) (string, []any) {
	where := ` WHERE mail_journal.updated_at>=?`
	arguments := []any{filter.Since.UTC().Format(timestampLayout)}
	if filter.InstallationID == "local" {
		where += ` AND mail_journal.handled_local=1`
	} else {
		where += ` AND mail_journal.origin_id=?`
		arguments = append(arguments, filter.InstallationID)
	}
	if filter.Stage != "" || filter.Kind != "" {
		where += ` AND EXISTS(SELECT 1 FROM mail_journal_stages observed WHERE observed.operation_id=mail_journal.operation_id AND observed.last_at>=?`
		arguments = append(arguments, filter.Since.UTC().Format(timestampLayout))
		if filter.Stage != "" {
			where += ` AND observed.stage=?`
			arguments = append(arguments, strings.ToLower(strings.TrimSpace(filter.Stage)))
		}
		if filter.Kind != "" {
			where += ` AND observed.kind=?`
			arguments = append(arguments, journalKind(filter.Kind))
		}
		where += `)`
	}
	if filter.Recipient != "" {
		where += ` AND LOWER(mail_journal.recipient) LIKE ? ESCAPE '!'`
		escaped := strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(strings.ToLower(filter.Recipient))
		arguments = append(arguments, "%"+escaped+"%")
	}
	return where, arguments
}

func (store Store) Journal(ctx context.Context, filter Filter) ([]Operation, int, error) {
	where, arguments := journalFilter(filter)
	rows, err := store.DB.QueryContext(ctx, `SELECT COUNT(*) FROM mail_journal`+where, arguments...)
	if err != nil {
		return nil, 0, err
	}
	count := 0
	if rows.Next() {
		err = rows.Scan(&count)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return nil, 0, err
	}
	limit := filter.Limit
	if limit <= 0 || limit > 50 {
		limit = 50
	}
	arguments = append(arguments, limit, max(0, filter.Offset))
	rows, err = store.DB.QueryContext(ctx, `SELECT payload FROM mail_journal`+where+` ORDER BY updated_at DESC,operation_id DESC LIMIT ? OFFSET ?`, arguments...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	operations := []Operation{}
	for rows.Next() {
		var encoded string
		if err = rows.Scan(&encoded); err != nil {
			return nil, 0, err
		}
		var operation Operation
		if err = json.Unmarshal([]byte(encoded), &operation); err != nil {
			return nil, 0, err
		}
		operations = append(operations, operation)
	}
	return operations, count, rows.Err()
}

func (store Store) JournalCounts(ctx context.Context, installationID string, since time.Time) (Counts, error) {
	where, arguments := journalFilter(Filter{InstallationID: installationID, Since: since})
	arguments = append(arguments, since.UTC().Format(timestampLayout))
	rows, err := store.DB.QueryContext(ctx, `SELECT c.stage,COUNT(*) FROM mail_journal INNER JOIN mail_journal_counters c ON mail_journal.operation_id=c.operation_id`+where+` AND c.occurred_at>=? AND (c.stage<>'failed' OR NOT EXISTS(SELECT 1 FROM mail_journal_counters delivered WHERE delivered.operation_id=c.operation_id AND delivered.identity=c.identity AND delivered.stage='accepted')) GROUP BY c.stage`, arguments...)
	if err != nil {
		return Counts{}, err
	}
	defer rows.Close()
	counts := Counts{}
	for rows.Next() {
		var stage string
		var count int
		if err = rows.Scan(&stage, &count); err != nil {
			return Counts{}, err
		}
		switch stage {
		case "queued":
			counts.Queued = count
		case "accepted":
			counts.Accepted = count
		case "failed":
			counts.Failed = count
		case "cancelled":
			counts.Cancelled = count
		case "relay_accepted":
			counts.RelayAccepted = count
		case "registration_requested":
			counts.RegistrationRequested = count
		case "registration_complete":
			counts.RegistrationComplete = count
		case "recovery_requested":
			counts.RecoveryRequested = count
		case "recovery_complete":
			counts.RecoveryComplete = count
		}
	}
	return counts, rows.Err()
}

// Rebuild is only run by a migration, before the database owner admits traffic.
func (store Store) Rebuild(ctx context.Context) error {
	offset := 0
	for {
		rows, err := store.DB.QueryContext(ctx, `SELECT id,installation_id,flow_id,message_id,kind,stage,domain,recipient,route,accepted_by,attempt,smtp_code,detail,created_at FROM mail_monitor_events ORDER BY created_at,id LIMIT 200 OFFSET ?`, offset)
		if err != nil {
			return err
		}
		events, err := scanEvents(rows)
		rows.Close()
		if err != nil {
			return err
		}
		events, err = store.withMetadata(ctx, events)
		if err != nil {
			return err
		}
		for _, event := range events {
			if err = store.project(ctx, event); err != nil {
				return err
			}
		}
		if len(events) < 200 {
			return nil
		}
		offset += len(events)
	}
}

func (store Store) State(ctx context.Context, name string) (string, error) {
	rows, err := store.DB.QueryContext(ctx, `SELECT payload FROM mail_control_state WHERE name=?`, name)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var payload string
	if rows.Next() {
		err = rows.Scan(&payload)
	}
	if err == nil {
		err = rows.Err()
	}
	return payload, err
}

func (store Store) SetState(ctx context.Context, name, payload string) error {
	_, err := store.DB.ExecContext(ctx, `INSERT INTO mail_control_state(name,payload) VALUES(?,?) ON CONFLICT(name) DO UPDATE SET payload=excluded.payload`, name, payload)
	return err
}
