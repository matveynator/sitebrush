package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSecurityBoundaryAuthenticatedMutationsRejectHostileOrigin(t *testing.T) {
	application, rawDB := newTestApplication(t)
	if _, err := rawDB.Exec(
		`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`,
		"localhost", "admin@example.com", "password",
	); err != nil {
		t.Fatal(err)
	}
	adminCookie := newAdminSessionCookie(t, application, "admin@example.com")

	for _, requestPath := range []string{
		"/?freeze",
		"/?publish",
		"/?settings",
		"/?files",
		"/?backup_import",
		"/docs?page_password=remove",
		"/?revision_toggle",
		"/?revision_restore",
		"/?revision_delete",
		"/?save",
	} {
		t.Run(requestPath, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "http://localhost:8080"+requestPath, strings.NewReader("action=test"))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			request.Header.Set("Origin", "https://attacker.example")
			request.AddCookie(adminCookie)
			response := httptest.NewRecorder()

			application.route(response, request)

			if response.Code != http.StatusForbidden {
				t.Fatalf("SECURITY: hostile-origin mutation %s status=%d body=%q", requestPath, response.Code, response.Body.String())
			}
		})
	}
}

func TestSecurityBoundaryAuthenticatedMutationAllowsSameOrigin(t *testing.T) {
	application, rawDB := newTestApplication(t)
	if _, err := rawDB.Exec(
		`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`,
		"localhost", "admin@example.com", "password",
	); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "http://localhost:8080/?freeze", strings.NewReader(""))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Origin", "http://localhost:8080")
	request.AddCookie(newAdminSessionCookie(t, application, "admin@example.com"))
	response := httptest.NewRecorder()

	application.route(response, request)

	if response.Code == http.StatusForbidden {
		t.Fatalf("same-origin admin mutation was rejected: %q", response.Body.String())
	}
}
