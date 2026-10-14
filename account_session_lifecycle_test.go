package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/matveynator/sitebrush/v2/pkg/accountauth"
)

func TestAccountSessionPersistentCookieAndRollingIdleExpiry(t *testing.T) {
	application, database := newTestApplication(t)
	if _, err := database.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES('localhost','idle@example.org','old',1)`); err != nil {
		t.Fatal(err)
	}
	cookie := newAdminSessionCookie(t, application, "idle@example.org")
	if cookie.MaxAge != 1296000 || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode || cookie.Expires.Before(time.Now().Add(accountauth.SessionTTL-time.Minute)) {
		t.Fatalf("login cookie is not persistent: %+v", cookie)
	}
	for _, scenario := range []struct {
		name, destination string
		idle              time.Duration
		renewed, active   bool
	}{
		{"foreground", "document", 14 * 24 * time.Hour, true, true},
		{"background", "empty", 14 * 24 * time.Hour, false, true},
		{"expired", "document", 16 * 24 * time.Hour, false, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			lastUsed := time.Now().Add(-scenario.idle).Unix()
			if _, err := database.Exec(`UPDATE sessions SET last_used_at=? WHERE token=?`, lastUsed, cookie.Value); err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodGet, "http://localhost/?profile", nil)
			request.Header.Set("Sec-Fetch-Dest", scenario.destination)
			request.AddCookie(cookie)
			response := httptest.NewRecorder()
			if !application.enforceAdminIPAllowlist(response, request, "localhost") {
				t.Fatal(response.Body.String())
			}
			if application.isAdminRequest(request) != scenario.active {
				t.Fatal("incorrect authentication state")
			}
			var observed int64
			if err := database.QueryRow(`SELECT last_used_at FROM sessions WHERE token=?`, cookie.Value).Scan(&observed); err != nil {
				t.Fatal(err)
			}
			if (observed > lastUsed) != scenario.renewed {
				t.Fatal("background or expired session renewed")
			}
			if scenario.renewed && len(response.Result().Cookies()) != 1 {
				t.Fatal("renewed browser cookie missing")
			}
		})
	}
	if _, err := database.Exec(`DELETE FROM sessions WHERE token=?`, cookie.Value); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "http://localhost/?profile", nil)
	response := httptest.NewRecorder()
	if !application.renewAccountSession(response, request, "localhost", "idle@example.org", cookie.Value, time.Now()) || len(response.Result().Cookies()) != 0 {
		t.Fatal("revoked session was recreated")
	}
}
