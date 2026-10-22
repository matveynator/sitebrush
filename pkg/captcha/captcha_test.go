package captcha

import (
	"bytes"
	"database/sql"
	"image/png"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func captchaDatabase(t *testing.T) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "captcha.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	database.SetMaxOpenConns(1)
	for _, query := range SchemaQueries() {
		if _, err := database.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	return database
}

func captchaTransaction(t *testing.T, database *sql.DB, run func(*sql.Tx) (bool, error)) bool {
	t.Helper()
	transaction, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.Rollback()
	valid, err := run(transaction)
	if err != nil {
		t.Fatal(err)
	}
	if err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}
	return valid
}

func TestChallengeIsSingleUseAndBoundToRequest(t *testing.T) {
	for _, scenario := range []struct {
		name, ip, domain, purpose, answer string
		age                               time.Duration
		valid                             bool
	}{
		{"valid", "192.0.2.10", "example.com", "register", "ab234x", 3 * time.Second, true},
		{"wrong answer", "192.0.2.10", "example.com", "register", "WRONGX", 3 * time.Second, false},
		{"wrong IP", "192.0.2.11", "example.com", "register", "AB234X", 3 * time.Second, false},
		{"wrong domain", "192.0.2.10", "other.com", "register", "AB234X", 3 * time.Second, false},
		{"wrong purpose", "192.0.2.10", "example.com", "recover", "AB234X", 3 * time.Second, false},
		{"expired", "192.0.2.10", "example.com", "register", "AB234X", Lifetime + time.Second, false},
		{"future", "192.0.2.10", "example.com", "register", "AB234X", -time.Second, false},
		{"too fast", "192.0.2.10", "example.com", "register", "AB234X", time.Second, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			database := captchaDatabase(t)
			now := time.Now().UTC()
			challenge, err := New()
			if err != nil {
				t.Fatal(err)
			}
			challenge.Answer = "AB234X"
			if !captchaTransaction(t, database, func(transaction *sql.Tx) (bool, error) {
				return Save(transaction, challenge, "192.0.2.10", "example.com", "register", now.Add(-scenario.age))
			}) {
				t.Fatal("challenge not issued")
			}
			valid := captchaTransaction(t, database, func(transaction *sql.Tx) (bool, error) {
				return Verify(transaction, challenge.Token, scenario.answer, scenario.ip, scenario.domain, scenario.purpose, now)
			})
			if valid != scenario.valid {
				t.Fatalf("verification=%t", valid)
			}
			if captchaTransaction(t, database, func(transaction *sql.Tx) (bool, error) {
				return Verify(transaction, challenge.Token, challenge.Answer, "192.0.2.10", "example.com", "register", now.Add(3*time.Second))
			}) {
				t.Fatal("spent challenge accepted")
			}
		})
	}
}

func TestClientSelectedCookieCannotSolveChallenge(t *testing.T) {
	database := captchaDatabase(t)
	if captchaTransaction(t, database, func(transaction *sql.Tx) (bool, error) {
		return Verify(transaction, "1234", "1234", "192.0.2.10", "example.com", "recover", time.Now())
	}) {
		t.Fatal("forged answer cookie accepted")
	}
}

func TestChallengeImageIsRasterAndIssuanceIsBounded(t *testing.T) {
	challenge, err := New()
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := Render(challenge.Answer)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := png.Decode(bytes.NewReader(encoded))
	if err != nil || decoded.Bounds().Dx() != 320 || decoded.Bounds().Dy() != 100 {
		t.Fatal("invalid challenge image", err)
	}
	if bytes.Contains(encoded, []byte(challenge.Token)) || bytes.Contains(encoded, []byte(challenge.Answer)) {
		t.Fatal("challenge metadata embedded in image")
	}
	database := captchaDatabase(t)
	now := time.Now()
	for index := 0; index < 7; index++ {
		fresh, err := New()
		if err != nil {
			t.Fatal(err)
		}
		issued := captchaTransaction(t, database, func(transaction *sql.Tx) (bool, error) {
			return Save(transaction, fresh, "192.0.2.10", "example.com", "recover", now)
		})
		if issued != (index < 6) {
			t.Fatalf("issuance limit failed at %d", index)
		}
	}
}
