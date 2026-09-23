// Package accountpasskey implements WebAuthn passkeys for SiteBrush accounts.
package accountpasskey

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

const ChallengeTTL = 5 * time.Minute

type User struct {
	Domain      string
	Email       string
	Credentials []webauthn.Credential
}

func (user *User) WebAuthnID() []byte {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(user.Domain)) + "\x00" + strings.ToLower(strings.TrimSpace(user.Email))))
	return sum[:]
}

func (user *User) WebAuthnName() string {
	return user.Email
}

func (user *User) WebAuthnDisplayName() string {
	return user.Email
}

func (user *User) WebAuthnCredentials() []webauthn.Credential {
	return user.Credentials
}

type CredentialInfo struct {
	ID        string
	CreatedAt time.Time
	LastUsed  time.Time
}

type BeginResult struct {
	Token   string `json:"token"`
	Options any    `json:"options"`
}

func Schema() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS account_passkeys(domain TEXT,email TEXT,user_handle TEXT,credential_id TEXT,credential_json TEXT,created_at INTEGER,last_used_at INTEGER,PRIMARY KEY(domain,credential_id))`,
		`CREATE TABLE IF NOT EXISTS account_webauthn_challenges(token TEXT PRIMARY KEY,domain TEXT,email TEXT,kind TEXT,session_json TEXT,client_ip TEXT,return_path TEXT,created_at INTEGER)`,
		`CREATE INDEX IF NOT EXISTS idx_account_passkeys_user ON account_passkeys(domain,email)`,
		`CREATE INDEX IF NOT EXISTS idx_account_passkeys_handle ON account_passkeys(domain,user_handle)`,
	}
}

func Count(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, domain string) int {
	var count int
	if database.QueryRowContext(ctx, `SELECT COUNT(1) FROM account_passkeys WHERE domain=?`, domain).Scan(&count) != nil {
		return 0
	}
	return count
}

func UserCount(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, domain, email string) int {
	var count int
	if database.QueryRowContext(ctx, `SELECT COUNT(1) FROM account_passkeys WHERE domain=? AND email=?`, domain, email).Scan(&count) != nil {
		return 0
	}
	return count
}

func List(ctx context.Context, database interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, domain, email string) ([]CredentialInfo, error) {
	rows, err := database.QueryContext(ctx, `SELECT credential_id,created_at,last_used_at FROM account_passkeys WHERE domain=? AND email=? ORDER BY created_at DESC`, domain, email)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var credentials []CredentialInfo
	for rows.Next() {
		var credential CredentialInfo
		var createdAt, lastUsedAt int64
		if err = rows.Scan(&credential.ID, &createdAt, &lastUsedAt); err != nil {
			return nil, err
		}
		credential.CreatedAt = time.Unix(createdAt, 0).UTC()
		if lastUsedAt > 0 {
			credential.LastUsed = time.Unix(lastUsedAt, 0).UTC()
		}
		credentials = append(credentials, credential)
	}
	return credentials, rows.Err()
}

func Delete(ctx context.Context, transaction *sql.Tx, domain, email, credentialID string) error {
	_, err := transaction.ExecContext(ctx, `DELETE FROM account_passkeys WHERE domain=? AND email=? AND credential_id=?`, domain, email, credentialID)
	return err
}

func BeginRegistration(ctx context.Context, transaction *sql.Tx, domain, rpID, origin, email, clientIP string, now time.Time) (BeginResult, error) {
	user, err := loadUser(ctx, transaction, domain, email)
	if err != nil {
		return BeginResult{}, err
	}
	service, err := service(rpID, origin)
	if err != nil {
		return BeginResult{}, err
	}
	options, session, err := service.BeginMediatedRegistration(user, protocol.MediationDefault,
		webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementRequired),
		webauthn.WithExclusions(webauthn.Credentials(user.Credentials).CredentialDescriptors()),
		webauthn.WithExtensions(map[string]any{"credProps": true}),
	)
	if err != nil {
		return BeginResult{}, err
	}
	return storeChallenge(ctx, transaction, domain, email, "register", clientIP, "", options, session, now)
}

func FinishRegistration(ctx context.Context, database *sql.Tx, domain, rpID, origin, email, clientIP, token string, request *http.Request, now time.Time) error {
	session, err := consumeChallenge(ctx, database, domain, email, "register", clientIP, token, now)
	if err != nil {
		return err
	}
	user, err := loadUser(ctx, database, domain, email)
	if err != nil {
		return err
	}
	service, err := service(rpID, origin)
	if err != nil {
		return err
	}
	credential, err := service.FinishRegistration(user, session, request)
	if err != nil {
		return err
	}
	credentialJSON, err := json.Marshal(credential)
	if err != nil {
		return err
	}
	credentialID := base64.RawURLEncoding.EncodeToString(credential.ID)
	userHandle := base64.RawURLEncoding.EncodeToString(user.WebAuthnID())
	_, err = database.ExecContext(ctx, `INSERT INTO account_passkeys(domain,email,user_handle,credential_id,credential_json,created_at,last_used_at) VALUES(?,?,?,?,?,?,0)`,
		domain, email, userHandle, credentialID, string(credentialJSON), now.Unix())
	return err
}

func BeginLogin(ctx context.Context, transaction *sql.Tx, domain, rpID, origin, clientIP, returnPath string, now time.Time) (BeginResult, error) {
	service, err := service(rpID, origin)
	if err != nil {
		return BeginResult{}, err
	}
	options, session, err := service.BeginDiscoverableMediatedLogin(protocol.MediationDefault)
	if err != nil {
		return BeginResult{}, err
	}
	return storeChallenge(ctx, transaction, domain, "", "login", clientIP, returnPath, options, session, now)
}

func FinishLogin(ctx context.Context, database *sql.Tx, domain, rpID, origin, clientIP, token string, request *http.Request, now time.Time) (email, returnPath string, err error) {
	session, returnPath, err := consumeLoginChallenge(ctx, database, domain, clientIP, token, now)
	if err != nil {
		return "", "", err
	}
	service, err := service(rpID, origin)
	if err != nil {
		return "", "", err
	}
	var loadedUser *User
	handler := func(rawID, userHandle []byte) (webauthn.User, error) {
		user, loadErr := loadUserByHandle(ctx, database, domain, userHandle)
		if loadErr != nil {
			return nil, loadErr
		}
		loadedUser = user
		return user, nil
	}
	validatedUser, credential, err := service.FinishPasskeyLogin(handler, session, request)
	if err != nil {
		return "", "", err
	}
	user, ok := validatedUser.(*User)
	if !ok || loadedUser == nil || !strings.EqualFold(user.Email, loadedUser.Email) {
		return "", "", errors.New("invalid passkey account")
	}
	credentialJSON, err := json.Marshal(credential)
	if err != nil {
		return "", "", err
	}
	credentialID := base64.RawURLEncoding.EncodeToString(credential.ID)
	result, err := database.ExecContext(ctx, `UPDATE account_passkeys SET credential_json=?,last_used_at=? WHERE domain=? AND email=? AND credential_id=?`,
		string(credentialJSON), now.Unix(), domain, user.Email, credentialID)
	if err != nil {
		return "", "", err
	}
	updated, err := result.RowsAffected()
	if err != nil || updated != 1 {
		if err == nil {
			err = errors.New("passkey credential not found")
		}
		return "", "", err
	}
	return user.Email, returnPath, nil
}

func service(domain, origin string) (*webauthn.WebAuthn, error) {
	domain = strings.TrimSpace(strings.ToLower(domain))
	origin = strings.TrimSpace(origin)
	if domain == "" || origin == "" {
		return nil, errors.New("invalid WebAuthn relying party")
	}
	return webauthn.New(&webauthn.Config{
		RPDisplayName: "SiteBrush",
		RPID:          domain,
		RPOrigins:     []string{origin},
	})
}

func loadUser(ctx context.Context, database interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, domain, email string) (*User, error) {
	user := &User{Domain: domain, Email: email}
	rows, err := database.QueryContext(ctx, `SELECT credential_json FROM account_passkeys WHERE domain=? AND email=? ORDER BY created_at`, domain, email)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var encoded string
		if err = rows.Scan(&encoded); err != nil {
			return nil, err
		}
		var credential webauthn.Credential
		if err = json.Unmarshal([]byte(encoded), &credential); err != nil {
			return nil, err
		}
		user.Credentials = append(user.Credentials, credential)
	}
	return user, rows.Err()
}

func loadUserByHandle(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, domain string, handle []byte) (*User, error) {
	encodedHandle := base64.RawURLEncoding.EncodeToString(handle)
	var email string
	err := database.QueryRowContext(ctx, `SELECT email FROM account_passkeys WHERE domain=? AND user_handle=? LIMIT 1`, domain, encodedHandle).Scan(&email)
	if err != nil {
		return nil, err
	}
	return loadUser(ctx, database, domain, email)
}

func storeChallenge(ctx context.Context, transaction *sql.Tx, domain, email, kind, clientIP, returnPath string, options any, session *webauthn.SessionData, now time.Time) (BeginResult, error) {
	token, err := randomToken()
	if err != nil {
		return BeginResult{}, err
	}
	sessionJSON, err := json.Marshal(session)
	if err != nil {
		return BeginResult{}, err
	}
	if _, err = transaction.ExecContext(ctx, `DELETE FROM account_webauthn_challenges WHERE created_at<?`, now.Add(-ChallengeTTL).Unix()); err != nil {
		return BeginResult{}, err
	}
	_, err = transaction.ExecContext(ctx, `INSERT INTO account_webauthn_challenges(token,domain,email,kind,session_json,client_ip,return_path,created_at) VALUES(?,?,?,?,?,?,?,?)`,
		token, domain, email, kind, string(sessionJSON), clientIP, returnPath, now.Unix())
	if err != nil {
		return BeginResult{}, err
	}
	return BeginResult{Token: token, Options: options}, nil
}

func consumeChallenge(ctx context.Context, database *sql.Tx, domain, email, kind, clientIP, token string, now time.Time) (webauthn.SessionData, error) {
	var sessionJSON string
	var createdAt int64
	err := database.QueryRowContext(ctx, `SELECT session_json,created_at FROM account_webauthn_challenges WHERE token=? AND domain=? AND email=? AND kind=? AND client_ip=?`,
		token, domain, email, kind, clientIP).Scan(&sessionJSON, &createdAt)
	if err != nil {
		return webauthn.SessionData{}, err
	}
	if now.Unix()-createdAt >= int64(ChallengeTTL/time.Second) {
		return webauthn.SessionData{}, errors.New("expired WebAuthn challenge")
	}
	result, err := database.ExecContext(ctx, `DELETE FROM account_webauthn_challenges WHERE token=? AND domain=? AND kind=?`, token, domain, kind)
	if err != nil {
		return webauthn.SessionData{}, err
	}
	deleted, err := result.RowsAffected()
	if err != nil || deleted != 1 {
		if err == nil {
			err = errors.New("WebAuthn challenge already consumed")
		}
		return webauthn.SessionData{}, err
	}
	var session webauthn.SessionData
	if err = json.Unmarshal([]byte(sessionJSON), &session); err != nil {
		return webauthn.SessionData{}, err
	}
	return session, nil
}

func consumeLoginChallenge(ctx context.Context, database *sql.Tx, domain, clientIP, token string, now time.Time) (webauthn.SessionData, string, error) {
	var sessionJSON, returnPath string
	var createdAt int64
	err := database.QueryRowContext(ctx, `SELECT session_json,return_path,created_at FROM account_webauthn_challenges WHERE token=? AND domain=? AND kind='login' AND client_ip=?`,
		token, domain, clientIP).Scan(&sessionJSON, &returnPath, &createdAt)
	if err != nil {
		return webauthn.SessionData{}, "", err
	}
	if now.Unix()-createdAt >= int64(ChallengeTTL/time.Second) {
		return webauthn.SessionData{}, "", errors.New("expired WebAuthn challenge")
	}
	result, err := database.ExecContext(ctx, `DELETE FROM account_webauthn_challenges WHERE token=? AND domain=? AND kind='login'`, token, domain)
	if err != nil {
		return webauthn.SessionData{}, "", err
	}
	deleted, err := result.RowsAffected()
	if err != nil || deleted != 1 {
		if err == nil {
			err = errors.New("WebAuthn challenge already consumed")
		}
		return webauthn.SessionData{}, "", err
	}
	var session webauthn.SessionData
	if err = json.Unmarshal([]byte(sessionJSON), &session); err != nil {
		return webauthn.SessionData{}, "", err
	}
	return session, returnPath, nil
}

func randomToken() (string, error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", buffer), nil
}
