package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log"
	"math/big"
	"net/http"
	"net/mail"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/matveynator/sitebrush/v2/pkg/captcha"
	"github.com/matveynator/sitebrush/v2/pkg/diagnosticlog"
	"github.com/matveynator/sitebrush/v2/pkg/hostingandsupport"
	"github.com/matveynator/sitebrush/v2/pkg/mailout"
	"golang.org/x/net/websocket"
)

const testDriveCodeTime = 10 * time.Minute

func testDriveLabelValid(label string) bool {
	if len(label) < 1 || len(label) > 24 || label[0] == '-' || label[len(label)-1] == '-' {
		return false
	}
	for index := 0; index < len(label); index++ {
		letter := label[index]
		if letter >= 'a' && letter <= 'z' || letter >= '0' && letter <= '9' || letter == '-' {
			continue
		}
		return false
	}
	return true
}

type privateTestDrive struct {
	ID             int64
	Domain         string
	Status         string
	SessionHash    string
	HandoffHash    string
	TemporaryAdmin string
	PendingEmail   string
	VerifiedEmail  string
	EmailHistory   string
	CodeHash       string
	CodeExpiresAt  string
	CodeAttempts   int
	MailCount      int
	LastMailAt     string
	DeleteAfter    string
	CreatedAt      string
	LastActiveAt   string
	LastIP         string
	HumanVerified  bool
}

func testDriveHash(secret string) string {
	digest := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(digest[:])
}

func (a *App) privateTestDrive(ctx context.Context, domain string) (privateTestDrive, bool, error) {
	var trial privateTestDrive
	err := a.withServerControlDatabaseRead(ctx, "test-drive-read", func(database *sql.DB) error {
		return database.QueryRowContext(ctx, `SELECT id,domain,status,session_hash,handoff_hash,temporary_admin,pending_email,verified_email,email_history,code_hash,code_expires_at,code_attempts,mail_count,last_mail_at,delete_after,created_at,COALESCE(last_active_at,''),COALESCE(last_ip,''),human_verified FROM public_trial_sites WHERE domain=? AND status IN ('private','email_pending','email_verified','expiring','blocked','active') ORDER BY id DESC LIMIT 1`, domain).Scan(&trial.ID, &trial.Domain, &trial.Status, &trial.SessionHash, &trial.HandoffHash, &trial.TemporaryAdmin, &trial.PendingEmail, &trial.VerifiedEmail, &trial.EmailHistory, &trial.CodeHash, &trial.CodeExpiresAt, &trial.CodeAttempts, &trial.MailCount, &trial.LastMailAt, &trial.DeleteAfter, &trial.CreatedAt, &trial.LastActiveAt, &trial.LastIP, &trial.HumanVerified)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return privateTestDrive{}, false, nil
	}
	return trial, err == nil, err
}

func testDriveOwner(r *http.Request, trial privateTestDrive) bool {
	cookie, err := r.Cookie("sitebrush_test_drive")
	if err != nil || trial.SessionHash == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(testDriveHash(cookie.Value)), []byte(trial.SessionHash)) == 1
}

func testDriveIdleDuration(trial privateTestDrive) time.Duration {
	lastActive, activeErr := time.Parse(time.RFC3339, trial.LastActiveAt)
	expiry, expiryErr := time.Parse(time.RFC3339, trial.DeleteAfter)
	if activeErr == nil && expiryErr == nil {
		idleDuration := expiry.Sub(lastActive)
		if idleDuration >= time.Minute && idleDuration <= 7*24*time.Hour {
			return idleDuration
		}
	}
	return 30 * time.Minute
}

// The gate runs before the public file reader and before application routing.
// The trial cookie grants only the editor-facing routes on its own hostname.
func (a *App) privateTestDriveGate(w http.ResponseWriter, r *http.Request) bool {
	domain := normalizeDomainName(domainFromRequest(r))
	trial, found, readErr := a.privateTestDrive(r.Context(), domain)
	if readErr != nil {
		a.logTestDriveError("gate", 0, r, readErr)
		testDriveHTTPError(w, r, "test_drive_generic_error", http.StatusServiceUnavailable)
		return true
	}
	if !found || trial.Status == "active" && trial.VerifiedEmail != "" {
		return false
	}
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Robots-Tag", "noindex, nofollow")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if trial.Status == "private" || trial.Status == "email_pending" || trial.Status == "active" {
		expiry, err := time.Parse(time.RFC3339, trial.DeleteAfter)
		if err == nil && !time.Now().UTC().Before(expiry) {
			a.renderTestDriveNeutral(w, r)
			return true
		}
	}
	if trial.Status == "expiring" || trial.Status == "blocked" {
		a.renderTestDriveNeutral(w, r)
		return true
	}
	if hasQueryFlag(r, "test_drive_handoff") {
		a.testDriveHandoff(w, r, trial)
		return true
	}
	if !testDriveOwner(r, trial) {
		a.renderTestDriveNeutral(w, r)
		return true
	}
	if hasQueryFlag(r, "test_drive_email") || hasQueryFlag(r, "test_drive_verify") || hasQueryFlag(r, "test_drive_activate") || hasQueryFlag(r, "test_drive_status") || hasQueryFlag(r, "test_drive_captcha") || hasQueryFlag(r, "test_drive_ws") {
		a.touchTestDrive(w, r, trial)
		a.testDriveOwnerAction(w, r, trial)
		return true
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodPost {
		testDriveHTTPError(w, r, "test_drive_generic_error", http.StatusForbidden)
		return true
	}
	if r.Method == http.MethodPost && !testDriveEditorMutation(r) {
		testDriveHTTPError(w, r, "test_drive_publish_requires_email", http.StatusForbidden)
		return true
	}
	if testDriveExternalAction(r) {
		testDriveHTTPError(w, r, "test_drive_publish_requires_email", http.StatusForbidden)
		return true
	}
	if !strings.HasPrefix(r.URL.Path, "/p/") && (r.Method == http.MethodPost || r.URL.RawQuery == "" || hasQueryFlag(r, "edit") || hasQueryFlag(r, "visual") || hasQueryFlag(r, "text")) {
		a.touchTestDrive(w, r, trial)
	}
	return false
}

func testDriveEditorMutation(r *http.Request) bool {
	for _, flag := range []string{"save", "files", "grab", "grab_preview", "grab_retry", "grab_cancel", "revision_restore", "revision_delete", "revision_toggle"} {
		if hasQueryFlag(r, flag) {
			return true
		}
	}
	return false
}

func testDriveExternalAction(r *http.Request) bool {
	for flag := range r.URL.Query() {
		if sitebrushControlQueryFlag(flag) {
			allowed := false
			for _, editorFlag := range []string{"edit", "visual", "text", "save", "files", "tree", "revisions", "revision_preview", "revision_restore", "revision_delete", "revision_toggle", "template_events", "grab", "grab_preview", "grab_retry", "grab_cancel", "grab_ws", "grab_events", "test_drive_status"} {
				if flag == editorFlag {
					allowed = true
					break
				}
			}
			if !allowed {
				return true
			}
		}
	}
	return false
}

func (a *App) renderTestDriveNeutral(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusForbidden)
	a.render(w, r, "test_drive_neutral.html", nil)
}

func (a *App) touchTestDrive(w http.ResponseWriter, r *http.Request, trial privateTestDrive) {
	now := time.Now().UTC()
	lastActive, _ := time.Parse(time.RFC3339, trial.DeleteAfter)
	idleDuration := testDriveIdleDuration(trial)
	refreshInterval := idleDuration / 3
	if refreshInterval > time.Minute {
		refreshInterval = time.Minute
	}
	ip := accountClientIP(r)
	if lastActive.After(now.Add(idleDuration-refreshInterval)) && ip == trial.LastIP {
		return
	}
	_ = a.withServerControlDatabaseWrite(r.Context(), "test-drive-activity", func(database *sql.DB) error {
		var lastIP, historyJSON string
		if err := database.QueryRowContext(r.Context(), `SELECT last_ip,ip_history FROM public_trial_sites WHERE id=?`, trial.ID).Scan(&lastIP, &historyJSON); err != nil {
			return err
		}
		if ip != lastIP {
			var history []map[string]string
			if historyJSON != "" && json.Unmarshal([]byte(historyJSON), &history) != nil {
				return errors.New("IP history unavailable")
			}
			history = append(history, map[string]string{"ip": ip, "at": now.Format(time.RFC3339)})
			if len(history) > 8 {
				history = history[len(history)-8:]
			}
			encoded, err := json.Marshal(history)
			if err != nil {
				return err
			}
			historyJSON = string(encoded)
		}
		_, err := database.ExecContext(r.Context(), `UPDATE public_trial_sites SET last_active_at=?,last_ip=?,ip_history=?,delete_after=? WHERE id=? AND status IN ('private','email_pending')`, now.Format(time.RFC3339), ip, historyJSON, now.Add(idleDuration).Format(time.RFC3339), trial.ID)
		return err
	})
	if cookie, err := r.Cookie("sitebrush_test_drive"); err == nil {
		http.SetCookie(w, &http.Cookie{Name: "sitebrush_test_drive", Value: cookie.Value, Path: "/", HttpOnly: true, Secure: !localAccountRequest(r), SameSite: http.SameSiteLaxMode, MaxAge: int(idleDuration.Seconds())})
	}
}

func (a *App) testDriveEntrySession(w http.ResponseWriter, r *http.Request) (string, bool) {
	if cookie, err := r.Cookie("sitebrush_trial_entry"); err == nil {
		hash := testDriveHash(cookie.Value)
		var verified bool
		if a.withServerControlDatabaseRead(r.Context(), "test-drive-entry-read", func(database *sql.DB) error {
			return database.QueryRowContext(r.Context(), `SELECT human_verified FROM test_drive_entry_sessions WHERE session_hash=? AND last_used_at>?`, hash, time.Now().Add(-24*time.Hour).Unix()).Scan(&verified)
		}) == nil {
			return hash, verified
		}
	}
	secret := randomAccessToken() + randomAccessToken()
	hash := testDriveHash(secret)
	now := time.Now().Unix()
	if err := a.withServerControlDatabaseWrite(r.Context(), "test-drive-entry-create", func(database *sql.DB) error {
		_, _ = database.ExecContext(r.Context(), `DELETE FROM test_drive_entry_sessions WHERE last_used_at<?`, now-86400)
		_, err := database.ExecContext(r.Context(), `INSERT INTO test_drive_entry_sessions(session_hash,created_at,last_used_at,human_verified) VALUES(?,?,?,0)`, hash, now, now)
		return err
	}); err != nil {
		return "", false
	}
	http.SetCookie(w, &http.Cookie{Name: "sitebrush_trial_entry", Value: secret, Path: "/", HttpOnly: true, Secure: !localAccountRequest(r), SameSite: http.SameSiteLaxMode})
	return hash, false
}

func (a *App) testDriveForm(w http.ResponseWriter, r *http.Request) {
	if !testDriveRequestHTTPSOrLocal(w, r) {
		return
	}
	if r.Method != http.MethodGet || !a.publicTrialAllowed(r) {
		testDriveHTTPError(w, r, "test_drive_generic_error", http.StatusForbidden)
		return
	}
	hash, trusted := a.testDriveEntrySession(w, r)
	if hash == "" {
		testDriveHTTPError(w, r, "test_drive_generic_error", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	a.renderTestDriveForm(w, r, trusted, "")
}

func (a *App) renderTestDriveForm(w http.ResponseWriter, r *http.Request, trusted bool, formError string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	a.render(w, r, "test_drive.html", map[string]any{"ParentDomain": a.hostingAndSupportMainDomain(r.Context()), "HumanVerified": trusted, "FormError": formError})
}

func (a *App) startPrivateTestDrive(w http.ResponseWriter, r *http.Request) {
	if !testDriveRequestHTTPSOrLocal(w, r) {
		return
	}
	if r.Method != http.MethodPost || !a.publicTrialAllowed(r) || !adminMutationSourceAllowed(r) {
		testDriveHTTPError(w, r, "test_drive_generic_error", http.StatusForbidden)
		return
	}
	entryHash, trusted := a.testDriveEntrySession(w, r)
	if entryHash == "" {
		testDriveHTTPError(w, r, "test_drive_generic_error", http.StatusServiceUnavailable)
		return
	}
	label := r.FormValue("subdomain")
	if !testDriveLabelValid(label) {
		a.recordTestDriveSuspicion(r.Context(), entryHash, 0, "invalid_name")
		testDriveHTTPError(w, r, "public_trial_address_invalid", http.StatusBadRequest)
		return
	}
	if !trusted {
		if !a.verifyPublicTrialCaptcha(r) {
			testDriveHTTPError(w, r, "test_drive_captcha_required", http.StatusForbidden)
			return
		}
		if err := a.withServerControlDatabaseWrite(r.Context(), "test-drive-human-verified", func(database *sql.DB) error {
			_, err := database.ExecContext(r.Context(), `UPDATE test_drive_entry_sessions SET human_verified=1,last_used_at=? WHERE session_hash=?`, time.Now().Unix(), entryHash)
			return err
		}); err != nil {
			testDriveHTTPError(w, r, "test_drive_generic_error", http.StatusServiceUnavailable)
			return
		}
		trusted = true
	}
	parentDomain := a.hostingAndSupportMainDomain(r.Context())
	domain := label + "." + parentDomain
	if !a.newSiteSubdomainAllowed(r.Context(), domain) || !a.publicTrialDomainAvailable(r.Context(), domain) {
		testDriveHTTPError(w, r, "public_trial_address_unavailable", http.StatusConflict)
		return
	}
	ip := accountClientIP(r)
	var activeSession, activeIP, recentSession, recentIP int
	var maxActiveSession, maxActiveIP, maxRecentSession, maxRecentIP int
	limitErr := a.withServerControlDatabaseRead(r.Context(), "test-drive-create-limits", func(database *sql.DB) error {
		maxActiveSession = a.testDriveLimit(r.Context(), database, "test_drive_active_per_session", 1)
		maxActiveIP = a.testDriveLimit(r.Context(), database, "test_drive_active_per_ip", 2)
		maxRecentSession = a.testDriveLimit(r.Context(), database, "test_drive_daily_per_session", 3)
		maxRecentIP = a.testDriveLimit(r.Context(), database, "test_drive_daily_per_ip", 5)
		for _, count := range []struct {
			query  string
			args   []any
			target *int
		}{
			{`SELECT COUNT(*) FROM public_trial_sites WHERE entry_hash=? AND status IN ('private','email_pending','email_verified')`, []any{entryHash}, &activeSession},
			{`SELECT COUNT(*) FROM public_trial_sites WHERE client_ip=? AND status IN ('private','email_pending','email_verified')`, []any{ip}, &activeIP},
			{`SELECT COUNT(*) FROM public_trial_sites WHERE entry_hash=? AND created_at>?`, []any{entryHash, time.Now().Add(-24 * time.Hour).UTC().Format(time.RFC3339)}, &recentSession},
			{`SELECT COUNT(*) FROM public_trial_sites WHERE client_ip=? AND created_at>?`, []any{ip, time.Now().Add(-24 * time.Hour).UTC().Format(time.RFC3339)}, &recentIP},
		} {
			if err := database.QueryRowContext(r.Context(), count.query, count.args...).Scan(count.target); err != nil {
				return err
			}
		}
		return nil
	})
	if limitErr != nil || activeIP >= maxActiveIP || recentIP >= maxRecentIP || activeSession >= maxActiveSession || recentSession >= maxRecentSession {
		if limitErr != nil {
			a.logTestDriveError("create-limit", 0, r, limitErr)
		}
		a.recordTestDriveSuspicion(r.Context(), entryHash, 0, "create_limit")
		_ = a.withServerControlDatabaseWrite(r.Context(), "test-drive-create-reset-captcha", func(database *sql.DB) error {
			_, err := database.ExecContext(r.Context(), `UPDATE test_drive_entry_sessions SET human_verified=0 WHERE session_hash=?`, entryHash)
			return err
		})
		w.WriteHeader(http.StatusTooManyRequests)
		a.renderTestDriveForm(w, r, false, translationsForRequest(r)["test_drive_limit_error"])
		return
	}
	claim := randomAccessToken()
	if err := a.claimTrialDomain(r.Context(), domain, claim); err != nil {
		testDriveHTTPError(w, r, "public_trial_address_unavailable", http.StatusConflict)
		return
	}
	created := false
	defer func() {
		if !created {
			a.releaseTrialDomain(context.Background(), domain, claim)
		}
	}()
	quota := a.creationSizeLimits(r.Context()).TestDrive
	if err := a.createManagedSiteWithoutAdmin(r.Context(), domain, quota); err != nil {
		a.logTestDriveError("create-site", 0, r, err)
		testDriveHTTPError(w, r, "test_drive_generic_error", http.StatusServiceUnavailable)
		return
	}
	domainContext := contextWithSiteDatabaseCreation(contextWithDomain(r.Context(), domain))
	temporaryEmail := "trial-" + randomAccessToken() + "@sitebrush.invalid"
	pageHTML := "<!doctype html><html><head><meta charset=\"utf-8\"><title>SiteBrush</title></head><body><h1>SiteBrush</h1></body></html>"
	if err := a.siteContentTransaction(domainContext, func(transaction *sql.Tx) error {
		if _, err := transaction.ExecContext(domainContext, `INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, domain, temporaryEmail, randomAccessToken()); err != nil {
			return err
		}
		_, err := transaction.ExecContext(domainContext, `INSERT INTO pages(domain,path,title,html,published) VALUES(?,?,'SiteBrush',?,0)`, domain, "/", pageHTML)
		return err
	}, func(error) {}); err != nil {
		a.logTestDriveError("create-editor", 0, r, err)
		_ = a.deleteDemoManagedSiteWithoutBackup(r.Context(), domain)
		testDriveHTTPError(w, r, "test_drive_generic_error", http.StatusServiceUnavailable)
		return
	}
	now := time.Now().UTC()
	idleDuration := time.Duration(a.testDriveSetting(r.Context(), "test_drive_idle_minutes", 30)) * time.Minute
	handoff := randomAccessToken() + randomAccessToken()
	if err := a.withServerControlDatabaseWrite(r.Context(), "test-drive-record", func(database *sql.DB) error {
		plan, hasPlan := (hostingandsupport.Store{DB: database}).DefaultPlan(r.Context())
		transaction, err := database.BeginTx(r.Context(), nil)
		if err != nil {
			return err
		}
		defer transaction.Rollback()
		if hasPlan {
			if _, err := transaction.ExecContext(r.Context(), `INSERT INTO site_service_assignments(domain,plan_id,service_status,notes,updated_at) VALUES(?,?,'trial','',?)`, domain, plan.ID, now.Format(time.RFC3339)); err != nil {
				return err
			}
		}
		userAgent := r.UserAgent()
		if len(userAgent) > 256 {
			userAgent = userAgent[:256]
		}
		_, err = transaction.ExecContext(r.Context(), `INSERT INTO public_trial_sites(domain,source_url,status,created_at,last_active_at,delete_after,client_ip,last_ip,user_agent,temporary_admin,handoff_hash,human_verified,ip_history,entry_hash) VALUES(?,'','private',?,?,?,?,?,?,?, ?,1,?,?)`, domain, now.Format(time.RFC3339), now.Format(time.RFC3339), now.Add(idleDuration).Format(time.RFC3339), ip, ip, userAgent, temporaryEmail, testDriveHash(handoff), fmt.Sprintf(`[{"ip":%q,"at":%q}]`, ip, now.Format(time.RFC3339)), entryHash)
		if err != nil {
			return err
		}
		return transaction.Commit()
	}); err != nil {
		a.logTestDriveError("record", 0, r, err)
		_ = a.deleteDemoManagedSiteWithoutBackup(r.Context(), domain)
		testDriveHTTPError(w, r, "test_drive_generic_error", http.StatusServiceUnavailable)
		return
	}
	created = true
	a.refreshHostingAndSupportPanel()
	a.observeAutomaticSSLRegistration(r, domain)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	endpoint := "https://" + domain + "/?test_drive_handoff"
	if localAccountRequest(r) {
		endpoint = "http://" + domain + "/?test_drive_handoff"
	}
	continueLabel := translationOrDefault(translationsForRequest(r), "test_drive_continue", "Continue to Test Drive")
	waitLabel := translationOrDefault(translationsForRequest(r), "test_drive_wait_https", "Preparing your private Test Drive address...")
	_, _ = fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><meta name="referrer" content="no-referrer"><meta name="viewport" content="width=device-width,initial-scale=1"><main style="font:16px/1.5 system-ui,sans-serif;max-width:36rem;margin:12vh auto;padding:1rem"><p>%s</p><form method="post" action="%s" data-test-drive-handoff data-local="%t"><input type="hidden" name="ticket" value="%s"><button type="submit">%s</button></form></main><script defer src="/p/static/test_drive_handoff.js"></script>`, template.HTMLEscapeString(waitLabel), template.HTMLEscapeString(endpoint), localAccountRequest(r), template.HTMLEscapeString(handoff), template.HTMLEscapeString(continueLabel))
}

func testDriveRequestHTTPSOrLocal(w http.ResponseWriter, r *http.Request) bool {
	if httpRequestHTTPSOrLocal(r) {
		return true
	}
	target := "https://" + r.Host + r.URL.RequestURI()
	http.Redirect(w, r, target, http.StatusTemporaryRedirect)
	return false
}

func (a *App) testDriveHandoff(w http.ResponseWriter, r *http.Request, trial privateTestDrive) {
	if r.Method != http.MethodPost || trial.HandoffHash == "" || !httpRequestHTTPSOrLocal(r) || subtle.ConstantTimeCompare([]byte(testDriveHash(r.FormValue("ticket"))), []byte(trial.HandoffHash)) != 1 {
		a.renderTestDriveNeutral(w, r)
		return
	}
	secret := randomAccessToken() + randomAccessToken()
	err := a.withServerControlDatabaseWrite(r.Context(), "test-drive-handoff", func(database *sql.DB) error {
		result, err := database.ExecContext(r.Context(), `UPDATE public_trial_sites SET session_hash=?,handoff_hash='' WHERE id=? AND handoff_hash=?`, testDriveHash(secret), trial.ID, trial.HandoffHash)
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if count != 1 {
			return errors.New("handoff already used")
		}
		return nil
	})
	if err != nil {
		a.logTestDriveError("handoff", trial.ID, r, err)
		a.renderTestDriveNeutral(w, r)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "sitebrush_test_drive", Value: secret, Path: "/", HttpOnly: true, Secure: !localAccountRequest(r), SameSite: http.SameSiteLaxMode, MaxAge: int(testDriveIdleDuration(trial).Seconds())})
	if a.createSessionForDomain(w, r, contextWithDomain(r.Context(), trial.Domain), trial.Domain, trial.TemporaryAdmin) == "" {
		testDriveHTTPError(w, r, "test_drive_generic_error", http.StatusServiceUnavailable)
		return
	}
	http.Redirect(w, r, "/?edit", http.StatusSeeOther)
}

func httpRequestHTTPSOrLocal(r *http.Request) bool {
	return requestScheme(r) == "https" || localAccountRequest(r)
}

func (a *App) testDriveOwnerAction(w http.ResponseWriter, r *http.Request, trial privateTestDrive) {
	w.Header().Set("Cache-Control", "no-store")
	if hasQueryFlag(r, "test_drive_ws") {
		a.testDriveSocket(w, r, trial)
		return
	}
	if hasQueryFlag(r, "test_drive_captcha") && r.Method == http.MethodGet {
		a.testDriveCaptcha(w, r, trial)
		return
	}
	if hasQueryFlag(r, "test_drive_status") && r.Method == http.MethodGet {
		retryAfter := 0
		resendSeconds := a.testDriveSetting(r.Context(), "test_drive_resend_seconds", 60)
		if lastMail, err := time.Parse(time.RFC3339, trial.LastMailAt); err == nil {
			retryAfter = int(time.Until(lastMail.Add(time.Duration(resendSeconds)*time.Second)).Seconds()) + 1
			if retryAfter < 0 {
				retryAfter = 0
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"status": trial.Status, "email": trial.PendingEmail, "verified_email": trial.VerifiedEmail, "mail_count": trial.MailCount, "human_verified": trial.HumanVerified, "retry_after": retryAfter})
		return
	}
	if r.Method != http.MethodPost || !adminMutationSourceAllowed(r) || !sessionCSRFMutationAllowed(r) {
		testDriveHTTPError(w, r, "test_drive_generic_error", http.StatusForbidden)
		return
	}
	if hasQueryFlag(r, "test_drive_email") {
		a.sendTestDriveCode(w, r, trial)
		return
	}
	if hasQueryFlag(r, "test_drive_verify") {
		a.verifyTestDriveCode(w, r, trial)
		return
	}
	if hasQueryFlag(r, "test_drive_activate") {
		a.activatePrivateTestDrive(w, r, trial)
		return
	}
	http.NotFound(w, r)
}

type testDriveSocketCommand struct {
	ID        int    `json:"id"`
	Action    string `json:"action"`
	Email     string `json:"email"`
	Code      string `json:"code"`
	Captcha   string `json:"captcha"`
	CaptchaID string `json:"captcha_id"`
	CSRF      string `json:"csrf"`
}

type testDriveSocketReply struct {
	ID         int             `json:"id"`
	Status     int             `json:"status"`
	Payload    json.RawMessage `json:"payload,omitempty"`
	Error      string          `json:"error,omitempty"`
	RetryAfter string          `json:"retry_after,omitempty"`
}

type testDriveResponseBuffer struct {
	headers http.Header
	body    bytes.Buffer
	status  int
}

func (response *testDriveResponseBuffer) Header() http.Header { return response.headers }

func (response *testDriveResponseBuffer) WriteHeader(status int) { response.status = status }

func (response *testDriveResponseBuffer) Write(content []byte) (int, error) {
	if response.status == 0 {
		response.status = http.StatusOK
	}
	return response.body.Write(content)
}

// The WebSocket adapts browser commands to the existing Test Drive action boundary.
func (a *App) testDriveSocket(w http.ResponseWriter, r *http.Request, trial privateTestDrive) {
	origin, err := url.Parse(r.Header.Get("Origin"))
	if err != nil || origin.Scheme != requestScheme(r) || origin.Host != r.Host || origin.User != nil || origin.Path != "" {
		testDriveHTTPError(w, r, "test_drive_generic_error", http.StatusForbidden)
		return
	}
	websocket.Server{Handshake: func(*websocket.Config, *http.Request) error { return nil }, Handler: func(connection *websocket.Conn) {
		defer connection.Close()
		connection.MaxPayloadBytes = 4096
		_ = connection.SetReadDeadline(time.Now().Add(30 * time.Second))
		var command testDriveSocketCommand
		if receiveErr := websocket.JSON.Receive(connection, &command); receiveErr != nil {
			return
		}
		response := &testDriveResponseBuffer{headers: make(http.Header)}
		actionRequest := r.Clone(r.Context())
		actionRequest.Form = url.Values{
			"email":        {command.Email},
			"code":         {command.Code},
			"captcha":      {command.Captcha},
			"captcha_id":   {command.CaptchaID},
			"account_csrf": {command.CSRF},
		}
		actionRequest.PostForm = actionRequest.Form
		if command.Action == "status" || command.Action == "captcha" {
			actionRequest.Method = http.MethodGet
		} else if command.Action == "email" && command.CSRF != "" && subtle.ConstantTimeCompare([]byte(command.CSRF), []byte(accountCSRF(r))) == 1 {
			actionRequest.Method = http.MethodPost
		} else {
			testDriveHTTPError(response, r, "test_drive_generic_error", http.StatusForbidden)
		}
		if response.status == 0 {
			actionRequest.URL.RawQuery = "test_drive_" + command.Action
			a.testDriveOwnerAction(response, actionRequest, trial)
		}
		reply := testDriveSocketReply{ID: command.ID, Status: response.status, RetryAfter: response.Header().Get("Retry-After")}
		if reply.Status == 0 {
			reply.Status = http.StatusOK
		}
		if reply.Status >= http.StatusBadRequest {
			reply.Error = strings.TrimSpace(response.body.String())
		} else if json.Valid(response.body.Bytes()) {
			reply.Payload = append([]byte(nil), response.body.Bytes()...)
		} else {
			reply.Status = http.StatusInternalServerError
			reply.Error = translationsForRequest(r)["test_drive_generic_error"]
		}
		_ = connection.SetWriteDeadline(time.Now().Add(10 * time.Second))
		_ = websocket.JSON.Send(connection, reply)
	}}.ServeHTTP(w, r)
}

func (a *App) confirmTestDriveNameCheckCaptcha(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !a.publicTrialAllowed(r) || !adminMutationSourceAllowed(r) {
		testDriveHTTPError(w, r, "test_drive_generic_error", http.StatusForbidden)
		return
	}
	entryHash, _ := a.testDriveEntrySession(w, r)
	if entryHash == "" {
		testDriveHTTPError(w, r, "test_drive_generic_error", http.StatusServiceUnavailable)
		return
	}
	if !a.verifyPublicTrialCaptcha(r) {
		testDriveHTTPError(w, r, "test_drive_captcha_required", http.StatusForbidden)
		return
	}
	err := a.withServerControlDatabaseWrite(r.Context(), "test-drive-name-check-captcha", func(database *sql.DB) error {
		_, writeErr := database.ExecContext(r.Context(), `UPDATE test_drive_entry_sessions SET human_verified=1,last_used_at=? WHERE session_hash=?`, time.Now().Unix(), entryHash)
		return writeErr
	})
	if err != nil {
		a.logTestDriveError("name-check-captcha", 0, r, err)
		testDriveHTTPError(w, r, "test_drive_generic_error", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"human_verified":true}`))
}

func (a *App) testDriveCaptcha(w http.ResponseWriter, r *http.Request, trial privateTestDrive) {
	challenge, err := captcha.New()
	if err != nil {
		a.logTestDriveError("captcha-create", trial.ID, r, err)
		testDriveHTTPError(w, r, "test_drive_generic_error", http.StatusServiceUnavailable)
		return
	}
	var admitted bool
	err = a.withAccountMailTransaction(r, "test-drive-captcha", func(transaction *sql.Tx) error {
		var saveErr error
		admitted, saveErr = captcha.Save(transaction, challenge, accountClientIP(r), trial.Domain, "test_drive", time.Now().UTC())
		return saveErr
	})
	if err != nil || !admitted {
		if err != nil {
			a.logTestDriveError("captcha-save", trial.ID, r, err)
		}
		testDriveHTTPError(w, r, "test_drive_limit_error", http.StatusTooManyRequests)
		return
	}
	imageBytes, err := captcha.Render(challenge.Answer)
	if err != nil {
		a.logTestDriveError("captcha-render", trial.ID, r, err)
		testDriveHTTPError(w, r, "test_drive_generic_error", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"captcha_id": challenge.Token, "image": "data:image/png;base64," + base64.StdEncoding.EncodeToString(imageBytes)})
}

func (a *App) verifyTestDriveCaptcha(r *http.Request, trial privateTestDrive) bool {
	verified := false
	err := a.withAccountMailTransaction(r, "test-drive-captcha-verify", func(transaction *sql.Tx) error {
		var verifyErr error
		verified, verifyErr = captcha.Verify(transaction, r.FormValue("captcha_id"), r.FormValue("captcha"), accountClientIP(r), trial.Domain, "test_drive", time.Now().UTC())
		return verifyErr
	})
	if err != nil || !verified {
		return false
	}
	return a.withServerControlDatabaseWrite(r.Context(), "test-drive-human-verified", func(database *sql.DB) error {
		_, err := database.ExecContext(r.Context(), `UPDATE public_trial_sites SET human_verified=1 WHERE id=?`, trial.ID)
		return err
	}) == nil
}

func (a *App) testDriveLimit(ctx context.Context, database *sql.DB, key string, fallback int) int {
	configured, err := strconv.Atoi(hostingandsupport.SettingText(ctx, database, key))
	if err != nil || configured < 1 || configured > 10000 {
		return fallback
	}
	return configured
}

func (a *App) testDriveSetting(ctx context.Context, key string, fallback int) int {
	configured := fallback
	_ = a.withServerControlDatabaseRead(ctx, "test-drive-setting", func(database *sql.DB) error {
		configured = a.testDriveLimit(ctx, database, key, fallback)
		return nil
	})
	return configured
}

func (a *App) recordTestDriveSuspicion(ctx context.Context, sessionHash string, trialID int64, kind string) {
	if sessionHash == "" {
		return
	}
	_ = a.withServerControlDatabaseWrite(ctx, "test-drive-suspicion", func(database *sql.DB) error {
		now := time.Now().Unix()
		_, _ = database.ExecContext(ctx, `DELETE FROM test_drive_suspicion_events WHERE occurred_at<?`, now-600)
		if _, err := database.ExecContext(ctx, `INSERT INTO test_drive_suspicion_events(session_hash,trial_id,kind,occurred_at) VALUES(?,?,?,?)`, sessionHash, trialID, kind, now); err != nil {
			return err
		}
		var count int
		if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM test_drive_suspicion_events WHERE session_hash=? AND occurred_at>?`, sessionHash, now-600).Scan(&count); err != nil {
			return err
		}
		if count < 3 {
			return nil
		}
		if trialID != 0 {
			_, err := database.ExecContext(ctx, `UPDATE public_trial_sites SET human_verified=0 WHERE id=?`, trialID)
			return err
		}
		_, err := database.ExecContext(ctx, `UPDATE test_drive_entry_sessions SET human_verified=0 WHERE session_hash=?`, sessionHash)
		return err
	})
}

func (a *App) admitTestDriveNameCheck(w http.ResponseWriter, r *http.Request) bool {
	entryHash, trusted := a.testDriveEntrySession(w, r)
	if entryHash == "" {
		testDriveHTTPError(w, r, "test_drive_generic_error", http.StatusServiceUnavailable)
		return false
	}
	clientIP := accountClientIP(r)
	var count, softLimit, hardLimit int
	err := a.withServerControlDatabaseWrite(r.Context(), "test-drive-name-check-limit", func(database *sql.DB) error {
		now := time.Now().Unix()
		softLimit = a.testDriveLimit(r.Context(), database, "test_drive_name_checks_soft_minute", 10)
		hardLimit = a.testDriveLimit(r.Context(), database, "test_drive_name_checks_hard_minute", 30)
		if _, err := database.ExecContext(r.Context(), `DELETE FROM test_drive_check_events WHERE checked_at<?`, now-60); err != nil {
			return err
		}
		if err := database.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM test_drive_check_events WHERE (client_ip=? OR session_hash=?) AND checked_at>?`, clientIP, entryHash, now-60).Scan(&count); err != nil {
			return err
		}
		_, err := database.ExecContext(r.Context(), `INSERT INTO test_drive_check_events(client_ip,session_hash,checked_at) VALUES(?,?,?)`, clientIP, entryHash, now)
		if count >= softLimit {
			_, _ = database.ExecContext(r.Context(), `UPDATE test_drive_entry_sessions SET human_verified=0 WHERE session_hash=?`, entryHash)
		}
		return err
	})
	if err != nil {
		testDriveHTTPError(w, r, "test_drive_generic_error", http.StatusServiceUnavailable)
		return false
	}
	if count >= hardLimit || count >= softLimit && !trusted {
		w.Header().Set("Retry-After", "60")
		if count < hardLimit {
			w.Header().Set("X-SiteBrush-Captcha-Required", "1")
		}
		testDriveHTTPError(w, r, "test_drive_limit_error", http.StatusTooManyRequests)
		return false
	}
	if count >= softLimit/2 {
		timer := time.NewTimer(500 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-r.Context().Done():
			return false
		}
	}
	return true
}

func testDriveNewCode() (string, error) {
	number, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", number.Int64()), nil
}

func testDriveCodeDigest(code, salt string) string {
	return salt + ":" + testDriveHash(salt+":"+code)
}

func testDriveCodeMatches(stored, submitted string) bool {
	parts := strings.SplitN(stored, ":", 2)
	if len(parts) != 2 || len(submitted) != 6 {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(stored), []byte(testDriveCodeDigest(submitted, parts[0]))) == 1
}

func (a *App) sendTestDriveCode(w http.ResponseWriter, r *http.Request, trial privateTestDrive) {
	if trial.Status != "private" && trial.Status != "email_pending" {
		testDriveHTTPError(w, r, "test_drive_generic_error", http.StatusConflict)
		return
	}
	if !trial.HumanVerified && !a.verifyTestDriveCaptcha(r, trial) {
		testDriveHTTPError(w, r, "test_drive_captcha_required", http.StatusForbidden)
		return
	}
	email := strings.ToLower(strings.TrimSpace(r.FormValue("email")))
	address, addressErr := mail.ParseAddress(email)
	if addressErr != nil || address.Address != email || strings.ContainsAny(email, "<>\r\n") {
		testDriveHTTPError(w, r, "test_drive_invalid_email", http.StatusBadRequest)
		return
	}
	code, err := testDriveNewCode()
	if err != nil {
		testDriveHTTPError(w, r, "test_drive_generic_error", http.StatusServiceUnavailable)
		return
	}
	salt := randomAccessToken()
	now := time.Now().UTC()
	ip := accountClientIP(r)
	var admitted bool
	resendSeconds := 60
	err = a.withServerControlDatabaseWrite(r.Context(), "test-drive-mail-admit", func(database *sql.DB) error {
		_, _ = database.ExecContext(r.Context(), `DELETE FROM test_drive_mail_events WHERE sent_at<?`, now.Add(-24*time.Hour).Unix())
		resendSeconds = a.testDriveLimit(r.Context(), database, "test_drive_resend_seconds", 60)
		var currentStatus, historyJSON, lastMail string
		var mailCount int
		if err := database.QueryRowContext(r.Context(), `SELECT status,email_history,last_mail_at,mail_count FROM public_trial_sites WHERE id=?`, trial.ID).Scan(&currentStatus, &historyJSON, &lastMail, &mailCount); err != nil {
			return err
		}
		if currentStatus != "private" && currentStatus != "email_pending" {
			return nil
		}
		var previous []string
		if historyJSON != "" && json.Unmarshal([]byte(historyJSON), &previous) != nil {
			return errors.New("email history unavailable")
		}
		seen := false
		for _, candidate := range previous {
			if candidate == email {
				seen = true
				break
			}
		}
		if !seen && len(previous) >= 3 {
			return nil
		}
		if last, parseErr := time.Parse(time.RFC3339, lastMail); parseErr == nil && now.Sub(last) < time.Duration(resendSeconds)*time.Second {
			return nil
		}
		checks := []struct {
			query string
			args  []any
			limit int
		}{
			{`SELECT COUNT(*) FROM test_drive_mail_events WHERE recipient=? AND sent_at>?`, []any{email, now.Add(-time.Hour).Unix()}, a.testDriveLimit(r.Context(), database, "test_drive_mail_per_email_hour", 5)},
			{`SELECT COUNT(*) FROM test_drive_mail_events WHERE client_ip=? AND sent_at>?`, []any{ip, now.Add(-time.Hour).Unix()}, a.testDriveLimit(r.Context(), database, "test_drive_mail_per_ip_hour", 10)},
			{`SELECT COUNT(*) FROM test_drive_mail_events WHERE session_hash=? AND sent_at>?`, []any{trial.SessionHash, now.Add(-time.Hour).Unix()}, a.testDriveLimit(r.Context(), database, "test_drive_mail_per_session_hour", 8)},
		}
		for _, check := range checks {
			var count int
			if err := database.QueryRowContext(r.Context(), check.query, check.args...).Scan(&count); err != nil {
				return err
			}
			if count >= check.limit {
				return nil
			}
		}
		if mailCount >= a.testDriveLimit(r.Context(), database, "test_drive_mail_per_trial", 8) {
			return nil
		}
		if !seen {
			previous = append(previous, email)
		}
		history, err := json.Marshal(previous)
		if err != nil {
			return err
		}
		if _, err = database.ExecContext(r.Context(), `UPDATE public_trial_sites SET pending_email=?,email_history=?,code_hash=?,code_expires_at=?,code_attempts=0,last_mail_at=?,mail_count=mail_count+1,status='email_pending' WHERE id=? AND status IN ('private','email_pending')`, email, string(history), testDriveCodeDigest(code, salt), now.Add(testDriveCodeTime).Format(time.RFC3339), now.Format(time.RFC3339), trial.ID); err != nil {
			return err
		}
		if _, err = database.ExecContext(r.Context(), `INSERT INTO test_drive_mail_events(trial_id,recipient,client_ip,session_hash,sent_at) VALUES(?,?,?,?,?)`, trial.ID, email, ip, trial.SessionHash, now.Unix()); err != nil {
			return err
		}
		admitted = true
		return nil
	})
	if err != nil || !admitted {
		if err != nil {
			a.logTestDriveError("mail-admit", trial.ID, r, err)
		}
		a.recordTestDriveSuspicion(r.Context(), trial.SessionHash, trial.ID, "mail_limit")
		w.Header().Set("Retry-After", strconv.Itoa(resendSeconds))
		testDriveHTTPError(w, r, "test_drive_limit_error", http.StatusTooManyRequests)
		return
	}
	translations := translationsForRequest(r)
	message := mailout.Message{Kind: "email_confirm", From: a.emailFromAddress(a.hostingAndSupportMainDomain(r.Context())), To: email, Subject: translations["test_drive_mail_subject"], Body: fmt.Sprintf(translations["test_drive_mail_body"], code), MonitorDomain: trial.Domain, MonitorClientIP: ip, MonitorFlowID: fmt.Sprintf("test-drive-%d", trial.ID)}
	if err := a.enqueueEmail(r.Context(), message); err != nil {
		a.logTestDriveError("mail-enqueue", trial.ID, r, err)
		_ = a.withServerControlDatabaseWrite(r.Context(), "test-drive-mail-rollback", func(database *sql.DB) error {
			_, _ = database.ExecContext(r.Context(), `DELETE FROM test_drive_mail_events WHERE trial_id=? AND recipient=? AND sent_at=?`, trial.ID, email, now.Unix())
			_, rollbackErr := database.ExecContext(r.Context(), `UPDATE public_trial_sites SET code_hash='',code_expires_at='',mail_count=mail_count-1 WHERE id=? AND code_hash=?`, trial.ID, testDriveCodeDigest(code, salt))
			return rollbackErr
		})
		testDriveHTTPError(w, r, "test_drive_generic_error", http.StatusServiceUnavailable)
		return
	}
	a.refreshHostingAndSupportPanel()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"email": email, "status": "email_pending", "retry_after": resendSeconds})
}

func (a *App) verifyTestDriveCode(w http.ResponseWriter, r *http.Request, trial privateTestDrive) {
	if !trial.HumanVerified && !a.verifyTestDriveCaptcha(r, trial) {
		testDriveHTTPError(w, r, "test_drive_captcha_required", http.StatusForbidden)
		return
	}
	if trial.Status == "email_verified" {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "email_verified", "email": trial.VerifiedEmail})
		return
	}
	if trial.Status != "email_pending" {
		testDriveHTTPError(w, r, "test_drive_generic_error", http.StatusConflict)
		return
	}
	code := r.FormValue("code")
	expires, err := time.Parse(time.RFC3339, trial.CodeExpiresAt)
	valid := err == nil && time.Now().UTC().Before(expires) && trial.CodeAttempts < 5 && testDriveCodeMatches(trial.CodeHash, code)
	var updated bool
	writeErr := a.withServerControlDatabaseWrite(r.Context(), "test-drive-code", func(database *sql.DB) error {
		if valid {
			result, err := database.ExecContext(r.Context(), `UPDATE public_trial_sites SET status='email_verified',verified_email=pending_email,code_hash='',code_expires_at='',code_attempts=code_attempts+1 WHERE id=? AND status='email_pending' AND code_hash=? AND code_attempts<5`, trial.ID, trial.CodeHash)
			if err != nil {
				return err
			}
			count, err := result.RowsAffected()
			updated = err == nil && count == 1
			return err
		}
		_, err := database.ExecContext(r.Context(), `UPDATE public_trial_sites SET code_attempts=code_attempts+1,human_verified=CASE WHEN code_attempts>=2 THEN 0 ELSE human_verified END WHERE id=? AND status='email_pending' AND code_attempts<5`, trial.ID)
		return err
	})
	if writeErr != nil {
		a.logTestDriveError("verify", trial.ID, r, writeErr)
		testDriveHTTPError(w, r, "test_drive_generic_error", http.StatusServiceUnavailable)
		return
	}
	if !updated {
		testDriveHTTPError(w, r, "test_drive_invalid_code", http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"status": "email_verified", "email": trial.PendingEmail})
}

func (a *App) activatePrivateTestDrive(w http.ResponseWriter, r *http.Request, trial privateTestDrive) {
	if trial.Status != "email_verified" || trial.VerifiedEmail == "" {
		testDriveHTTPError(w, r, "test_drive_generic_error", http.StatusConflict)
		return
	}
	if !trial.HumanVerified && !a.verifyTestDriveCaptcha(r, trial) {
		testDriveHTTPError(w, r, "test_drive_captcha_required", http.StatusForbidden)
		return
	}
	domainContext := contextWithDomain(r.Context(), trial.Domain)
	err := a.accountTransaction(domainContext, func(transaction *sql.Tx) error {
		var existing int
		if err := transaction.QueryRowContext(domainContext, `SELECT COUNT(*) FROM users WHERE domain=? AND email=? AND is_admin=1`, trial.Domain, trial.VerifiedEmail).Scan(&existing); err != nil {
			return err
		}
		if existing == 0 {
			_, err := transaction.ExecContext(domainContext, `INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, trial.Domain, trial.VerifiedEmail, randomAccessToken())
			return err
		}
		return nil
	})
	if err == nil {
		var candidates []publishPageCandidate
		candidates, err = a.collectPublishPageCandidates(domainContext, trial.Domain)
		if err == nil {
			for _, candidate := range candidates {
				if _, publishErr := a.publishPageCandidate(domainContext, trial.Domain, candidate); publishErr != nil {
					err = publishErr
					break
				}
			}
		}
	}
	if err == nil {
		err = a.publicPublicationRequest("refresh", trial.Domain)
	}
	if err != nil {
		a.logTestDriveError("activate", trial.ID, r, err)
		testDriveHTTPError(w, r, "test_drive_activation_retry", http.StatusServiceUnavailable)
		return
	}
	if a.createSessionForDomain(w, r, domainContext, trial.Domain, trial.VerifiedEmail) == "" {
		testDriveHTTPError(w, r, "test_drive_activation_retry", http.StatusServiceUnavailable)
		return
	}
	err = a.withServerControlDatabaseWrite(r.Context(), "test-drive-activate", func(database *sql.DB) error {
		transaction, err := database.BeginTx(r.Context(), nil)
		if err != nil {
			return err
		}
		defer transaction.Rollback()
		if _, err := transaction.ExecContext(r.Context(), `UPDATE public_trial_sites SET status='activated',session_hash='',handoff_hash='',temporary_admin='',last_active_at=?,delete_after='' WHERE id=? AND status='email_verified'`, time.Now().UTC().Format(time.RFC3339), trial.ID); err != nil {
			return err
		}
		if _, err := transaction.ExecContext(r.Context(), `DELETE FROM domain_registration_claims WHERE domain=? AND kind='trial'`, trial.Domain); err != nil {
			return err
		}
		if _, err := transaction.ExecContext(r.Context(), `UPDATE site_service_assignments SET service_status='free',updated_at=? WHERE domain=?`, time.Now().UTC().Format(time.RFC3339), trial.Domain); err != nil {
			return err
		}
		return transaction.Commit()
	})
	if err != nil {
		a.logTestDriveError("activate-record", trial.ID, r, err)
		testDriveHTTPError(w, r, "test_drive_activation_retry", http.StatusServiceUnavailable)
		return
	}
	_ = a.accountTransaction(domainContext, func(transaction *sql.Tx) error {
		_, _ = transaction.ExecContext(domainContext, `DELETE FROM sessions WHERE user_email=?`, trial.Domain+"|"+trial.TemporaryAdmin)
		_, err := transaction.ExecContext(domainContext, `DELETE FROM users WHERE domain=? AND email=?`, trial.Domain, trial.TemporaryAdmin)
		return err
	})
	http.SetCookie(w, &http.Cookie{Name: "sitebrush_test_drive", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: !localAccountRequest(r), SameSite: http.SameSiteLaxMode})
	a.refreshHostingAndSupportPanel()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"status": "activated", "domain": trial.Domain})
}

func (a *App) logTestDriveError(endpoint string, trialID int64, r *http.Request, err error) {
	// The request identifier is generated at the boundary and never appears in public responses.
	requestID := r.Header.Get("X-Request-ID")
	if requestID == "" {
		requestID = randomAccessToken()
	}
	adminEmail, _ := a.currentAdminEmail(r)
	log.Printf("TEST_DRIVE error endpoint=%s trial_id=%d request_id=%s admin=%s at=%s error=%s", endpoint, trialID, diagnosticlog.SafeLogValue(requestID), diagnosticlog.SafeLogValue(adminEmail), time.Now().UTC().Format(time.RFC3339), diagnosticlog.SafeLogValue(err.Error()))
}

func testDriveHTTPError(w http.ResponseWriter, r *http.Request, key string, status int) {
	translations := translationsForRequest(r)
	message := translations[key]
	if message == "" {
		message = translations["test_drive_generic_error"]
	}
	http.Error(w, message, status)
}
