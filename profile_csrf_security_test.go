package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestSecurityBoundaryProfileSensitiveActionsRequireCSRF(t *testing.T) {
	application, rawDB := newTestApplication(t)
	if _, err := rawDB.Exec(
		`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`,
		"localhost", "admin@example.com", "password",
	); err != nil {
		t.Fatal(err)
	}
	adminCookie := newAdminSessionCookie(t, application, "admin@example.com")

	testCases := []url.Values{
		{"profile_action": {"revoke_ip"}, "trusted_ip": {"203.0.113.7"}},
		{"profile_action": {"passkey_delete"}, "passkey_id": {"credential"}},
		{"profile_action": {"totp_setup"}},
		{"profile_action": {"totp_enable"}, "totp_secret": {"secret"}, "totp_code": {"123456"}},
		{"profile_action": {"totp_disable"}},
	}

	for _, form := range testCases {
		action := form.Get("profile_action")
		t.Run(action, func(t *testing.T) {
			for _, csrf := range []string{"", "attacker-controlled"} {
				requestForm := url.Values{}
				for key, values := range form {
					requestForm[key] = append([]string(nil), values...)
				}
				if csrf != "" {
					requestForm.Set("account_csrf", csrf)
				}
				request := httptest.NewRequest(http.MethodPost, "http://localhost:8080/?profile", strings.NewReader(requestForm.Encode()))
				request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				request.AddCookie(adminCookie)
				response := httptest.NewRecorder()

				application.route(response, request)

				if response.Code != http.StatusForbidden {
					t.Fatalf("SECURITY: profile action %q accepted CSRF=%q with status %d", action, csrf, response.Code)
				}
			}
		})
	}
}
