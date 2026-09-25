package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"
)

func TestAuthAttackConcurrentEmailChangeConfirmationAppliesOnce(t *testing.T) {
	application, database := newTestApplication(t)
	now := time.Now().UTC()
	const currentEmail = "owner@example.com"
	confirmation := EmailConfirmation{
		Token: "email-change-once", Domain: "localhost", Action: "profile",
		Email: "owner-new@example.com", CurrentEmail: currentEmail,
	}
	if _, err := database.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, confirmation.Domain, currentEmail, "password"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO email_confirmations(token,domain,action,email,current_email,created_at,expires_at) VALUES(?,?,?,?,?,?,?)`,
		confirmation.Token, confirmation.Domain, confirmation.Action, confirmation.Email, confirmation.CurrentEmail,
		now.Format(time.RFC3339), now.Add(time.Hour).Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}

	successfulClaims := concurrentConfirmationClaims(t, func() error {
		return application.applyProfileEmailConfirmation(context.Background(), confirmation)
	})
	if successfulClaims != 1 {
		t.Fatalf("SECURITY: concurrent email-change confirmations succeeded %d times, want one", successfulClaims)
	}
	var updatedEmail string
	if err := database.QueryRow(`SELECT email FROM users WHERE domain=?`, confirmation.Domain).Scan(&updatedEmail); err != nil {
		t.Fatal(err)
	}
	if updatedEmail != confirmation.Email {
		t.Fatalf("confirmed email = %q, want %q", updatedEmail, confirmation.Email)
	}
}

func TestAuthAttackConcurrentPasswordChangeConfirmationAppliesOnce(t *testing.T) {
	application, database := newTestApplication(t)
	now := time.Now().UTC()
	const currentEmail = "owner@example.com"
	confirmation := EmailConfirmation{
		Token: "password-change-once", Domain: "localhost", Action: "profile_password",
		Password: "new-password", CurrentEmail: currentEmail,
	}
	if _, err := database.Exec(`INSERT INTO users(domain,email,password,is_admin) VALUES(?,?,?,1)`, confirmation.Domain, currentEmail, "old-password"); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO email_confirmations(token,domain,action,password,current_email,created_at,expires_at) VALUES(?,?,?,?,?,?,?)`,
		confirmation.Token, confirmation.Domain, confirmation.Action, confirmation.Password, confirmation.CurrentEmail,
		now.Format(time.RFC3339), now.Add(time.Hour).Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}

	successfulClaims := concurrentConfirmationClaims(t, func() error {
		return application.applyProfileCodeConfirmation(context.Background(), confirmation)
	})
	if successfulClaims != 1 {
		t.Fatalf("SECURITY: concurrent password-change confirmations succeeded %d times, want one", successfulClaims)
	}
	var updatedPassword string
	if err := database.QueryRow(`SELECT password FROM users WHERE domain=? AND email=?`, confirmation.Domain, currentEmail).Scan(&updatedPassword); err != nil {
		t.Fatal(err)
	}
	if updatedPassword != confirmation.Password {
		t.Fatalf("confirmed password = %q, want %q", updatedPassword, confirmation.Password)
	}
}

func concurrentConfirmationClaims(t *testing.T, claim func() error) int {
	t.Helper()
	const attempts = 50
	start := make(chan struct{})
	results := make(chan error, attempts)
	var workers sync.WaitGroup
	for attempt := 0; attempt < attempts; attempt++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			results <- claim()
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	return successes
}

func TestAuthAttackConfirmationURLUsesRoutedHostAndTrustedProxyScheme(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "http://example.com/?recover", nil)
	request.Host = "example.com"
	request.RemoteAddr = "10.20.30.40:443"
	request.Header.Set("X-Forwarded-Host", "attacker.example")
	request.Header.Set("X-Forwarded-Proto", "https, http")

	confirmationURL, err := url.Parse(emailConfirmationURL(request, "one-time-secret"))
	if err != nil {
		t.Fatal(err)
	}
	if confirmationURL.Scheme != "https" || confirmationURL.Host != "example.com" || confirmationURL.Query().Get("email_confirm") != "one-time-secret" {
		t.Fatalf("forwarded confirmation URL = %q", confirmationURL)
	}
}
