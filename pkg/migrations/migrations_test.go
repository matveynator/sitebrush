package migrations

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestTwentySequentialDataMigrationsResumeAfterFailure(t *testing.T) {
	database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "old.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	database.SetMaxOpenConns(1)
	if _, err := database.Exec(`CREATE TABLE document(id INTEGER PRIMARY KEY,body TEXT,stage INTEGER); INSERT INTO document(id,body,stage) VALUES(1,'preserve this document',0)`); err != nil {
		t.Fatal(err)
	}
	steps := make([]Step, 0, 20)
	for number := 1; number <= 20; number++ {
		steps = append(steps, Step{ID: fmt.Sprintf("stage-%02d", number), Revision: "1", Apply: func(transaction *sql.Tx) error {
			result, err := transaction.Exec(`UPDATE document SET stage=? WHERE stage=?`, number, number-1)
			if err != nil {
				return err
			}
			count, err := result.RowsAffected()
			if err != nil {
				return err
			}
			if count != 1 {
				return errors.New("prerequisite migration did not run")
			}
			return nil
		}})
	}
	broken := append([]Step(nil), steps...)
	apply := broken[9].Apply
	broken[9].Apply = func(transaction *sql.Tx) error {
		if err := apply(transaction); err != nil {
			return err
		}
		return errors.New("simulated power loss before commit")
	}
	if err := Run(database, "documents", broken); err == nil {
		t.Fatal("failed migration succeeded")
	}
	var stage, count int
	var body string
	if err := database.QueryRow(`SELECT body,stage FROM document`).Scan(&body, &stage); err != nil {
		t.Fatal(err)
	}
	if stage != 9 || body != "preserve this document" {
		t.Fatal("failed step changed committed user data", stage, body)
	}
	if err := database.QueryRow(`SELECT COUNT(1) FROM application_migration_steps`).Scan(&count); err != nil || count != 9 {
		t.Fatal("completion marker was committed without its step", count, err)
	}
	if err := Run(database, "documents", steps); err != nil {
		t.Fatal(err)
	}
	if err := Run(database, "documents", steps); err != nil {
		t.Fatal("repeated startup reran completed steps", err)
	}
	if err := database.QueryRow(`SELECT body,stage FROM document`).Scan(&body, &stage); err != nil || stage != 20 || body != "preserve this document" {
		t.Fatal("cumulative upgrade lost content", stage, body, err)
	}
	if err := Run(database, "documents", steps[:19]); err == nil {
		t.Fatal("an older binary accepted a newer schema")
	}
	changed := append([]Step(nil), steps...)
	changed[5].Revision = "changed"
	if _, err := Check(database, "documents", changed); err == nil {
		t.Fatal("changed historical migration accepted")
	}
	if _, err := database.Exec(`DELETE FROM application_migration_steps WHERE ordinal=7`); err != nil {
		t.Fatal(err)
	}
	if _, err := Check(database, "documents", steps); err == nil {
		t.Fatal("migration gap accepted")
	}
}

func TestRecoverableWorkRetriesAndEnsureRepairsWithoutDuplicateMarkers(t *testing.T) {
	database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "recover.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	calls := 0
	step := Step{ID: "external-files", Revision: "1", Recoverable: func() error {
		calls++
		if calls == 1 {
			return errors.New("incomplete filesystem copy")
		}
		return nil
	}}
	if err := Run(database, "files", []Step{step}); err == nil {
		t.Fatal("incomplete external work was marked complete")
	}
	if count, err := Check(database, "files", []Step{step}); err != nil || count != 0 {
		t.Fatal("failed work retained marker", count, err)
	}
	if err := Run(database, "files", []Step{step}); err != nil {
		t.Fatal(err)
	}
	if err := Run(database, "files", []Step{step}); err != nil || calls != 2 {
		t.Fatal("completed external work reran", calls, err)
	}
	step.Ensure = true
	if err := Run(database, "files", []Step{step}); err != nil || calls != 3 {
		t.Fatal("explicit repair was skipped", calls, err)
	}
	if count, err := Check(database, "files", []Step{step}); err != nil || count != 1 {
		t.Fatal("repair duplicated completion", count, err)
	}
	if err := Run(database, "sql", []Step{SQL("table", `CREATE TABLE protected(content TEXT)`), SQL("seed", `INSERT INTO protected(content) VALUES(?)`, "kept")}); err != nil {
		t.Fatal(err)
	}
	var content string
	if err := database.QueryRow(`SELECT content FROM protected`).Scan(&content); err != nil || content != "kept" {
		t.Fatal(content, err)
	}
	if err := Run(database, "sql", []Step{SQL("changed", `CREATE TABLE changed(content TEXT)`)}); err == nil {
		t.Fatal("mismatched history was applied")
	}
}

func TestInvalidPlansAndUnavailableDatabaseFailClosed(t *testing.T) {
	database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "invalid.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	for _, steps := range [][]Step{
		{{ID: "", Revision: "1"}}, {{ID: "one", Revision: ""}},
		{{ID: "one", Revision: "1"}, {ID: "one", Revision: "1"}},
		{{ID: "one", Revision: "1"}},
		{{ID: "one", Revision: "1", Apply: func(*sql.Tx) error { return nil }, Recoverable: func() error { return nil }}},
	} {
		if err := Run(database, "invalid", steps); err == nil {
			t.Fatal("invalid migration plan accepted")
		}
	}
	if _, err := Check(database, "invalid", []Step{{}}); err == nil {
		t.Fatal("invalid read plan accepted")
	}
	if _, err := Check(database, "absent", nil); err == nil {
		t.Fatal("missing journal accepted")
	}
	if err := Run(database, "bad-sql", []Step{SQL("bad", `INSERT INTO missing_table VALUES(1)`)}); err == nil {
		t.Fatal("bad SQL was recorded")
	}
	database.Close()
	if err := Run(database, "closed", []Step{SQL("table", `CREATE TABLE closed(content TEXT)`)}); err == nil {
		t.Fatal("closed database accepted")
	}
}
