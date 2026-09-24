package accountauth

import (
	"context"
	"database/sql"
	_ "modernc.org/sqlite"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testDatabase(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "auth.db"))
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	for _, query := range append(Schema(), `CREATE TABLE users(domain TEXT,email TEXT,password TEXT,is_admin INTEGER)`, `CREATE TABLE sessions(token TEXT,user_email TEXT,created_at TEXT,client_ip TEXT,security_version INTEGER)`, `INSERT INTO users VALUES('example.org','owner@example.org','password',1)`) {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	return db
}
func transact(t *testing.T, db *sql.DB, call func(*sql.Tx) (Outcome, error)) Outcome {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	result, err := call(tx)
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return result
}
func TestLoginAlwaysRequiresMailWithoutSession(t *testing.T) {
	db := testDatabase(t)
	ctx := context.Background()
	now := time.Now()
	challenge := transact(t, db, func(tx *sql.Tx) (Outcome, error) {
		return Password(ctx, tx, "example.org", "owner@example.org", "password", "192.0.2.1", "/edit/", "ru", now)
	})
	if challenge.Status != "code" || len(challenge.Code) != 6 {
		t.Fatalf("challenge: %#v", challenge)
	}
	var sessions int
	db.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&sessions)
	if sessions != 0 {
		t.Fatal("session created before verification")
	}
	verify := func(token, code, ip string, at time.Time) Outcome {
		return transact(t, db, func(tx *sql.Tx) (Outcome, error) { return Verify(ctx, tx, "example.org", token, code, ip, at) })
	}
	if verify(challenge.Token, challenge.Code, "192.0.2.2", now).Status != "invalid" {
		t.Fatal("wrong IP accepted")
	}
	if verify(challenge.Token, challenge.Code, "192.0.2.1", now).Status != "session" {
		t.Fatal("valid code rejected")
	}
	if verify(challenge.Token, challenge.Code, "192.0.2.1", now).Status != "invalid" {
		t.Fatal("code reused")
	}
	next := transact(t, db, func(tx *sql.Tx) (Outcome, error) {
		return Password(ctx, tx, "example.org", "owner@example.org", "password", "192.0.2.1", "/", "en", now.Add(time.Minute))
	})
	if next.Status != "code" {
		t.Fatal("known IP bypassed email verification")
	}
	for attempt := 0; attempt < 5; attempt++ {
		verify(next.Token, "abcdef", "192.0.2.1", now.Add(time.Minute))
	}
	if verify(next.Token, next.Code, "192.0.2.1", now.Add(time.Minute)).Status != "invalid" {
		t.Fatal("attempt limit bypassed")
	}
}

func TestCredentialsAndSingleUseLoginLink(t *testing.T) {
	database := testDatabase(t)
	ctx := context.Background()
	now := time.Now().UTC()
	transaction, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if matched, err := Credentials(ctx, transaction, "example.org", "owner@example.org", "password"); err != nil || !matched {
		t.Fatalf("valid credentials = %t, %v", matched, err)
	}
	if matched, err := Credentials(ctx, transaction, "example.org", "owner@example.org", "incorrect"); err != nil || matched {
		t.Fatalf("invalid credentials = %t, %v", matched, err)
	}
	if matched, err := Credentials(ctx, transaction, "example.org", "missing@example.org", "password"); err != nil || matched {
		t.Fatalf("unknown account credentials = %t, %v", matched, err)
	}
	if err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}
	challenge := transact(t, database, func(tx *sql.Tx) (Outcome, error) {
		return Challenge(ctx, tx, "example.org", "owner@example.org", "192.0.2.10", "/profile", "en", now)
	})
	if challenge.Status != "code" {
		t.Fatalf("login challenge = %#v", challenge)
	}
	link := transact(t, database, func(tx *sql.Tx) (Outcome, error) {
		return VerifyLink(ctx, tx, "example.org", challenge.Token, "192.0.2.10", now.Add(time.Second))
	})
	if link.Status != "session" || link.Email != "owner@example.org" || link.Path != "/profile" || link.Language != "en" || link.Token == "" {
		t.Fatalf("verified login link = %#v", link)
	}
	if reused := transact(t, database, func(tx *sql.Tx) (Outcome, error) {
		return VerifyLink(ctx, tx, "example.org", challenge.Token, "192.0.2.10", now.Add(2*time.Second))
	}); reused.Status != "invalid" {
		t.Fatalf("reused login link = %#v", reused)
	}
	if invalid := transact(t, database, func(tx *sql.Tx) (Outcome, error) {
		return VerifyLink(ctx, tx, "example.org", challenge.Token, "192.0.2.11", now)
	}); invalid.Status != "invalid" {
		t.Fatalf("wrong address login link = %#v", invalid)
	}
}
func TestResendExpiryAndUnknownAddress(t *testing.T) {
	db := testDatabase(t)
	ctx := context.Background()
	now := time.Now()
	request := func(at time.Time) Outcome {
		return transact(t, db, func(tx *sql.Tx) (Outcome, error) {
			return Password(ctx, tx, "example.org", "owner@example.org", "password", "", "/", "en", at)
		})
	}
	first := request(now)
	duplicate := request(now.Add(time.Second))
	if duplicate.Status != "code_existing" || duplicate.Token != first.Token || duplicate.Code != "" {
		t.Fatalf("duplicate request did not reuse pending challenge: %#v", duplicate)
	}
	second := request(now.Add(CodeSendCooldown))
	verify := func(challenge Outcome, at time.Time) Outcome {
		return transact(t, db, func(tx *sql.Tx) (Outcome, error) {
			return Verify(ctx, tx, "example.org", challenge.Token, challenge.Code, "", at)
		})
	}
	if verify(first, now.Add(CodeSendCooldown)).Status != "invalid" {
		t.Fatal("old code accepted")
	}
	if verify(second, now.Add(CodeSendCooldown)).Status != "session" {
		t.Fatal("unknown IP could not sign in")
	}
	var count int
	db.QueryRow(`SELECT COUNT(*) FROM account_trusted_ips`).Scan(&count)
	if count != 0 {
		t.Fatal("unknown IP remembered")
	}
	third := request(now.Add(2 * CodeSendCooldown))
	for attempt := 3; attempt < CodeSendLimit; attempt++ {
		at := now.Add(time.Duration(attempt) * CodeSendCooldown)
		if request(at).Status != "code" {
			t.Fatalf("send %d unexpectedly limited", attempt+1)
		}
	}
	if request(now.Add(time.Duration(CodeSendLimit)*CodeSendCooldown)).Status != "limited" {
		t.Fatal("send quota missing")
	}
	if verify(third, now.Add(CodeTTL+2*CodeSendCooldown)).Status != "invalid" {
		t.Fatal("expired code accepted")
	}
}
func TestTrustedProxyBoundaryAndRedaction(t *testing.T) {
	for _, check := range []struct{ peer, forwarded, trust, want string }{
		{"192.0.2.1:80", "203.0.113.4", "", "192.0.2.1"},
		{"127.0.0.1:80", "203.0.113.4", "", "203.0.113.4"},
		{"127.0.0.1:80", "", "", ""},
		{"10.0.0.1:80", "203.0.113.4", "", "10.0.0.1"},
		{"10.0.0.1:80", "198.51.100.9, 203.0.113.4", "10.0.0.0/8", "203.0.113.4"},
		{"[::ffff:192.0.2.1]:80", "", "", "192.0.2.1"},
	} {
		r := httptest.NewRequest("GET", "http://example.org", nil)
		r.RemoteAddr = check.peer
		r.Header.Set("X-Forwarded-For", check.forwarded)
		if got := ClientIP(r, check.trust); got != check.want {
			t.Errorf("%+v: %q", check, got)
		}
	}
	if query := SafeQuery("email_confirm=secret&login_challenge=secret&campaign=hello"); strings.Contains(query, "secret") || !strings.Contains(query, "campaign=hello") {
		t.Fatal(query)
	}
}

func TestRevokeOnlySelectedAccountAddress(t *testing.T) {
	database := testDatabase(t)
	ctx := context.Background()
	now := time.Now()
	transact(t, database, func(transaction *sql.Tx) (Outcome, error) {
		for _, address := range []string{"192.0.2.1", "192.0.2.2"} {
			if err := RememberAddress(ctx, transaction, "example.org", "owner@example.org", address, now); err != nil {
				return Outcome{}, err
			}
			if _, err := Session(ctx, transaction, "example.org", "owner@example.org", address, now); err != nil {
				return Outcome{}, err
			}
		}
		return Outcome{}, Revoke(ctx, transaction, "example.org", "owner@example.org", "192.0.2.1")
	})
	for _, table := range []string{"sessions", "account_trusted_ips"} {
		var count int
		if err := database.QueryRow(`SELECT COUNT(*) FROM ` + table + ` WHERE client_ip='192.0.2.1'`).Scan(&count); err != nil || count != 0 {
			t.Fatalf("revocation %s: %d %v", table, count, err)
		}
		if err := database.QueryRow(`SELECT COUNT(*) FROM ` + table + ` WHERE client_ip='192.0.2.2'`).Scan(&count); err != nil || count != 1 {
			t.Fatalf("unrelated address %s: %d %v", table, count, err)
		}
	}
}

func TestPasswordSnapshotUsesSaltAndRejectsChangedPassword(t *testing.T) {
	first, err := passwordSnapshot("password", "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := passwordSnapshot("password", "")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("snapshot salt reused")
	}
	same, err := passwordSnapshot("password", first)
	if err != nil || same != first {
		t.Fatal("snapshot cannot be verified")
	}
	changed, err := passwordSnapshot("changed", first)
	if err != nil || changed == first {
		t.Fatal("password change undetected")
	}
	if _, err := passwordSnapshot("password", strings.Repeat("0", 64)); err == nil {
		t.Fatal("obsolete fast snapshot accepted")
	}
}
