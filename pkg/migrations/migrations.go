// Package migrations executes an append-only chain inside the database owner's
// process. Release numbers never substitute for the work recorded in this log.
package migrations

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
)

type Step struct {
	ID       string
	Revision string
	Ensure   bool
	Apply    func(*sql.Tx) error
	// Recoverable is for legacy adapters and filesystem work that cannot join a
	// SQL transaction. It must be idempotent; completion is recorded afterwards.
	Recoverable func() error
}

func SQL(id, statement string, arguments ...any) Step {
	digest := sha256.Sum256([]byte(statement))
	return Step{ID: id, Revision: hex.EncodeToString(digest[:]), Apply: func(transaction *sql.Tx) error {
		_, err := transaction.Exec(statement, arguments...)
		return err
	}}
}

func Run(database *sql.DB, component string, steps []Step) error {
	if err := validate(steps); err != nil {
		return err
	}
	for _, step := range steps {
		if (step.Apply == nil) == (step.Recoverable == nil) {
			return errors.New("migration step must have one execution strategy")
		}
	}
	if _, err := database.Exec(`CREATE TABLE IF NOT EXISTS application_migration_steps(component TEXT NOT NULL,step_id TEXT NOT NULL,ordinal INTEGER NOT NULL,revision TEXT NOT NULL,applied_at TEXT NOT NULL,PRIMARY KEY(component,step_id),UNIQUE(component,ordinal))`); err != nil {
		return err
	}
	completed, err := Check(database, component, steps)
	if err != nil {
		return err
	}
	for index := 0; index < len(steps); index++ {
		step := steps[index]
		if index < completed && !step.Ensure {
			continue
		}
		if step.Recoverable != nil {
			if err := step.Recoverable(); err != nil {
				return fmt.Errorf("%s/%s: %w", component, step.ID, err)
			}
		}
		transaction, err := database.Begin()
		if err != nil {
			return err
		}
		if step.Apply != nil {
			err = step.Apply(transaction)
		}
		if err == nil && index >= completed {
			_, err = transaction.Exec(`INSERT INTO application_migration_steps(component,step_id,ordinal,revision,applied_at) VALUES(?,?,?,?,?)`, component, step.ID, index+1, step.Revision, time.Now().UTC().Format(time.RFC3339Nano))
		}
		if err != nil {
			transaction.Rollback()
			return fmt.Errorf("%s/%s: %w", component, step.ID, err)
		}
		if err := transaction.Commit(); err != nil {
			return fmt.Errorf("%s/%s commit: %w", component, step.ID, err)
		}
	}
	return nil
}

// Check never creates metadata or runs a step. Database-specific adapters decide
// whether an older database predates the journal before invoking it.
func Check(database *sql.DB, component string, steps []Step) (int, error) {
	if err := validate(steps); err != nil {
		return 0, err
	}
	rows, err := database.Query(`SELECT step_id,ordinal,revision FROM application_migration_steps WHERE component=? ORDER BY ordinal`, component)
	if err != nil {
		return 0, err
	}
	completed := 0
	for rows.Next() {
		var id, revision string
		var ordinal int
		if err := rows.Scan(&id, &ordinal, &revision); err != nil {
			rows.Close()
			return 0, err
		}
		if ordinal != completed+1 || ordinal > len(steps) || steps[completed].ID != id || steps[completed].Revision != revision {
			rows.Close()
			expectedID, expectedRevision := "<end of known history>", ""
			if completed < len(steps) {
				expectedID, expectedRevision = steps[completed].ID, steps[completed].Revision
			}
			return 0, fmt.Errorf("migration history does not match this binary: component=%q ordinal=%d expected_ordinal=%d stored_step=%q stored_revision=%q expected_step=%q expected_revision=%q; refusing a missing, changed or newer step", component, ordinal, completed+1, id, revision, expectedID, expectedRevision)
		}
		completed++
	}
	readErr := rows.Err()
	rows.Close()
	if readErr != nil {
		return 0, readErr
	}
	return completed, nil
}

func validate(steps []Step) error {
	seen := make(map[string]bool)
	for _, step := range steps {
		if step.ID == "" || step.Revision == "" || seen[step.ID] {
			return errors.New("invalid or duplicate migration step")
		}
		seen[step.ID] = true
	}
	return nil
}
