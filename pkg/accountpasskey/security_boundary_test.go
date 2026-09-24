package accountpasskey

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
	_ "modernc.org/sqlite"
)

func securityPasskeyDatabase(t *testing.T) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "security-passkeys.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	for _, statement := range Schema() {
		if _, err := database.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	return database
}

func storeSecurityChallenge(t *testing.T, database *sql.DB, domain, email, kind, ip, token, sessionJSON string, created time.Time) {
	t.Helper()
	_, err := database.Exec(
		"INSERT INTO account_webauthn_challenges(token,domain,email,kind,session_json,client_ip,return_path,created_at) VALUES(?,?,?,?,?,?,?,?)",
		token, domain, email, kind, sessionJSON, ip, "/return", created.Unix(),
	)
	if err != nil {
		t.Fatal(err)
	}
}

func TestSecurityBoundaryRegistrationChallengeBinding(t *testing.T) {
	database := securityPasskeyDatabase(t)
	ctx := context.Background()
	now := time.Unix(1_800_100_000, 0).UTC()
	session := webauthn.SessionData{Challenge: "challenge"}
	sessionJSON := "{\"challenge\":\"challenge\"}"
	storeSecurityChallenge(t, database, "example.com", "owner@example.com", "register", "192.0.2.40", "register-token", sessionJSON, now)

	tests := []struct {
		name   string
		domain string
		email  string
		kind   string
		ip     string
	}{
		{name: "domain", domain: "evil.example", email: "owner@example.com", kind: "register", ip: "192.0.2.40"},
		{name: "email", domain: "example.com", email: "other@example.com", kind: "register", ip: "192.0.2.40"},
		{name: "kind", domain: "example.com", email: "owner@example.com", kind: "login", ip: "192.0.2.40"},
		{name: "ip", domain: "example.com", email: "owner@example.com", kind: "register", ip: "192.0.2.41"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tx := mustBeginPasskey(t, database)
			if _, err := consumeChallenge(ctx, tx, tc.domain, tc.email, tc.kind, tc.ip, "register-token", now); err == nil {
				_ = tx.Rollback()
				t.Fatal("SECURITY: WebAuthn registration challenge crossed its binding boundary")
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
		})
	}

	tx := mustBeginPasskey(t, database)
	consumed, err := consumeChallenge(ctx, tx, "example.com", "owner@example.com", "register", "192.0.2.40", "register-token", now)
	if err != nil || consumed.Challenge != session.Challenge {
		_ = tx.Rollback()
		t.Fatalf("valid challenge = %#v, %v", consumed, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	tx = mustBeginPasskey(t, database)
	if _, err := consumeChallenge(ctx, tx, "example.com", "owner@example.com", "register", "192.0.2.40", "register-token", now); err == nil {
		_ = tx.Rollback()
		t.Fatal("SECURITY: consumed registration challenge was replayed")
	}
	_ = tx.Rollback()
}

func TestSecurityBoundaryLoginChallengeBindingExpiryAndReplay(t *testing.T) {
	database := securityPasskeyDatabase(t)
	ctx := context.Background()
	now := time.Unix(1_800_100_100, 0).UTC()
	sessionJSON := "{\"challenge\":\"login-challenge\"}"

	storeSecurityChallenge(t, database, "example.com", "", "login", "198.51.100.40", "login-token", sessionJSON, now)
	storeSecurityChallenge(t, database, "example.com", "", "login", "198.51.100.40", "expired-token", sessionJSON, now.Add(-ChallengeTTL))

	for _, tc := range []struct {
		name   string
		domain string
		ip     string
		token  string
	}{
		{name: "wrong domain", domain: "evil.example", ip: "198.51.100.40", token: "login-token"},
		{name: "wrong ip", domain: "example.com", ip: "198.51.100.41", token: "login-token"},
		{name: "expired", domain: "example.com", ip: "198.51.100.40", token: "expired-token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := mustBeginPasskey(t, database)
			if _, _, err := consumeLoginChallenge(ctx, tx, tc.domain, tc.ip, tc.token, now); err == nil {
				_ = tx.Rollback()
				t.Fatal("SECURITY: invalid WebAuthn login challenge was accepted")
			}
			_ = tx.Rollback()
		})
	}

	tx := mustBeginPasskey(t, database)
	session, path, err := consumeLoginChallenge(ctx, tx, "example.com", "198.51.100.40", "login-token", now)
	if err != nil || session.Challenge != "login-challenge" || path != "/return" {
		_ = tx.Rollback()
		t.Fatalf("valid login challenge = %#v, %q, %v", session, path, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	tx = mustBeginPasskey(t, database)
	if _, _, err := consumeLoginChallenge(ctx, tx, "example.com", "198.51.100.40", "login-token", now); err == nil {
		_ = tx.Rollback()
		t.Fatal("SECURITY: consumed WebAuthn login challenge was replayed")
	}
	_ = tx.Rollback()
}

func TestSecurityBoundaryMalformedLoginSessionRollsBackThroughFinishLogin(t *testing.T) {
	database := securityPasskeyDatabase(t)
	ctx := context.Background()
	now := time.Unix(1_800_100_200, 0).UTC()
	storeSecurityChallenge(t, database, "example.com", "", "login", "203.0.113.40", "malformed-login", "not-json", now)

	for attempt := 0; attempt < 2; attempt++ {
		tx := mustBeginPasskey(t, database)
		if _, _, err := FinishLogin(
			ctx,
			tx,
			"example.com",
			"example.com",
			"https://example.com",
			"203.0.113.40",
			"malformed-login",
			nil,
			now,
		); err == nil {
			_ = tx.Rollback()
			t.Fatal("SECURITY: malformed WebAuthn login session was accepted")
		}
		// Production accountTransaction rolls the transaction back when
		// FinishLogin returns an error. The challenge delete must therefore
		// roll back too rather than being tested under an artificial commit.
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}

		var count int
		if err := database.QueryRow(
			"SELECT COUNT(*) FROM account_webauthn_challenges WHERE token=?",
			"malformed-login",
		).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("SECURITY: rollback changed malformed challenge count to %d, want 1", count)
		}
	}
}

func TestSecurityBoundaryRelyingPartyConfigurationRejectsInvalidValues(t *testing.T) {
	for _, tc := range []struct {
		domain string
		origin string
	}{
		{"", "https://example.com"},
		{"example.com", ""},
		{" ", "https://example.com"},
	} {
		if _, err := service(tc.domain, tc.origin); err == nil {
			t.Fatalf("SECURITY: invalid WebAuthn relying party accepted domain=%q origin=%q", tc.domain, tc.origin)
		}
	}
}


func TestVerifiedPasskeyPersistenceHelpers(t *testing.T) {
	database := securityPasskeyDatabase(t)
	ctx := context.Background()
	now := time.Unix(1_800_100_300, 0).UTC()
	user := &User{Domain: "example.com", Email: "owner@example.com", Handle: []byte("handle")}
	credential := &webauthn.Credential{ID: []byte("credential-id")}

	if err := storeRegistrationCredential(ctx, database, "example.com", user.Email, user, credential, now); err != nil {
		t.Fatalf("store verified registration credential: %v", err)
	}
	if Count(ctx, database, "example.com") != 1 {
		t.Fatal("verified registration credential was not persisted")
	}

	email, path, err := finishLoginCredential(ctx, database, "example.com", user, user, credential, "/profile", now.Add(time.Minute))
	if err != nil || email != user.Email || path != "/profile" {
		t.Fatalf("finish verified login = %q %q %v", email, path, err)
	}

	other := &User{Domain: "example.com", Email: "other@example.com", Handle: []byte("other")}
	if _, _, err := finishLoginCredential(ctx, database, "example.com", user, other, credential, "/", now); err == nil {
		t.Fatal("SECURITY: passkey result was accepted for a different loaded account")
	}
	if _, _, err := finishLoginCredential(ctx, database, "example.com", user, nil, credential, "/", now); err == nil {
		t.Fatal("SECURITY: passkey result was accepted without a loaded account")
	}
	if _, _, err := finishLoginCredential(ctx, database, "example.com", structUser{}, user, credential, "/", now); err == nil {
		t.Fatal("SECURITY: foreign WebAuthn user implementation was accepted as SiteBrush account")
	}

	missingCredential := &webauthn.Credential{ID: []byte("missing-id")}
	if _, _, err := finishLoginCredential(ctx, database, "example.com", user, user, missingCredential, "/", now); err == nil {
		t.Fatal("SECURITY: login succeeded for credential absent from account storage")
	}

	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if err := storeRegistrationCredential(ctx, database, "example.com", user.Email, user, credential, now); err == nil {
		t.Fatal("registration persistence hid closed database error")
	}
}

type structUser struct{}

func (structUser) WebAuthnID() []byte                         { return []byte("foreign") }
func (structUser) WebAuthnName() string                       { return "foreign" }
func (structUser) WebAuthnDisplayName() string                { return "foreign" }
func (structUser) WebAuthnCredentials() []webauthn.Credential { return nil }

func TestFinishPasskeyRejectsMalformedBrowserResponsesAndConsumesChallenges(t *testing.T) {
	database := securityPasskeyDatabase(t)
	ctx := context.Background()
	now := time.Unix(1_800_100_400, 0).UTC()

	registrationTx := mustBeginPasskey(t, database)
	beginRegistration, err := BeginRegistration(ctx, registrationTx, "example.com", "example.com", "https://example.com", "owner@example.com", "192.0.2.90", now)
	if err != nil {
		_ = registrationTx.Rollback()
		t.Fatal(err)
	}
	if err := registrationTx.Commit(); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest("POST", "https://example.com/register", strings.NewReader("{}"))
	request.Header.Set("Content-Type", "application/json")
	registrationTx = mustBeginPasskey(t, database)
	if err := FinishRegistration(ctx, registrationTx, "example.com", "example.com", "https://example.com", "owner@example.com", "192.0.2.90", beginRegistration.Token, request, now); err == nil {
		_ = registrationTx.Rollback()
		t.Fatal("SECURITY: malformed passkey registration response was accepted")
	}
	if err := registrationTx.Commit(); err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err := database.QueryRow("SELECT COUNT(*) FROM account_webauthn_challenges WHERE token=?", beginRegistration.Token).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatal("SECURITY: failed registration response left challenge replayable")
	}

	loginTx := mustBeginPasskey(t, database)
	beginLogin, err := BeginLogin(ctx, loginTx, "example.com", "example.com", "https://example.com", "192.0.2.91", "/profile", now)
	if err != nil {
		_ = loginTx.Rollback()
		t.Fatal(err)
	}
	if err := loginTx.Commit(); err != nil {
		t.Fatal(err)
	}
	request = httptest.NewRequest("POST", "https://example.com/login", strings.NewReader("{}"))
	request.Header.Set("Content-Type", "application/json")
	loginTx = mustBeginPasskey(t, database)
	if _, _, err := FinishLogin(ctx, loginTx, "example.com", "example.com", "https://example.com", "192.0.2.91", beginLogin.Token, request, now); err == nil {
		_ = loginTx.Rollback()
		t.Fatal("SECURITY: malformed passkey login response was accepted")
	}
	if err := loginTx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow("SELECT COUNT(*) FROM account_webauthn_challenges WHERE token=?", beginLogin.Token).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatal("SECURITY: failed login response left challenge replayable")
	}
}

func TestPasskeyStorageAndTokenHelperBranches(t *testing.T) {
	database := securityPasskeyDatabase(t)
	ctx := context.Background()
	now := time.Unix(1_800_100_500, 0).UTC()
	handle := base64.RawURLEncoding.EncodeToString([]byte("handle"))
	credential := webauthn.Credential{ID: []byte("id")}
	encodedCredential, err := json.Marshal(credential)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(
		"INSERT INTO account_passkeys(domain,email,user_handle,credential_id,credential_json,created_at,last_used_at) VALUES(?,?,?,?,?,?,?)",
		"example.com", "owner@example.com", handle, base64.RawURLEncoding.EncodeToString(credential.ID), string(encodedCredential), now.Unix(), now.Unix(),
	); err != nil {
		t.Fatal(err)
	}
	list, err := List(ctx, database, "example.com", "owner@example.com")
	if err != nil || len(list) != 1 || list[0].LastUsed.IsZero() {
		t.Fatalf("passkey list = %#v, %v", list, err)
	}

	tx := mustBeginPasskey(t, database)
	result, err := storeChallenge(ctx, tx, "example.com", "owner@example.com", "register", "203.0.113.90", "/", map[string]any{"ok": true}, &webauthn.SessionData{Challenge: "challenge"}, now)
	if err != nil || result.Token == "" || result.Options == nil {
		_ = tx.Rollback()
		t.Fatalf("store challenge = %#v, %v", result, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	first, err := randomToken()
	if err != nil {
		t.Fatal(err)
	}
	second, err := randomToken()
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 64 || len(second) != 64 || first == second {
		t.Fatalf("random passkey token properties = %q %q", first, second)
	}
}


func TestSecurityBoundaryFinishRegistrationFailsClosedAfterValidChallenge(t *testing.T) {
	database := securityPasskeyDatabase(t)
	ctx := context.Background()
	now := time.Unix(1_800_100_600, 0).UTC()
	sessionJSON, err := json.Marshal(&webauthn.SessionData{Challenge: "registration-challenge"})
	if err != nil {
		t.Fatal(err)
	}

	storeSecurityChallenge(
		t,
		database,
		"example.com",
		"owner@example.com",
		"register",
		"192.0.2.100",
		"registration-config-error",
		string(sessionJSON),
		now,
	)
	tx := mustBeginPasskey(t, database)
	if err := FinishRegistration(
		ctx,
		tx,
		"example.com",
		" ",
		"https://example.com",
		"owner@example.com",
		"192.0.2.100",
		"registration-config-error",
		nil,
		now,
	); err == nil {
		_ = tx.Rollback()
		t.Fatal("SECURITY: registration continued with an invalid relying party")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}

	storeSecurityChallenge(
		t,
		database,
		"example.com",
		"owner@example.com",
		"register",
		"192.0.2.101",
		"registration-storage-error",
		string(sessionJSON),
		now,
	)
	if _, err := database.Exec("DROP TABLE account_passkeys"); err != nil {
		t.Fatal(err)
	}
	tx = mustBeginPasskey(t, database)
	if err := FinishRegistration(
		ctx,
		tx,
		"example.com",
		"example.com",
		"https://example.com",
		"owner@example.com",
		"192.0.2.101",
		"registration-storage-error",
		nil,
		now,
	); err == nil {
		_ = tx.Rollback()
		t.Fatal("SECURITY: registration continued after passkey storage failure")
	}
	_ = tx.Rollback()
}

func TestSecurityBoundaryFinishLoginFailsClosedAfterValidChallenge(t *testing.T) {
	database := securityPasskeyDatabase(t)
	ctx := context.Background()
	now := time.Unix(1_800_100_700, 0).UTC()
	sessionJSON, err := json.Marshal(&webauthn.SessionData{Challenge: "login-challenge"})
	if err != nil {
		t.Fatal(err)
	}
	storeSecurityChallenge(
		t,
		database,
		"example.com",
		"",
		"login",
		"198.51.100.100",
		"login-config-error",
		string(sessionJSON),
		now,
	)

	tx := mustBeginPasskey(t, database)
	if _, _, err := FinishLogin(
		ctx,
		tx,
		"example.com",
		" ",
		"https://example.com",
		"198.51.100.100",
		"login-config-error",
		nil,
		now,
	); err == nil {
		_ = tx.Rollback()
		t.Fatal("SECURITY: login continued with an invalid relying party")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
}
