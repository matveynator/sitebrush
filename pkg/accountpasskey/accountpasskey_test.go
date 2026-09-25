package accountpasskey

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
	_ "modernc.org/sqlite"
)

func TestUserIdentityAndCredentialAccessors(t *testing.T) {
	user := &User{Domain: " Example.COM ", Email: " Owner@Example.COM "}
	if len(user.WebAuthnID()) == 0 || user.WebAuthnName() != user.Email || user.WebAuthnDisplayName() != user.Email {
		t.Fatal("WebAuthn user accessors returned unexpected identity")
	}
	derivedHandle := user.WebAuthnID()
	user.Handle = []byte("stored-handle")
	if string(user.WebAuthnID()) != "stored-handle" || string(derivedHandle) == string(user.WebAuthnID()) {
		t.Fatal("stored user handle was not preferred")
	}
	credentials := []webauthn.Credential{{ID: []byte("credential")}}
	user.Credentials = credentials
	if len(user.WebAuthnCredentials()) != 1 || ChallengeTTL != 5*time.Minute {
		t.Fatal("WebAuthn credentials or challenge TTL are incorrect")
	}
}

func TestPasskeyPersistenceAndChallengeLifecycle(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1_700_000_000, 0).UTC()
	database, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "passkeys.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	for _, statement := range Schema() {
		if _, err := database.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if len(Schema()) != 4 {
		t.Fatal("passkey schema statement count changed")
	}
	if Count(ctx, database, "example.com") != 0 || UserCount(ctx, database, "example.com", "owner@example.com") != 0 {
		t.Fatal("empty passkey counts are nonzero")
	}
	handle := []byte("stable-user-handle")
	credential := webauthn.Credential{ID: []byte("credential-id")}
	credentialJSON, err := json.Marshal(credential)
	if err != nil {
		t.Fatal(err)
	}
	encodedHandle := base64.RawURLEncoding.EncodeToString(handle)
	credentialID := base64.RawURLEncoding.EncodeToString(credential.ID)
	if _, err := database.Exec(`INSERT INTO account_passkeys(domain,email,user_handle,credential_id,credential_json,created_at,last_used_at) VALUES(?,?,?,?,?,?,?)`, "example.com", "owner@example.com", encodedHandle, credentialID, string(credentialJSON), now.Unix(), 0); err != nil {
		t.Fatal(err)
	}
	if Count(ctx, database, "example.com") != 1 || UserCount(ctx, database, "example.com", "owner@example.com") != 1 {
		t.Fatal("stored passkey counts are incorrect")
	}
	credentials, err := List(ctx, database, "example.com", "owner@example.com")
	if err != nil || len(credentials) != 1 || credentials[0].ID != credentialID || !credentials[0].CreatedAt.Equal(now) || !credentials[0].LastUsed.IsZero() {
		t.Fatalf("listed credentials = %#v, %v", credentials, err)
	}
	loadedUser, err := loadUser(ctx, database, "example.com", "owner@example.com")
	if err != nil || string(loadedUser.Handle) != string(handle) || len(loadedUser.Credentials) != 1 {
		t.Fatalf("loaded WebAuthn user = %#v, %v", loadedUser, err)
	}
	byHandle, err := loadUserByHandle(ctx, database, "example.com", handle)
	if err != nil || byHandle.Email != "owner@example.com" {
		t.Fatalf("loaded user by handle = %#v, %v", byHandle, err)
	}
	if _, err := loadUserByHandle(ctx, database, "example.com", []byte("missing")); err == nil {
		t.Fatal("unknown user handle was accepted")
	}
	if _, err := service(" ", "https://example.com"); err == nil {
		t.Fatal("empty relying party was accepted")
	}
	if _, err := service("example.com", ""); err == nil {
		t.Fatal("empty WebAuthn origin was accepted")
	}
	if _, err := service("example.com", "https://example.com"); err != nil {
		t.Fatalf("valid WebAuthn configuration: %v", err)
	}
	transaction := mustBeginPasskey(t, database)
	registration, err := BeginRegistration(ctx, transaction, "example.com", "example.com", "https://example.com", "owner@example.com", "192.0.2.1", now)
	if err != nil || registration.Token == "" || registration.Options == nil {
		t.Fatalf("begin registration = %#v, %v", registration, err)
	}
	if err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}
	transaction = mustBeginPasskey(t, database)
	registrationSession, err := consumeChallenge(ctx, transaction, "example.com", "owner@example.com", "register", "192.0.2.1", registration.Token, now)
	if err != nil || registrationSession.Challenge == "" {
		t.Fatalf("consumed registration challenge = %#v, %v", registrationSession, err)
	}
	if err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}
	transaction = mustBeginPasskey(t, database)
	if err := FinishRegistration(ctx, transaction, "example.com", "example.com", "https://attacker.example", "owner@example.com", "192.0.2.1", registration.Token, nil, now); err == nil {
		t.Fatal("registration with a foreign origin was accepted")
	}
	if err := transaction.Rollback(); err != nil {
		t.Fatal(err)
	}
	transaction = mustBeginPasskey(t, database)
	result, err := BeginLogin(ctx, transaction, "example.com", "example.com", "https://example.com", "192.0.2.1", "/profile", now)
	if err != nil || result.Token == "" || result.Options == nil {
		t.Fatalf("begin login = %#v, %v", result, err)
	}
	if err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}
	transaction = mustBeginPasskey(t, database)
	if _, _, err := FinishLogin(ctx, transaction, "example.com", "example.com", "https://attacker.example", "192.0.2.1", result.Token, nil, now); err == nil {
		t.Fatal("login with a foreign origin was accepted")
	}
	if err := transaction.Rollback(); err != nil {
		t.Fatal(err)
	}
	transaction = mustBeginPasskey(t, database)
	session, returnPath, err := consumeLoginChallenge(ctx, transaction, "example.com", "192.0.2.1", result.Token, now)
	if err != nil || returnPath != "/profile" || session.Challenge == "" {
		t.Fatalf("consumed login challenge = %#v, %q, %v", session, returnPath, err)
	}
	if err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}
	transaction = mustBeginPasskey(t, database)
	if _, _, err := consumeLoginChallenge(ctx, transaction, "example.com", "192.0.2.1", result.Token, now); err == nil {
		t.Fatal("consumed login challenge was reused")
	}
	if err := transaction.Rollback(); err != nil {
		t.Fatal(err)
	}
	transaction = mustBeginPasskey(t, database)
	if err := Delete(ctx, transaction, "example.com", "owner@example.com", credentialID); err != nil {
		t.Fatal(err)
	}
	if err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}
	if Count(ctx, database, "example.com") != 0 {
		t.Fatal("deleted passkey remained in storage")
	}
}

func TestPasskeyChallengeExpiryAndMalformedStoredRows(t *testing.T) {
	ctx := context.Background()
	database, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "invalid-passkeys.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	for _, statement := range Schema() {
		if _, err := database.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Unix(1_700_000_000, 0).UTC()
	if _, err := database.Exec(`INSERT INTO account_webauthn_challenges(token,domain,email,kind,session_json,client_ip,return_path,created_at) VALUES(?,?,?,?,?,?,?,?)`, "expired", "example.com", "owner@example.com", "register", `{}`, "192.0.2.1", "", now.Add(-ChallengeTTL).Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO account_webauthn_challenges(token,domain,email,kind,session_json,client_ip,return_path,created_at) VALUES(?,?,?,?,?,?,?,?)`, "malformed", "example.com", "owner@example.com", "register", "not-json", "192.0.2.1", "", now.Unix()); err != nil {
		t.Fatal(err)
	}
	tx := mustBeginPasskey(t, database)
	if _, err := consumeChallenge(ctx, tx, "example.com", "owner@example.com", "register", "wrong-ip", "expired", now); err == nil {
		t.Fatal("challenge from another client IP accepted")
	}
	if _, err := consumeChallenge(ctx, tx, "example.com", "owner@example.com", "register", "192.0.2.1", "expired", now); err == nil {
		t.Fatal("expired challenge accepted")
	}
	if _, err := consumeChallenge(ctx, tx, "example.com", "owner@example.com", "register", "192.0.2.1", "malformed", now); err == nil {
		t.Fatal("malformed session accepted")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := BeginRegistration(ctx, mustBeginPasskey(t, database), "example.com", "example.com", "https://example.com", "missing@example.com", "192.0.2.1", now); err != nil {
		t.Fatalf("registration for user without credentials: %v", err)
	}
	if _, err := BeginLogin(ctx, mustBeginPasskey(t, database), "example.com", " ", "https://example.com", "192.0.2.1", "/", now); err == nil {
		t.Fatal("login with invalid relying party accepted")
	}
}

func TestPasskeyStorageErrorAndMalformedCredentialPaths(t *testing.T) {
	ctx := context.Background()
	database, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "malformed-passkeys.db"))
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range Schema() {
		if _, err := database.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := database.Exec(`INSERT INTO account_passkeys(domain,email,user_handle,credential_id,credential_json,created_at,last_used_at) VALUES(?,?,?,?,?,?,?)`, "example.com", "bad-handle@example.com", "%%%", "id1", `{}`, 1, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := loadUser(ctx, database, "example.com", "bad-handle@example.com"); err == nil {
		t.Fatal("malformed user handle accepted")
	}
	if _, err := database.Exec(`INSERT INTO account_passkeys(domain,email,user_handle,credential_id,credential_json,created_at,last_used_at) VALUES(?,?,?,?,?,?,?)`, "example.com", "bad-credential@example.com", base64.RawURLEncoding.EncodeToString([]byte("handle")), "id2", "not-json", 2, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := loadUser(ctx, database, "example.com", "bad-credential@example.com"); err == nil {
		t.Fatal("malformed credential accepted")
	}
	if _, err := loadUser(ctx, database, "example.com", "nobody@example.com"); err != nil {
		t.Fatalf("user without credentials: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if Count(ctx, database, "example.com") != 0 || UserCount(ctx, database, "example.com", "owner@example.com") != 0 {
		t.Fatal("query failures returned nonzero counts")
	}
	if _, err := List(ctx, database, "example.com", "owner@example.com"); err == nil {
		t.Fatal("List hid a closed database error")
	}
}

func mustBeginPasskey(t *testing.T, database *sql.DB) *sql.Tx {
	t.Helper()
	transaction, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	return transaction
}


// BEGIN WebAuthn transaction rollback regression tests.

func TestMalformedLoginChallengeRollbackPreservesChallenge(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	database, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "passkey-rollback.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	for _, statement := range Schema() {
		if _, err := database.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := database.Exec(`INSERT INTO account_webauthn_challenges(token,domain,email,kind,session_json,client_ip,return_path,created_at) VALUES(?,?,?,?,?,?,?,?)`,
		"malformed-login", "example.com", "owner@example.com", "login", "not-json", "192.0.2.80", "/profile", now.Unix()); err != nil {
		t.Fatal(err)
	}

	transaction := mustBeginPasskey(t, database)
	if _, _, err := consumeLoginChallenge(ctx, transaction, "example.com", "192.0.2.80", "malformed-login", now); err == nil {
		t.Fatal("malformed login challenge was accepted")
	}
	if err := transaction.Rollback(); err != nil {
		t.Fatal(err)
	}

	var challengeCount int
	if err := database.QueryRow(`SELECT COUNT(1) FROM account_webauthn_challenges WHERE token='malformed-login'`).Scan(&challengeCount); err != nil {
		t.Fatal(err)
	}
	if challengeCount != 1 {
		t.Fatalf("rollback left challenge count=%d want=1", challengeCount)
	}
}

func TestPasskeyChallengeCannotCrossDomainClientOrKind(t *testing.T) {
	ctx := context.Background()
	now := time.Now().UTC()
	database, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "passkey-boundary.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	for _, statement := range Schema() {
		if _, err := database.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	sessionJSON, err := json.Marshal(&webauthn.SessionData{Challenge: "challenge"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO account_webauthn_challenges(token,domain,email,kind,session_json,client_ip,return_path,created_at) VALUES(?,?,?,?,?,?,?,?)`,
		"bound-login", "example.com", "owner@example.com", "login", string(sessionJSON), "192.0.2.81", "/profile", now.Unix()); err != nil {
		t.Fatal(err)
	}

	for _, attempt := range []struct {
		name     string
		domain   string
		clientIP string
	}{
		{name: "domain", domain: "other.example.com", clientIP: "192.0.2.81"},
		{name: "client", domain: "example.com", clientIP: "192.0.2.82"},
	} {
		t.Run(attempt.name, func(t *testing.T) {
			transaction := mustBeginPasskey(t, database)
			if _, _, err := consumeLoginChallenge(ctx, transaction, attempt.domain, attempt.clientIP, "bound-login", now); err == nil {
				t.Fatal("cross-boundary login challenge was accepted")
			}
			if err := transaction.Rollback(); err != nil {
				t.Fatal(err)
			}
		})
	}

	transaction := mustBeginPasskey(t, database)
	if _, err := consumeChallenge(ctx, transaction, "example.com", "owner@example.com", "register", "192.0.2.81", "bound-login", now); err == nil {
		t.Fatal("login challenge was consumed as a registration challenge")
	}
	if err := transaction.Rollback(); err != nil {
		t.Fatal(err)
	}
}

// END WebAuthn transaction rollback regression tests.
