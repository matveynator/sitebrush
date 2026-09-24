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

func passkeyCoverageDatabase(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "passkey-extra.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, statement := range Schema() {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func TestPasskeyVerifiedCredentialPersistenceAndAccountBinding(t *testing.T) {
	db := passkeyCoverageDatabase(t)
	ctx := context.Background()
	now := time.Unix(1_800_500_000, 0).UTC()
	user := &User{Domain: "example.com", Email: "owner@example.com", Handle: []byte("handle")}
	credential := &webauthn.Credential{ID: []byte("credential-id")}

	if err := storeRegistrationCredential(ctx, db, "example.com", user.Email, user, credential, now); err != nil {
		t.Fatal(err)
	}
	credentialID := base64.RawURLEncoding.EncodeToString(credential.ID)
	var storedHandle, storedJSON string
	if err := db.QueryRow(
		"SELECT user_handle,credential_json FROM account_passkeys WHERE domain=? AND email=? AND credential_id=?",
		"example.com", user.Email, credentialID,
	).Scan(&storedHandle, &storedJSON); err != nil {
		t.Fatal(err)
	}
	if storedHandle != base64.RawURLEncoding.EncodeToString(user.Handle) {
		t.Fatalf("stored handle=%q", storedHandle)
	}
	var storedCredential webauthn.Credential
	if err := json.Unmarshal([]byte(storedJSON), &storedCredential); err != nil || string(storedCredential.ID) != "credential-id" {
		t.Fatalf("stored credential=%#v err=%v", storedCredential, err)
	}

	updatedCredential := &webauthn.Credential{ID: []byte("credential-id")}
	email, returnPath, err := finishLoginCredential(ctx, db, "example.com", user, user, updatedCredential, "/admin", now.Add(time.Minute))
	if err != nil || email != user.Email || returnPath != "/admin" {
		t.Fatalf("finish login credential=%q %q err=%v", email, returnPath, err)
	}
	var lastUsed int64
	if err := db.QueryRow(
		"SELECT last_used_at FROM account_passkeys WHERE domain=? AND email=? AND credential_id=?",
		"example.com", user.Email, credentialID,
	).Scan(&lastUsed); err != nil {
		t.Fatal(err)
	}
	if lastUsed != now.Add(time.Minute).Unix() {
		t.Fatalf("last_used_at=%d", lastUsed)
	}

	other := &User{Domain: "example.com", Email: "attacker@example.com", Handle: []byte("other")}
	if _, _, err := finishLoginCredential(ctx, db, "example.com", user, other, updatedCredential, "/", now); err == nil {
		t.Fatal("SECURITY: passkey validated user was rebound to a different loaded account")
	}
	if _, _, err := finishLoginCredential(ctx, db, "example.com", user, nil, updatedCredential, "/", now); err == nil {
		t.Fatal("SECURITY: passkey login succeeded without a loaded account")
	}
	if _, _, err := finishLoginCredential(ctx, db, "example.com", user, user, &webauthn.Credential{ID: []byte("missing")}, "/", now); err == nil {
		t.Fatal("SECURITY: unknown passkey credential ID was accepted")
	}
}

func TestPasskeyStorageFailsClosedWhenSchemaUnavailable(t *testing.T) {
	db := passkeyCoverageDatabase(t)
	ctx := context.Background()
	now := time.Unix(1_800_500_100, 0).UTC()
	if _, err := db.Exec("DROP TABLE account_passkeys"); err != nil {
		t.Fatal(err)
	}

	if _, err := loadUser(ctx, db, "example.com", "owner@example.com"); err == nil {
		t.Fatal("SECURITY: passkey user load hid storage failure")
	}
	if _, err := loadUserByHandle(ctx, db, "example.com", []byte("handle")); err == nil {
		t.Fatal("SECURITY: passkey handle lookup hid storage failure")
	}
	user := &User{Domain:"example.com", Email:"owner@example.com", Handle:[]byte("handle")}
	if err := storeRegistrationCredential(ctx, db, "example.com", user.Email, user, &webauthn.Credential{ID:[]byte("id")}, now); err == nil {
		t.Fatal("SECURITY: passkey registration persistence hid storage failure")
	}
	if _, _, err := finishLoginCredential(ctx, db, "example.com", user, user, &webauthn.Credential{ID:[]byte("id")}, "/", now); err == nil {
		t.Fatal("SECURITY: passkey login persistence hid storage failure")
	}
}

func TestPasskeyChallengeStorageAndBeginFailClosedBranches(t *testing.T) {
	db := passkeyCoverageDatabase(t)
	ctx := context.Background()
	now := time.Unix(1_800_500_200, 0).UTC()

	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("DROP TABLE account_webauthn_challenges"); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if _, err := BeginLogin(ctx, tx, "example.com", "example.com", "https://example.com", "192.0.2.70", "/", now); err == nil {
		_ = tx.Rollback()
		t.Fatal("SECURITY: WebAuthn login began without challenge storage")
	}
	_ = tx.Rollback()

	tx, _ = db.Begin()
	if _, err := BeginRegistration(ctx, tx, "example.com", " ", "https://example.com", "owner@example.com", "192.0.2.70", now); err == nil {
		_ = tx.Rollback()
		t.Fatal("invalid registration relying party was accepted")
	}
	_ = tx.Rollback()

	tx, _ = db.Begin()
	if _, err := BeginLogin(ctx, tx, "example.com", " ", "https://example.com", "192.0.2.70", "/", now); err == nil {
		_ = tx.Rollback()
		t.Fatal("invalid login relying party was accepted")
	}
	_ = tx.Rollback()
}

func TestPasskeyChallengeExpiryAndMalformedJSONBranches(t *testing.T) {
	db := passkeyCoverageDatabase(t)
	ctx := context.Background()
	now := time.Unix(1_800_500_300, 0).UTC()

	for _, row := range []struct{
		token, kind, email, body string
		at time.Time
	}{
		{"expired-register","register","owner@example.com",`{"challenge":"x"}`,now.Add(-ChallengeTTL)},
		{"bad-register","register","owner@example.com","{",now},
		{"expired-login","login","",`{"challenge":"x"}`,now.Add(-ChallengeTTL)},
		{"bad-login","login","","{",now},
	} {
		if _, err := db.Exec(
			"INSERT INTO account_webauthn_challenges(token,domain,email,kind,session_json,client_ip,return_path,created_at) VALUES(?,?,?,?,?,?,?,?)",
			row.token,"example.com",row.email,row.kind,row.body,"192.0.2.71","/return",row.at.Unix(),
		); err != nil { t.Fatal(err) }
	}

	tx,_:=db.Begin()
	if _,err:=consumeChallenge(ctx,tx,"example.com","owner@example.com","register","192.0.2.71","expired-register",now); err==nil {
		_ = tx.Rollback(); t.Fatal("expired registration challenge accepted")
	}
	_ = tx.Rollback()
	tx,_=db.Begin()
	if _,err:=consumeChallenge(ctx,tx,"example.com","owner@example.com","register","192.0.2.71","bad-register",now); err==nil {
		_ = tx.Rollback(); t.Fatal("malformed registration challenge accepted")
	}
	_ = tx.Rollback()
	tx,_=db.Begin()
	if _,_,err:=consumeLoginChallenge(ctx,tx,"example.com","192.0.2.71","expired-login",now); err==nil {
		_ = tx.Rollback(); t.Fatal("expired login challenge accepted")
	}
	_ = tx.Rollback()
	tx,_=db.Begin()
	if _,_,err:=consumeLoginChallenge(ctx,tx,"example.com","192.0.2.71","bad-login",now); err==nil {
		_ = tx.Rollback(); t.Fatal("malformed login challenge accepted")
	}
	_ = tx.Rollback()
}
