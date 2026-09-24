package database

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestShortLinkValidationAndCollisionBranches(t *testing.T) {
	var nilDB *Database
	if _, _, err := nilDB.PreviewShortLink(context.Background(), "https://example.com", 8); err == nil {
		t.Fatal("nil database preview did not fail")
	}
	if _, err := nilDB.PersistShortLink(context.Background(), "https://example.com", "", time.Now(), 8); err == nil {
		t.Fatal("nil database persist did not fail")
	}
	if _, err := nilDB.ResolveShortLink(context.Background(), "code"); err == nil {
		t.Fatal("nil database resolve did not fail")
	}

	db, _ := newSQLiteConcurrencyTestDatabase(t)

	if _, _, err := db.PreviewShortLink(context.Background(), " ", 8); err == nil {
		t.Fatal("empty preview target did not fail")
	}
	if _, _, err := db.PreviewShortLink(context.Background(), strings.Repeat("x", 4097), 8); err == nil {
		t.Fatal("oversized preview target did not fail")
	}
	if _, err := db.PersistShortLink(context.Background(), " ", "", time.Now(), 8); err == nil {
		t.Fatal("empty persist target did not fail")
	}
	if _, err := db.PersistShortLink(context.Background(), strings.Repeat("x", 4097), "", time.Now(), 8); err == nil {
		t.Fatal("oversized persist target did not fail")
	}
	if _, err := db.PersistShortLink(context.Background(), "https://example.com/a", "not-valid!", time.Now(), 8); err == nil {
		t.Fatal("invalid short code did not fail")
	}

	code, err := db.PersistShortLink(context.Background(), "https://example.com/a", "AbC123", time.Unix(100, 0), 8)
	if err != nil {
		t.Fatalf("persist explicit short link: %v", err)
	}
	if code != "AbC123" {
		t.Fatalf("explicit short code = %q", code)
	}

	existing, stored, err := db.PreviewShortLink(context.Background(), "https://example.com/a", 8)
	if err != nil {
		t.Fatalf("preview existing short link: %v", err)
	}
	if !stored || existing != "AbC123" {
		t.Fatalf("existing preview = %q stored=%v", existing, stored)
	}

	code, err = db.PersistShortLink(context.Background(), "https://example.com/a", "Other123", time.Now(), 8)
	if err != nil {
		t.Fatalf("persist existing target: %v", err)
	}
	if code != "AbC123" {
		t.Fatalf("existing target returned %q, want original code", code)
	}

	// Supplying an already used code must fall back to a fresh generated code.
	code, err = db.PersistShortLink(context.Background(), "https://example.com/b", "AbC123", time.Now(), 6)
	if err != nil {
		t.Fatalf("persist colliding code: %v", err)
	}
	if code == "AbC123" || len(code) != 6 || !isBase62(code) {
		t.Fatalf("collision fallback code = %q", code)
	}

	target, err := db.ResolveShortLink(context.Background(), "missing")
	if err != nil {
		t.Fatalf("resolve missing link: %v", err)
	}
	if target != "" {
		t.Fatalf("missing target = %q", target)
	}
	if target, err = db.ResolveShortLink(context.Background(), " "); err != nil || target != "" {
		t.Fatalf("blank resolve = %q, %v", target, err)
	}

	if exists, err := db.shortCodeExists(context.Background(), " "); err != nil || exists {
		t.Fatalf("blank code exists = %v, %v", exists, err)
	}
	if exists, err := db.shortCodeExists(context.Background(), "AbC123"); err != nil || !exists {
		t.Fatalf("stored code exists = %v, %v", exists, err)
	}
	if exists, err := db.shortCodeExists(context.Background(), "Missing9"); err != nil || exists {
		t.Fatalf("missing code exists = %v, %v", exists, err)
	}

	generated, err := randomBase62String(0)
	if err != nil {
		t.Fatalf("generate default base62: %v", err)
	}
	if len(generated) != defaultShortCodeLength || !isBase62(generated) {
		t.Fatalf("default generated code = %q", generated)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := db.randomUnusedCode(ctx, 8); err != context.Canceled {
		t.Fatalf("cancelled random code error = %v, want context.Canceled", err)
	}
}

func TestUserIdentityValidationAndIdempotencyBranches(t *testing.T) {
	db, _ := newSQLiteConcurrencyTestDatabase(t)

	if id, name, err := db.ResolveUserBySource(context.Background(), " ", "id", "sqlite"); err != nil || id != "" || name != "" {
		t.Fatalf("blank source resolution = %q %q %v", id, name, err)
	}
	if id, err := db.EnsureUserBySource(context.Background(), "source", " ", "name", "sqlite"); err != nil || id != "" {
		t.Fatalf("blank source user ensure = %q %v", id, err)
	}

	id, err := db.EnsureUserBySource(context.Background(), "provider", "external-1", "", "sqlite")
	if err != nil {
		t.Fatalf("ensure new user: %v", err)
	}
	if id == "" {
		t.Fatal("new user id is empty")
	}

	resolvedID, name, err := db.ResolveUserBySource(context.Background(), "provider", "external-1", "sqlite")
	if err != nil {
		t.Fatalf("resolve user: %v", err)
	}
	if resolvedID != id || name != "" {
		t.Fatalf("resolved user = %q %q, want %q blank-name", resolvedID, name, id)
	}

	again, err := db.EnsureUserBySource(context.Background(), "provider", "external-1", "Alice", "sqlite")
	if err != nil {
		t.Fatalf("ensure existing user with name: %v", err)
	}
	if again != id {
		t.Fatalf("existing user id changed: %q -> %q", id, again)
	}

	resolvedID, name, err = db.ResolveUserBySource(context.Background(), "provider", "external-1", "sqlite")
	if err != nil {
		t.Fatalf("resolve named user: %v", err)
	}
	if resolvedID != id || name != "Alice" {
		t.Fatalf("named user = %q %q", resolvedID, name)
	}

	if err := db.UpdateUserNameIfEmpty(context.Background(), "", "name", "sqlite"); err != nil {
		t.Fatalf("blank user name update: %v", err)
	}
	if err := db.UpdateUserNameIfEmpty(context.Background(), id, "", "sqlite"); err != nil {
		t.Fatalf("blank name update: %v", err)
	}
	if err := db.UpdateUserNameIfEmpty(context.Background(), id, "Bob", "sqlite"); err != nil {
		t.Fatalf("non-empty name update: %v", err)
	}
	_, name, err = db.ResolveUserBySource(context.Background(), "provider", "external-1", "sqlite")
	if err != nil {
		t.Fatalf("resolve preserved name: %v", err)
	}
	if name != "Alice" {
		t.Fatalf("existing non-empty name was overwritten: %q", name)
	}

	if err := db.EnsureTrackUser(context.Background(), "", id, "source", "sqlite"); err != nil {
		t.Fatalf("blank track user: %v", err)
	}
	if err := db.EnsureTrackUser(context.Background(), "track-a", "", "source", "sqlite"); err != nil {
		t.Fatalf("blank user track: %v", err)
	}
	if err := db.EnsureTrackUser(context.Background(), "track-a", id, "", "sqlite"); err != nil {
		t.Fatalf("ensure track user default source: %v", err)
	}
	if err := db.EnsureTrackUser(context.Background(), "track-a", id, "again", "sqlite"); err != nil {
		t.Fatalf("ensure duplicate track user: %v", err)
	}

	generated, err := newUserID()
	if err != nil {
		t.Fatalf("new user id: %v", err)
	}
	if len(generated) != 32 {
		t.Fatalf("new user id length = %d, want 32", len(generated))
	}
}
